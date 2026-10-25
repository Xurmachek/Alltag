package audiometa

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// vorbisComment holds a Vorbis Comment block (used by FLAC, Ogg Vorbis, Opus
// and Speex).
type vorbisComment struct {
	Vendor string
	Fields []Field
	Pics   []Picture
}

func parseVorbisComment(b []byte) (*vorbisComment, error) {
	if len(b) < 8 {
		return nil, ErrNoTag
	}
	v := &vorbisComment{}
	vlen := int(le32(b[0:4]))
	if vlen < 0 || 4+vlen > len(b) {
		return nil, ErrNoTag
	}
	v.Vendor = string(b[4 : 4+vlen])
	p := 4 + vlen
	if p+4 > len(b) {
		return v, nil
	}
	count := int(le32(b[p : p+4]))
	p += 4
	for i := 0; i < count && p+4 <= len(b); i++ {
		l := int(le32(b[p : p+4]))
		p += 4
		if l < 0 || p+l > len(b) {
			break
		}
		entry := string(b[p : p+l])
		p += l
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(entry[:eq]))
		val := entry[eq+1:]
		if key == "METADATA_BLOCK_PICTURE" {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val)); err == nil {
				if pic, err := parseFLACPicture(raw); err == nil {
					v.Pics = append(v.Pics, pic)
				}
			}
			continue
		}
		v.Fields = append(v.Fields, Field{CanonicalKey(key), val})
	}
	return v, nil
}

// vorbisKey maps a canonical key back to the conventional Vorbis field name.
func vorbisKey(key string) string {
	switch CanonicalKey(key) {
	case "YEAR":
		return "DATE"
	case "TRACK":
		return "TRACKNUMBER"
	case "DISCNUMBER":
		return "DISCNUMBER"
	case "COMMENT":
		return "COMMENT"
	case "LYRICS":
		return "UNSYNCEDLYRICS"
	}
	return strings.ToUpper(CanonicalKey(key))
}

// encode serialises the comment block, appending base64 METADATA_BLOCK_PICTURE
// entries for the given pictures.
func (v *vorbisComment) encode(pics []Picture) []byte {
	var buf bytes.Buffer
	vendor := v.Vendor
	if vendor == "" {
		vendor = "AllTag 1.0"
	}
	hdr := make([]byte, 4)
	putLE32(hdr, uint32(len(vendor)))
	buf.Write(hdr)
	buf.WriteString(vendor)

	var entries [][]byte
	for _, f := range v.Fields {
		if f.Key == "" || f.Value == "" {
			continue
		}
		line := vorbisKey(f.Key) + "=" + f.Value
		entries = append(entries, []byte(line))
	}
	for _, p := range pics {
		if len(p.Data) == 0 {
			continue
		}
		raw := encodeFLACPicture(p)
		line := "METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(raw)
		entries = append(entries, []byte(line))
	}
	cnt := make([]byte, 4)
	putLE32(cnt, uint32(len(entries)))
	buf.Write(cnt)
	for _, e := range entries {
		l := make([]byte, 4)
		putLE32(l, uint32(len(e)))
		buf.Write(l)
		buf.Write(e)
	}
	return buf.Bytes()
}

func (v *vorbisComment) set(key, value string) {
	key = CanonicalKey(key)
	if value == "" {
		v.remove(key)
		return
	}
	for i := range v.Fields {
		if v.Fields[i].Key == key {
			v.Fields[i].Value = value
			return
		}
	}
	v.Fields = append(v.Fields, Field{key, value})
}

func (v *vorbisComment) remove(key string) {
	key = CanonicalKey(key)
	out := v.Fields[:0]
	for _, f := range v.Fields {
		if f.Key != key {
			out = append(out, f)
		}
	}
	v.Fields = out
}

// ------------------------------------------------------------------ pictures

func parseFLACPicture(b []byte) (Picture, error) {
	var p Picture
	if len(b) < 32 {
		return p, ErrNoTag
	}
	p.Kind = int(be32(b[0:4]))
	mimeLen := int(be32(b[4:8]))
	pos := 8
	if pos+mimeLen > len(b) {
		return p, ErrNoTag
	}
	p.MIME = string(b[pos : pos+mimeLen])
	pos += mimeLen
	if pos+4 > len(b) {
		return p, ErrNoTag
	}
	descLen := int(be32(b[pos : pos+4]))
	pos += 4
	if pos+descLen > len(b) {
		return p, ErrNoTag
	}
	p.Desc = string(b[pos : pos+descLen])
	pos += descLen
	pos += 16 // width, height, depth, colors
	if pos+4 > len(b) {
		return p, ErrNoTag
	}
	dataLen := int(be32(b[pos : pos+4]))
	pos += 4
	if dataLen < 0 || pos+dataLen > len(b) {
		return p, ErrNoTag
	}
	p.Data = make([]byte, dataLen)
	copy(p.Data, b[pos:pos+dataLen])
	if p.MIME == "" {
		p.MIME = mimeFromData(p.Data)
	}
	return p, nil
}

func encodeFLACPicture(p Picture) []byte {
	var buf bytes.Buffer
	head := make([]byte, 4)
	putBE32(head, uint32(p.Kind))
	buf.Write(head)
	if p.MIME == "" {
		p.MIME = mimeFromData(p.Data)
	}
	putBE32(head, uint32(len(p.MIME)))
	buf.Write(head)
	buf.WriteString(p.MIME)
	putBE32(head, uint32(len(p.Desc)))
	buf.Write(head)
	buf.WriteString(p.Desc)
	zeros := make([]byte, 16)
	buf.Write(zeros) // width / height / depth / colours (optional)
	putBE32(head, uint32(len(p.Data)))
	buf.Write(head)
	buf.Write(p.Data)
	return buf.Bytes()
}

// flacPicturesToFields is a tiny helper for the extended tag editor.
func flacPicturesToFields(pics []Picture) []Field {
	var out []Field
	for i, p := range pics {
		out = append(out, Field{Key: "PICTURE", Value: strings.Join([]string{
			intToString(i), p.MIME, intToString(len(p.Data)),
		}, " | ")})
	}
	return out
}

func intToString(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

var _ = binary.LittleEndian
