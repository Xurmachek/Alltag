package audiometa

import (
	"io"
	"os"
)

type readerAtSize interface {
	ReadAt(p []byte, off int64) (int, error)
}

func readAll(f readerAtSize, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, ErrUnsupported
	}
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return buf[:n], nil
	}
	return buf[:n], nil
}

// ---------------------------------------------------------------- Monkey's Audio

func readAPEFormat(f readerAtSize, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "APE"}
	t.Info.Lossless = true
	t.Info.Codec = "Monkey's Audio"
	readMACProperties(data, &t.Info, size)
	// tag: APE tag at the very end (possibly in front of an ID3v1 tag)
	end := len(data)
	if end >= 128 && string(data[end-128:end-125]) == "TAG" {
		if v1 := parseID3v1(data[end-128:]); v1 != nil {
			t.Fields = append(t.Fields, v1.fields()...)
			t.TagType = "ID3v1"
			end -= 128
		}
	}
	if at, _ := findAPETag(data, end); at != nil && len(at.Items) > 0 {
		t.Fields = append(t.Fields, at.fields()...)
		t.Pictures = append(t.Pictures, at.pictures()...)
		t.TagType = "APEv2"
	}
	if t.TagType == "" && len(data) > 3 && string(data[:3]) == "ID3" {
		if tag, _ := readID3Tag(data); tag != nil {
			t.Fields = append(t.Fields, tag.fields()...)
			t.Pictures = append(t.Pictures, tag.pictures()...)
			t.TagType = "ID3v2." + itoa(maxInt(tag.Version, 3))
		}
	}
	return t, nil
}

// readMACProperties extracts sample rate / channels / duration from a
// Monkey's Audio stream header. Layouts differ between encoder generations, so
// the 3.98+ descriptor area is scanned for a plausible WAVEFORMATEX block.
func readMACProperties(data []byte, info *Info, size int64) {
	if len(data) < 16 || string(data[:4]) != "MAC " {
		return
	}
	version := int(le16(data[4:6]))
	if version >= 3980 {
		descBytes := int(le32(data[8:12]))
		end := 52 + descBytes
		if end > len(data) {
			end = len(data)
		}
		known := map[int]bool{8000: true, 11025: true, 12000: true, 16000: true,
			22050: true, 24000: true, 32000: true, 44100: true, 48000: true,
			64000: true, 88200: true, 96000: true, 192000: true}
		for off := 52; off+10 <= end; off += 2 {
			bps := int(le16(data[off : off+2]))
			ch := int(le16(data[off+2 : off+4]))
			sr := int(le32(data[off+4 : off+8]))
			if known[sr] && ch >= 1 && ch <= 8 && (bps == 8 || bps == 16 || bps == 24 || bps == 32) {
				info.SampleRate, info.Channels, info.BitDepth = sr, ch, bps
				break
			}
		}
	} else {
		// legacy header: channels @10, sample rate @12, frames @24, final @28
		ch := int(le16(data[10:12]))
		sr := int(le32(data[12:16]))
		if ch >= 1 && ch <= 8 && sr >= 4000 && sr <= 400000 {
			info.Channels = ch
			info.SampleRate = sr
			totalFrames := int64(le32(data[24:28]))
			final := int64(le32(data[28:32]))
			if sr > 0 && totalFrames > 0 {
				info.Duration = float64((totalFrames-1)*9216+final) / float64(sr)
			}
		}
	}
	if info.Duration > 0 && size > 0 {
		info.Bitrate = int(float64(size) * 8 / info.Duration / 1000)
	}
}

func (t *Tags) tagTypeOrNone() string {
	if t.TagType == "" {
		return "none"
	}
	return t.TagType
}

// ---------------------------------------------------------------- APE file writer

type apeFile struct {
	path    string
	data    []byte
	tag     *apeTag
	audioTo int
}

func openAPE(path string) (*apeFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &apeFile{path: path, data: data, audioTo: len(data)}
	if at, start := findAPETag(data, len(data)); at != nil {
		m.tag = at
		m.audioTo = start
	} else {
		m.tag = &apeTag{}
	}
	return m, nil
}

func (m *apeFile) apply(e Edits) error {
	pics := applyPictureEdits(m.tag.pictures(), e)
	if e.PictureAction != "" || e.RemoveAllPictures {
		m.tag.setPictures(pics)
	}
	for k, v := range e.Set {
		if k != "" {
			m.tag.set(k, v)
		}
	}
	for _, k := range e.Remove {
		m.tag.remove(k)
	}
	out := append([]byte{}, m.data[:m.audioTo]...)
	if len(m.tag.Items) > 0 {
		out = append(out, m.tag.encode()...)
	}
	return writeFileAtomic(m.path, out)
}
