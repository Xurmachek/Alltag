package audiometa

import (
	"bytes"
	"fmt"
	"os"
)

const (
	flacStreamInfo = 0
	flacPadding    = 1
	flacApp        = 2
	flacSeekTable  = 3
	flacVorbis     = 4
	flacCueSheet   = 5
	flacPicture    = 6
)

type flacBlock struct {
	Type byte
	Data []byte
}

type flacFile struct {
	path       string
	blocks     []flacBlock
	audioStart int64
	audio      []byte
	vc         *vorbisComment
	info       Info
}

func readFLAC(f *os.File, size int64) (*Tags, error) {
	data := make([]byte, size)
	if _, err := f.ReadAt(data, 0); err != nil && size > 0 {
		// partial reads are tolerated below
	}
	t := &Tags{Format: "FLAC", TagType: "Vorbis"}
	if len(data) < 8 {
		return nil, ErrUnsupported
	}
	pos := 4
	var vc *vorbisComment
	for pos+4 <= len(data) {
		h := data[pos]
		last := h&0x80 != 0
		btype := h & 0x7F
		length := be24(data[pos+1 : pos+4])
		pos += 4
		if length < 0 || pos+length > len(data) {
			break
		}
		block := data[pos : pos+length]
		pos += length
		switch btype {
		case flacStreamInfo:
			applyStreamInfo(t, block)
		case flacVorbis:
			if c, err := parseVorbisComment(block); err == nil {
				vc = c
			}
		case flacPicture:
			if p, err := parseFLACPicture(block); err == nil {
				t.Pictures = append(t.Pictures, p)
			}
		}
		if last {
			break
		}
	}
	if vc != nil {
		t.Fields = append(t.Fields, vc.Fields...)
		t.Pictures = append(t.Pictures, vc.Pics...)
	}
	if t.Info.Duration > 0 {
		t.Info.Bitrate = int(float64(size-int64(pos)) * 8 / t.Info.Duration / 1000)
	}
	t.Info.Lossless = true
	if t.Info.Codec == "" {
		t.Info.Codec = "FLAC"
	}
	if vc == nil {
		t.TagType = "none"
	}
	return t, nil
}

func applyStreamInfo(t *Tags, b []byte) {
	if len(b) < 18 {
		return
	}
	packed := be64(b[10:18])
	sampleRate := int(packed >> 44)
	channels := int((packed>>41)&0x7) + 1
	bps := int((packed>>36)&0x1F) + 1
	totalSamples := int64(packed & 0xFFFFFFFFF)
	t.Info.SampleRate = sampleRate
	t.Info.Channels = channels
	t.Info.BitDepth = bps
	if sampleRate > 0 {
		t.Info.Duration = float64(totalSamples) / float64(sampleRate)
	}
}

func openFLAC(path string) (*flacFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		return nil, ErrUnsupported
	}
	m := &flacFile{path: path}
	pos := 4
	for pos+4 <= len(data) {
		h := data[pos]
		last := h&0x80 != 0
		btype := h & 0x7F
		length := be24(data[pos+1 : pos+4])
		pos += 4
		if length < 0 || pos+length > len(data) {
			return nil, fmt.Errorf("flac: corrupt metadata block")
		}
		blk := data[pos : pos+length]
		pos += length
		switch btype {
		case flacVorbis:
			if c, err := parseVorbisComment(blk); err == nil {
				m.vc = c
			}
		case flacPicture:
			// kept for potential re-emission
		default:
			cp := make([]byte, len(blk))
			copy(cp, blk)
			m.blocks = append(m.blocks, flacBlock{btype, cp})
		}
		if last {
			break
		}
	}
	if m.vc == nil {
		m.vc = &vorbisComment{}
	}
	// re-read pictures (they are dropped from blocks above)
	pics := readFLACPictures(data)
	m.vc.Pics = pics
	m.audioStart = int64(pos)
	m.audio = data[pos:]
	applyStreamInfo(&Tags{}, nil)
	return m, nil
}

func readFLACPictures(data []byte) []Picture {
	var out []Picture
	pos := 4
	for pos+4 <= len(data) {
		h := data[pos]
		last := h&0x80 != 0
		btype := h & 0x7F
		length := be24(data[pos+1 : pos+4])
		pos += 4
		if length < 0 || pos+length > len(data) {
			break
		}
		if btype == flacPicture {
			if p, err := parseFLACPicture(data[pos : pos+length]); err == nil {
				out = append(out, p)
			}
		}
		pos += length
		if last {
			break
		}
	}
	return out
}

func (m *flacFile) apply(e Edits) error {
	pics := applyPictureEdits(m.vc.Pics, e)
	for k, v := range e.Set {
		if k == "" {
			continue
		}
		m.vc.set(k, v)
	}
	for _, k := range e.Remove {
		m.vc.remove(k)
	}
	return m.write(pics)
}

func (m *flacFile) write(pics []Picture) error {
	var out bytes.Buffer
	out.WriteString("fLaC")
	var blocks []flacBlock
	insertedVC := false
	for _, b := range m.blocks {
		if b.Type == flacPadding {
			continue
		}
		blocks = append(blocks, b)
		if b.Type == flacStreamInfo && !insertedVC {
			blocks = append(blocks, flacBlock{Type: flacVorbis, Data: m.vc.encode(nil)})
			insertedVC = true
		}
	}
	if !insertedVC {
		blocks = append(blocks, flacBlock{Type: flacVorbis, Data: m.vc.encode(nil)})
	}
	// pictures after the comment block
	var withPics []flacBlock
	for _, b := range blocks {
		withPics = append(withPics, b)
		if b.Type == flacVorbis {
			for _, p := range pics {
				withPics = append(withPics, flacBlock{Type: flacPicture, Data: encodeFLACPicture(p)})
			}
		}
	}
	// trailing padding keeps room for future edits without rewriting the file
	withPics = append(withPics, flacBlock{Type: flacPadding, Data: make([]byte, 4096)})

	for i, b := range withPics {
		h := []byte{b.Type, 0, 0, 0}
		if i == len(withPics)-1 {
			h[0] |= 0x80
		}
		l := len(b.Data)
		h[1] = byte(l >> 16)
		h[2] = byte(l >> 8)
		h[3] = byte(l)
		out.Write(h)
		out.Write(b.Data)
	}
	out.Write(m.audio)
	return writeFileAtomic(m.path, out.Bytes())
}
