package audiometa

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- WAV / AIFF

type riffChunk struct {
	ID   string
	Data []byte
}

type riffFile struct {
	path   string
	chunks []riffChunk
	format string // RIFF or FORM
	kind   string // WAVE or AIFF/AIFC
	tag    *id3Tag
}

func readRIFF(f *os.File, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "WAV", Info: Info{Codec: "PCM", Lossless: true}}
	pos := 12
	var dataSize int64
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		l := int(le32(data[pos+4 : pos+8]))
		if l < 0 || pos+8+l > len(data) {
			break
		}
		body := data[pos+8 : pos+8+l]
		switch id {
		case "fmt ":
			if len(body) >= 16 {
				ch := int(le16(body[2:4]))
				sr := int(le32(body[4:8]))
				bps := int(le16(body[14:16]))
				t.Info.Channels = ch
				t.Info.SampleRate = sr
				t.Info.BitDepth = bps
			}
		case "data":
			dataSize = int64(l)
		case "id3 ", "ID3 ", "ID32":
			if tag, _ := readID3Tag(body); tag != nil {
				t.Fields = append(t.Fields, tag.fields()...)
				t.Pictures = append(t.Pictures, tag.pictures()...)
				t.TagType = "ID3v2." + itoa(maxInt(tag.Version, 3))
			}
		case "LIST":
			if len(body) > 4 && string(body[:4]) == "INFO" {
				sub := rippSubchunks(body[4:])
				for _, s := range sub {
					if k := infoTagKey(s.ID); k != "" {
						t.Fields = append(t.Fields, Field{k, strings.TrimRight(string(s.Data), "\x00")})
					}
				}
				if t.TagType == "" || t.TagType == "none" {
					t.TagType = "RIFF INFO"
				}
			}
		}
		pos += 8 + l
		if l%2 == 1 {
			pos++
		}
	}
	if t.Info.SampleRate > 0 && t.Info.Channels > 0 && t.Info.BitDepth > 0 {
		bps := t.Info.SampleRate * t.Info.Channels * t.Info.BitDepth
		t.Info.Bitrate = bps / 1000
		if dataSize > 0 {
			t.Info.Duration = float64(dataSize) / float64(bps/8)
		}
	}
	return t, nil
}

func rippSubchunks(b []byte) []riffChunk {
	var out []riffChunk
	pos := 0
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		l := int(le32(b[pos+4 : pos+8]))
		if l < 0 || pos+8+l > len(b) {
			break
		}
		out = append(out, riffChunk{strings.TrimRight(id, " \x00"), b[pos+8 : pos+8+l]})
		pos += 8 + l
		if l%2 == 1 {
			pos++
		}
	}
	return out
}

func infoTagKey(id string) string {
	switch strings.ToUpper(id) {
	case "INAM":
		return "TITLE"
	case "IART":
		return "ARTIST"
	case "IPRD":
		return "ALBUM"
	case "ICRD":
		return "YEAR"
	case "ITRK", "IPRT":
		return "TRACK"
	case "IGNR":
		return "GENRE"
	case "ICMT":
		return "COMMENT"
	case "ICOP":
		return "COPYRIGHT"
	case "ISFT":
		return "ENCODER"
	}
	return ""
}

func openRIFF(path string) (*riffFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 {
		return nil, ErrUnsupported
	}
	m := &riffFile{path: path, format: string(data[:4]), kind: string(data[8:12])}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		l := int(le32(data[pos+4 : pos+8]))
		if l < 0 || pos+8+l > len(data) {
			break
		}
		if id == "id3 " || id == "ID3 " || id == "ID32" {
			if tag, _ := readID3Tag(data[pos+8 : pos+8+l]); tag != nil {
				m.tag = tag
			}
		} else {
			cp := make([]byte, l)
			copy(cp, data[pos+8:pos+8+l])
			m.chunks = append(m.chunks, riffChunk{id, cp})
		}
		pos += 8 + l
		if l%2 == 1 {
			pos++
		}
	}
	if m.tag == nil {
		m.tag = newID3Tag(3)
	}
	return m, nil
}

func (m *riffFile) apply(e Edits) error {
	pics := m.tag.pictures()
	pics = applyPictureEdits(pics, e)
	if e.PictureAction != "" || e.RemoveAllPictures {
		m.tag.setPictures(pics)
	}
	for k, v := range e.Set {
		if k != "" {
			m.tag.set(k, v)
		}
	}
	for _, k := range e.Remove {
		m.tag.delete(k)
	}
	var out bytes.Buffer
	out.WriteString(m.format)
	out.Write([]byte{0, 0, 0, 0})
	out.WriteString(m.kind)
	for _, c := range m.chunks {
		hdr := make([]byte, 8)
		copy(hdr, c.ID)
		putLE32(hdr[4:8], uint32(len(c.Data)))
		out.Write(hdr)
		out.Write(c.Data)
		if len(c.Data)%2 == 1 {
			out.WriteByte(0)
		}
	}
	if len(m.tag.Frames) > 0 {
		tb := m.tag.encode()
		hdr := make([]byte, 8)
		copy(hdr, "id3 ")
		putLE32(hdr[4:8], uint32(len(tb)))
		out.Write(hdr)
		out.Write(tb)
		if len(tb)%2 == 1 {
			out.WriteByte(0)
		}
	}
	res := out.Bytes()
	putLE32(res[4:8], uint32(len(res)-8))
	return writeFileAtomic(m.path, res)
}

// ---------------------------------------------------------------- AIFF

func readAIFF(f *os.File, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "AIFF", Info: Info{Lossless: true, Codec: "PCM"}}
	pos := 12
	var ssnd int64
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		l := int(be32(data[pos+4 : pos+8]))
		if l < 0 || pos+8+l > len(data) {
			break
		}
		body := data[pos+8 : pos+8+l]
		switch id {
		case "COMM":
			if len(body) >= 18 {
				t.Info.Channels = int(be16(body[0:2]))
				frames := be32(body[2:6])
				t.Info.BitDepth = int(be16(body[6:8]))
				rate := exteneded80(body[8:18])
				t.Info.SampleRate = int(rate)
				if rate > 0 {
					t.Info.Duration = float64(frames) / rate
				}
			}
		case "SSND":
			ssnd = int64(l)
		case "ID3 ", "id3 ":
			if tag, _ := readID3Tag(body); tag != nil {
				t.Fields = append(t.Fields, tag.fields()...)
				t.Pictures = append(t.Pictures, tag.pictures()...)
				t.TagType = "ID3v2." + itoa(maxInt(tag.Version, 3))
			}
		}
		pos += 8 + l
		if l%2 == 1 {
			pos++
		}
	}
	if t.Info.SampleRate > 0 && t.Info.Channels > 0 && t.Info.BitDepth > 0 {
		bps := t.Info.SampleRate * t.Info.Channels * t.Info.BitDepth
		t.Info.Bitrate = bps / 1000
		if t.Info.Duration == 0 && ssnd > 0 {
			t.Info.Duration = float64(ssnd) / float64(bps/8)
		}
	}
	return t, nil
}

func openAIFF(path string) (*riffFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:4]) != "FORM" {
		return nil, ErrUnsupported
	}
	m := &riffFile{path: path, format: "FORM", kind: string(data[8:12])}
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		l := int(be32(data[pos+4 : pos+8]))
		if l < 0 || pos+8+l > len(data) {
			break
		}
		if id == "ID3 " || id == "id3 " {
			if tag, _ := readID3Tag(data[pos+8 : pos+8+l]); tag != nil {
				m.tag = tag
			}
		} else {
			cp := make([]byte, l)
			copy(cp, data[pos+8:pos+8+l])
			m.chunks = append(m.chunks, riffChunk{id, cp})
		}
		pos += 8 + l
		if l%2 == 1 {
			pos++
		}
	}
	if m.tag == nil {
		m.tag = newID3Tag(3)
	}
	return m, nil
}

func (m *riffFile) applyAIFF(e Edits) error { return m.apply(e) }

func exteneded80(b []byte) float64 {
	if len(b) < 10 {
		return 0
	}
	exp := int(be16(b[0:2]))
	exp -= 16383
	mant := be64(b[2:10])
	return float64(mant) * pow2(exp-63)
}

func pow2(e int) float64 {
	v := 1.0
	if e >= 0 {
		for i := 0; i < e; i++ {
			v *= 2
		}
	} else {
		for i := 0; i < -e; i++ {
			v /= 2
		}
	}
	return v
}

var _ = strconv.Itoa
