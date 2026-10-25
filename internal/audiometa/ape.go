package audiometa

import (
	"bytes"
	"strings"
)

// APEv2 tag (Monkey's Audio, WavPack, Musepack and optionally MP3).

type apeItem struct {
	Key    string
	Value  []byte
	Binary bool
}

type apeTag struct {
	Version   int
	Items     []apeItem
	totalSize int // bytes occupied in the file including header/footer
	Start     int // offset of the tag inside the file buffer
	HasHeader bool
}

const apeMagic = "APETAGEX"

func findAPETag(data []byte, end int) (*apeTag, int) {
	if end < 32 || end > len(data) {
		return nil, 0
	}
	// footer is the last 32 bytes before end
	f := data[end-32 : end]
	if string(f[:8]) != apeMagic {
		return nil, 0
	}
	version := int(le32(f[8:12]))
	size := int(le32(f[12:16]))
	count := int(le32(f[16:20]))
	flags := le32(f[20:24])
	if size < 32 || size > end || size > 16<<20 {
		return nil, 0
	}
	start := end - size
	t := &apeTag{Version: version, totalSize: size, Start: start}
	t.HasHeader = flags&0x80000000 != 0
	if t.HasHeader && start >= 32 && string(data[start-32:start-24]) == apeMagic {
		t.Start = start - 32
		t.totalSize += 32
	} else {
		t.HasHeader = false
	}
	pos := start
	for i := 0; i < count && pos+8 <= end-32; i++ {
		sz := int(le32(data[pos : pos+4]))
		itFlags := le32(data[pos+4 : pos+8])
		pos += 8
		z := bytes.IndexByte(data[pos:end-32], 0)
		if z < 0 {
			break
		}
		key := string(data[pos : pos+z])
		pos += z + 1
		if sz < 0 || pos+sz > end-32 {
			break
		}
		val := make([]byte, sz)
		copy(val, data[pos:pos+sz])
		pos += sz
		t.Items = append(t.Items, apeItem{Key: key, Value: val, Binary: itFlags&2 != 0})
	}
	return t, t.Start
}

// parseAPEHeader is retained for callers that already located the magic.
func parseAPEHeader(b []byte) (*apeTag, error) {
	if len(b) < 32 || string(b[:8]) != apeMagic {
		return nil, ErrNoTag
	}
	size := int(le32(b[12:16]))
	count := int(le32(b[16:20]))
	t := &apeTag{Version: int(le32(b[8:12])), totalSize: size}
	pos := 32
	for i := 0; i < count && pos+8 <= len(b); i++ {
		sz := int(le32(b[pos : pos+4]))
		pos += 8
		z := bytes.IndexByte(b[pos:], 0)
		if z < 0 {
			break
		}
		key := string(b[pos : pos+z])
		pos += z + 1
		if sz < 0 || pos+sz > len(b) {
			break
		}
		val := make([]byte, sz)
		copy(val, b[pos:pos+sz])
		pos += sz
		t.Items = append(t.Items, apeItem{Key: key, Value: val})
	}
	return t, nil
}

// plausible rejects false positives (the magic can appear inside other tags).
func (t *apeTag) plausible() bool {
	if t.Version < 1000 || t.Version > 4000 {
		return false
	}
	if t.totalSize < 32 || t.totalSize > 16<<20 {
		return false
	}
	if len(t.Items) > 10000 {
		return false
	}
	return len(t.Items) > 0
}

func (t *apeTag) fields() []Field {
	var out []Field
	for _, it := range t.Items {
		key := strings.ToUpper(it.Key)
		if key == "COVER ART (FRONT)" || strings.HasPrefix(key, "COVER ART") {
			continue
		}
		if it.Binary {
			continue
		}
		out = append(out, Field{CanonicalKey(key), strings.TrimRight(string(it.Value), "\x00")})
	}
	return out
}

func (t *apeTag) pictures() []Picture {
	var out []Picture
	for _, it := range t.Items {
		key := strings.ToUpper(it.Key)
		if key != "COVER ART (FRONT)" && key != "COVER ART (BACK)" {
			continue
		}
		v := it.Value
		z := bytes.IndexByte(v, 0)
		var data []byte
		if z >= 0 && z < len(v) {
			data = v[z+1:]
		} else {
			data = v
		}
		if len(data) == 0 {
			continue
		}
		kind := 3
		if key == "COVER ART (BACK)" {
			kind = 4
		}
		out = append(out, Picture{MIME: mimeFromData(data), Kind: kind, Data: append([]byte{}, data...)})
	}
	return out
}

func (t *apeTag) set(key, value string) {
	key = CanonicalKey(key)
	if value == "" {
		t.remove(key)
		return
	}
	for i := range t.Items {
		if CanonicalKey(t.Items[i].Key) == key && !t.Items[i].Binary {
			t.Items[i].Value = []byte(value)
			return
		}
	}
	t.Items = append(t.Items, apeItem{Key: strings.ToUpper(key), Value: []byte(value)})
}

func (t *apeTag) remove(key string) {
	key = CanonicalKey(key)
	out := t.Items[:0]
	for _, it := range t.Items {
		if CanonicalKey(it.Key) != key || it.Binary {
			out = append(out, it)
		}
	}
	t.Items = out
}

func (t *apeTag) setPictures(pics []Picture) {
	out := t.Items[:0]
	for _, it := range t.Items {
		k := strings.ToUpper(it.Key)
		if strings.HasPrefix(k, "COVER ART") {
			continue
		}
		out = append(out, it)
	}
	t.Items = out
	for _, p := range pics {
		name := []byte("cover.jpg\x00")
		if strings.Contains(p.MIME, "png") {
			name = []byte("cover.png\x00")
		}
		val := append(append([]byte{}, name...), p.Data...)
		t.Items = append(t.Items, apeItem{Key: "Cover Art (Front)", Value: val, Binary: true})
	}
}

// encode builds a complete APEv2 tag with header and footer.
func (t *apeTag) encode() []byte {
	var items bytes.Buffer
	count := 0
	for _, it := range t.Items {
		if it.Key == "" {
			continue
		}
		hdr := make([]byte, 8)
		putLE32(hdr[0:4], uint32(len(it.Value)))
		flags := uint32(0) // bit1: 0 = UTF-8 text
		if it.Binary {
			flags |= 2 // binary payload
		}
		putLE32(hdr[4:8], flags)
		items.Write(hdr)
		items.WriteString(it.Key)
		items.WriteByte(0)
		items.Write(it.Value)
		count++
	}
	size := items.Len() + 32 // footer + items, excluding the header
	mk := func(isHeader bool) []byte {
		b := make([]byte, 32)
		copy(b, apeMagic)
		putLE32(b[8:12], 2000)
		putLE32(b[12:16], uint32(size))
		putLE32(b[16:20], uint32(count))
		flags := uint32(0x80000000) // tag contains header
		if isHeader {
			flags |= 0x20000000 // this is the header, not the footer
		}
		putLE32(b[20:24], flags)
		return b
	}
	var out bytes.Buffer
	out.Write(mk(true))
	out.Write(items.Bytes())
	out.Write(mk(false))
	return out.Bytes()
}

// ---------------------------------------------------------------- WavPack

func readWV(f readerAtSize, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "WV"}
	if len(data) < 32 || string(data[:4]) != "wvpk" {
		return nil, ErrUnsupported
	}
	version := int(le16(data[8:10]))
	totalSamples := int64(le32(data[12:16]))
	indexNo := int(le32(data[20:24]))
	sampleRate := int(le32(data[24:28])/0x10000000) + 1
	sampleRate = wvSampleRates[(le32(data[24:28])>>23)&0x0F]
	if sampleRate == 0 {
		sampleRate = 44100
	}
	channels := 1
	if le32(data[24:28])&0x4 != 0 {
		channels = 2
	}
	bits := int((le32(data[24:28]) >> 20 & 0xF)) + 1
	t.Info.SampleRate = sampleRate
	t.Info.Channels = channels
	t.Info.BitDepth = bits
	t.Info.Lossless = true
	t.Info.Codec = "WavPack " + itoa(version)
	if sampleRate > 0 && totalSamples > 0 {
		t.Info.Duration = float64(totalSamples) / float64(sampleRate)
		t.Info.Bitrate = int(float64(size) * 8 / t.Info.Duration / 1000)
	}
	_ = indexNo
	if at, _ := findAPETag(data, len(data)); at != nil && len(at.Items) > 0 {
		t.Fields = append(t.Fields, at.fields()...)
		t.Pictures = append(t.Pictures, at.pictures()...)
		t.TagType = "APEv2"
	}
	return t, nil
}

var wvSampleRates = []int{6000, 8000, 9600, 11025, 12000, 16000, 22050, 24000,
	32000, 44100, 48000, 64000, 88200, 96000, 192000, 0}

// ---------------------------------------------------------------- Musepack

func readMPC(f readerAtSize, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "MPC"}
	t.Info.Lossless = false
	t.Info.Codec = "Musepack"
	if len(data) > 12 && string(data[:4]) == "MPCK" {
		t.Info.SampleRate = 44100
		t.Info.Codec = "Musepack SV8"
	} else if len(data) > 8 && string(data[:3]) == "MP+" {
		t.Info.Codec = "Musepack SV7"
		t.Info.SampleRate = 44100
	}
	if at, _ := findAPETag(data, len(data)); at != nil {
		t.Fields = append(t.Fields, at.fields()...)
		t.Pictures = append(t.Pictures, at.pictures()...)
		if len(at.Items) > 0 {
			t.TagType = "APEv2"
		}
	}
	return t, nil
}

func itoa(v int) string { return intToString(v) }
