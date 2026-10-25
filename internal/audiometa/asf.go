package audiometa

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"unicode/utf16"
)

// ASF / WMA metadata support.

type guid [16]byte

var (
	guidHeader        = guid{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}
	guidContentDesc   = guid{0x33, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}
	guidExtContent    = guid{0x40, 0xA4, 0xD0, 0xD2, 0x07, 0xE3, 0xD2, 0x11, 0x97, 0xF0, 0x00, 0xA0, 0xC9, 0x5E, 0xA8, 0x50}
	guidFileProps     = guid{0xA1, 0xDC, 0xAB, 0x8C, 0x47, 0xA9, 0xCF, 0x11, 0x8E, 0xE4, 0x00, 0xC0, 0x0C, 0x20, 0x53, 0x65}
	guidStreamProps   = guid{0x91, 0x07, 0xDC, 0xB7, 0xB7, 0xA9, 0xCF, 0x11, 0x8E, 0xE6, 0x00, 0xC0, 0x0C, 0x20, 0x53, 0x65}
	guidHeaderExt     = guid{0xB5, 0x03, 0xBF, 0x5F, 0x8E, 0xA1, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}
	guidMetadata      = guid{0xEA, 0xCB, 0xF8, 0xC5, 0xAF, 0x5B, 0x77, 0x48, 0x84, 0x67, 0xAA, 0x8C, 0x44, 0xFA, 0x4C, 0xCA}
	guidMetadataLib   = guid{0x94, 0x1C, 0x23, 0x44, 0x98, 0x94, 0xD1, 0x49, 0xA1, 0x41, 0x1D, 0x13, 0x4E, 0x45, 0x70, 0x54}
	guidReservedExt   = guid{0x11, 0xD2, 0xD3, 0xAB, 0xBA, 0xA9, 0xCF, 0x11, 0x8E, 0xE6, 0x00, 0xC0, 0x0C, 0x20, 0x53, 0x65}
	guidHeaderExtAlt  = guid{0xB5, 0x03, 0xBF, 0x5F, 0x2E, 0xA9, 0xCF, 0x11, 0x8E, 0xE3, 0x00, 0xC0, 0x0C, 0x20, 0x53, 0x65}
	asfVersionGUIDLit = guid{0x40, 0x9E, 0x69, 0xF8, 0x4D, 0x5B, 0xCF, 0x11, 0xA8, 0xFD, 0x00, 0x80, 0x5F, 0x5C, 0x44, 0x2B}
)

type asfObj struct {
	g   guid
	raw []byte // full object including header
}

type asfFile struct {
	path    string
	extGUID guid
	objs    []asfObj
	extObjs []asfObj // objects inside the header extension
	hasExt  bool
	tail    []byte
	info    Info
	fields  []Field
	pics    []Picture
	hdrLen  int
}

func (g guid) String() string {
	return string(g[:])
}

func parseASFObjects(b []byte) []asfObj {
	var out []asfObj
	pos := 0
	for pos+24 <= len(b) {
		var g guid
		copy(g[:], b[pos:pos+16])
		size := int(le64(b[pos+16 : pos+24]))
		if size < 24 || pos+size > len(b) {
			break
		}
		raw := make([]byte, size)
		copy(raw, b[pos:pos+size])
		out = append(out, asfObj{g: g, raw: raw})
		pos += size
	}
	return out
}

func readASF(f *os.File, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	m, err := parseASF(data)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: "WMA", TagType: "ASF", Info: m.info}
	t.Fields = m.fields
	t.Pictures = m.pics
	if len(m.fields) == 0 {
		t.TagType = "none"
	}
	return t, nil
}

func parseASF(data []byte) (*asfFile, error) {
	if len(data) < 30 || !bytes.Equal(data[:16], guidHeader[:]) {
		return nil, ErrUnsupported
	}
	m := &asfFile{}
	hdrSize := int(le64(data[16:24]))
	if hdrSize > len(data) {
		hdrSize = len(data)
	}
	m.hdrLen = hdrSize
	m.objs = parseASFObjects(data[30:hdrSize])
	m.tail = data[hdrSize:]
	m.info.Lossless = false
	m.info.Codec = "WMA"
	for _, o := range m.objs {
		switch o.g {
		case guidFileProps:
			d := o.raw[24:]
			if len(d) >= 48 {
				// fileSize(8) at 16, playDuration at 40, preroll at 56
				play := int64(le64(d[40:48]))
				preroll := int64(le64(d[56:64]))
				m.info.Duration = float64(play)/1e7 - float64(preroll)/1000
				if len(d) >= 80 {
					m.info.Bitrate = int(le32(d[76:80]) / 1000)
				}
			}
		case guidStreamProps:
			scanWaveFormat(&m.info, o.raw[24:])
		case guidContentDesc:
			m.parseContentDesc(o.raw[24:])
		case guidExtContent:
			m.parseExtContent(o.raw[24:])
		case guidHeaderExt, guidHeaderExtAlt:
			d := o.raw[24:]
			if len(d) >= 22 {
				m.extGUID = o.g
				m.extObjs = parseASFObjects(d[22:])
				m.hasExt = true
				for _, eo := range m.extObjs {
					if eo.g == guidMetadata || eo.g == guidMetadataLib {
						m.parseMetadata(eo.raw[24:])
					}
				}
			}
		}
	}
	return m, nil
}

func readUTF16(b []byte) string {
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	s := string(utf16.Decode(u))
	return strings.TrimRight(s, "\x00")
}

func encodeUTF16(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2+2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[i*2:], c)
	}
	return b
}

func (m *asfFile) parseContentDesc(d []byte) {
	if len(d) < 10 {
		return
	}
	lens := make([]int, 5)
	for i := 0; i < 5; i++ {
		lens[i] = int(le16(d[i*2 : i*2+2]))
	}
	p := 10
	keys := []string{"TITLE", "ARTIST", "COPYRIGHT", "COMMENT", ""}
	for i := 0; i < 5; i++ {
		if p+lens[i] > len(d) {
			break
		}
		if keys[i] != "" && lens[i] > 2 {
			v := readUTF16(d[p : p+lens[i]])
			if v != "" {
				m.fields = append(m.fields, Field{keys[i], v})
			}
		}
		p += lens[i]
	}
}

func (m *asfFile) parseExtContent(d []byte) {
	if len(d) < 2 {
		return
	}
	count := int(le16(d[:2]))
	p := 2
	for i := 0; i < count && p+2 <= len(d); i++ {
		nameLen := int(le16(d[p : p+2]))
		p += 2
		if p+nameLen > len(d) {
			break
		}
		name := readUTF16(d[p : p+nameLen])
		p += nameLen
		if p+4 > len(d) {
			break
		}
		vtype := int(le16(d[p : p+2]))
		vlen := int(le16(d[p+2 : p+4]))
		p += 4
		if p+vlen > len(d) {
			break
		}
		val := d[p : p+vlen]
		p += vlen
		m.addAttr(name, vtype, val)
	}
}

func (m *asfFile) parseMetadata(d []byte) {
	if len(d) < 2 {
		return
	}
	count := int(le16(d[:2]))
	p := 2
	for i := 0; i < count && p+12 <= len(d); i++ {
		nameLen := int(le16(d[p+4 : p+6]))
		vtype := int(le16(d[p+6 : p+8]))
		vlen := int(le32(d[p+8 : p+12]))
		p += 12
		if vlen < 0 || p+nameLen+vlen > len(d) {
			break
		}
		name := readUTF16(d[p : p+nameLen])
		p += nameLen
		val := d[p : p+vlen]
		p += vlen
		m.addAttr(name, vtype, val)
	}
}

func (m *asfFile) addAttr(name string, vtype int, val []byte) {
	switch strings.ToUpper(name) {
	case "WM/PICTURE":
		if len(val) >= 5 {
			kind := val[0]
			dl := int(le32(val[1:5]))
			p := 5
			mimeEnd := p
			for mimeEnd+1 < len(val) && !(val[mimeEnd] == 0 && val[mimeEnd+1] == 0) {
				mimeEnd += 2
			}
			mime := readUTF16(val[p:mimeEnd])
			p = mimeEnd + 2
			descEnd := p
			for descEnd+1 < len(val) && !(val[descEnd] == 0 && val[descEnd+1] == 0) {
				descEnd += 2
			}
			desc := readUTF16(val[p:descEnd])
			p = descEnd + 2
			if dl > 0 && p+dl <= len(val) {
				img := make([]byte, dl)
				copy(img, val[p:p+dl])
				if mime == "" {
					mime = mimeFromData(img)
				}
				m.pics = append(m.pics, Picture{MIME: mime, Kind: int(kind), Desc: desc, Data: img})
			}
		}
		return
	}
	var s string
	switch vtype {
	case 0:
		s = readUTF16(val)
	case 1:
		return
	case 2:
		if len(val) >= 4 {
			s = itoa(int(le32(val)))
		}
	case 3:
		if len(val) >= 4 {
			s = itoa(int(le32(val)))
		}
	case 4:
		if len(val) >= 8 {
			s = "1"
		}
	case 5:
		if len(val) >= 2 {
			s = itoa(int(le16(val)))
		}
	}
	if s == "" {
		return
	}
	key := CanonicalKey(name)
	if key == "" {
		return
	}
	for i := range m.fields {
		if m.fields[i].Key == key {
			return
		}
	}
	m.fields = append(m.fields, Field{key, s})
}

// ---------------------------------------------------------------- writing

func openASF(path string) (*asfFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := parseASF(data)
	if err != nil {
		return nil, err
	}
	m.path = path
	return m, nil
}

var asfWriteNames = map[string]string{
	"ALBUM": "WM/AlbumTitle", "YEAR": "WM/Year", "TRACK": "WM/TrackNumber",
	"GENRE": "WM/Genre", "ALBUMARTIST": "WM/AlbumArtist", "COMPOSER": "WM/Composer",
	"DISCNUMBER": "WM/PartOfSet", "BPM": "WM/BeatsPerMinute",
	"PUBLISHER": "WM/Publisher", "ENCODER": "WM/EncodedBy", "LYRICS": "WM/Lyrics",
	"SUBTITLE": "WM/SubTitle", "CONDUCTOR": "WM/Conductor",
	"ISRC": "WM/ISRC", "GROUPING": "WM/ContentGroupDescription",
}

func (m *asfFile) apply(e Edits) error {
	// Collect the desired attributes.
	attrs := map[string]string{}
	for _, f := range m.fields {
		if f.Key == "TITLE" || f.Key == "ARTIST" || f.Key == "COPYRIGHT" || f.Key == "COMMENT" {
			continue
		}
		attrs[f.Key] = f.Value
	}
	for k, v := range e.Set {
		key := CanonicalKey(k)
		if key == "" {
			continue
		}
		switch key {
		case "TITLE", "ARTIST", "COPYRIGHT", "COMMENT":
			continue // stored in the content description object
		}
		delete(attrs, key)
		if v != "" {
			attrs[key] = v
		}
	}
	for _, k := range e.Remove {
		delete(attrs, CanonicalKey(k))
	}
	cd := map[string]string{}
	for _, f := range m.fields {
		if f.Key == "TITLE" || f.Key == "ARTIST" || f.Key == "COPYRIGHT" || f.Key == "COMMENT" {
			cd[f.Key] = f.Value
		}
	}
	for k, v := range e.Set {
		key := CanonicalKey(k)
		switch key {
		case "TITLE", "ARTIST", "COPYRIGHT", "COMMENT":
			delete(cd, key)
			if v != "" {
				cd[key] = v
			}
		}
	}
	for _, k := range e.Remove {
		delete(cd, CanonicalKey(k))
	}

	pics := applyPictureEdits(m.pics, e)

	// Rebuild the object list, replacing CD / ECD / metadata library objects.
	var newObjs []asfObj
	doneCD, doneECD := false, false
	for _, o := range m.objs {
		switch o.g {
		case guidContentDesc:
			newObjs = append(newObjs, asfObj{guidContentDesc, buildContentDesc(cd)})
			doneCD = true
		case guidExtContent:
			newObjs = append(newObjs, asfObj{guidExtContent, buildExtContentDesc(attrs, pics)})
			doneECD = true
		case guidHeaderExt, guidHeaderExtAlt:
			newObjs = append(newObjs, asfObj{o.g, m.buildHeaderExt(pics)})
		default:
			newObjs = append(newObjs, o)
		}
	}
	if !doneCD {
		newObjs = append([]asfObj{{guidContentDesc, buildContentDesc(cd)}}, newObjs...)
	}
	if !doneECD {
		// insert after the content description object
		var out []asfObj
		inserted := false
		for _, o := range newObjs {
			out = append(out, o)
			if !inserted && o.g == guidContentDesc {
				out = append(out, asfObj{guidExtContent, buildExtContentDesc(attrs, pics)})
				inserted = true
			}
		}
		if !inserted {
			out = append(out, asfObj{guidExtContent, buildExtContentDesc(attrs, pics)})
		}
		newObjs = out
	}
	// update file properties (file size)
	total := 0
	for _, o := range newObjs {
		total += len(o.raw)
	}
	finalSize := 30 + total + len(m.tail)
	var buf bytes.Buffer
	head := make([]byte, 30)
	copy(head, guidHeader[:])
	putLE64(head[16:24], uint64(30+total))
	putLE32(head[24:28], uint32(len(newObjs)))
	head[28] = 1
	head[29] = 2
	buf.Write(head)
	for _, o := range newObjs {
		raw := o.raw
		if o.g == guidFileProps && len(raw) >= 24+32 {
			putLE64(raw[24+16:24+24], uint64(finalSize))
		}
		buf.Write(raw)
	}
	buf.Write(m.tail)
	return writeFileAtomic(m.path, buf.Bytes())
}

func buildContentDesc(cd map[string]string) []byte {
	keys := []string{"TITLE", "ARTIST", "COPYRIGHT", "COMMENT", ""}
	var body bytes.Buffer
	var strs [][]byte
	lens := make([]byte, 10)
	for i, k := range keys {
		var v []byte
		if k != "" {
			if s := cd[k]; s != "" {
				v = encodeUTF16(s)
			}
		}
		strs = append(strs, v)
		if v != nil {
			binary.LittleEndian.PutUint16(lens[i*2:], uint16(len(v)))
		}
	}
	body.Write(lens)
	for _, v := range strs {
		body.Write(v)
	}
	return asfWrap(guidContentDesc, body.Bytes())
}

func buildExtContentDesc(attrs map[string]string, pics []Picture) []byte {
	var body bytes.Buffer
	count := 0
	for _, v := range attrs {
		if v != "" {
			count++
		}
	}
	cnt := make([]byte, 2)
	binary.LittleEndian.PutUint16(cnt, uint16(count))
	body.Write(cnt)
	for k, v := range attrs {
		if v == "" {
			continue
		}
		name := asfWriteNames[k]
		if name == "" {
			name = k
		}
		nb := encodeUTF16(name)
		l := make([]byte, 2)
		binary.LittleEndian.PutUint16(l, uint16(len(nb)))
		body.Write(l)
		body.Write(nb)
		vb := encodeUTF16(v)
		vt := make([]byte, 4)
		binary.LittleEndian.PutUint16(vt[0:2], 0) // unicode string
		binary.LittleEndian.PutUint16(vt[2:4], uint16(len(vb)))
		body.Write(vt)
		body.Write(vb)
	}
	return asfWrap(guidExtContent, body.Bytes())
}

func asfWrap(g guid, body []byte) []byte {
	out := make([]byte, 24, 24+len(body))
	copy(out, g[:])
	putLE64(out[16:24], uint64(24+len(body)))
	return append(out, body...)
}

// buildHeaderExt keeps the original extension objects and refreshes the
// metadata library that carries embedded pictures.
func (m *asfFile) buildHeaderExt(pics []Picture) []byte {
	var exts []asfObj
	for _, o := range m.extObjs {
		if o.g == guidMetadataLib || o.g == guidMetadata || o.g == guidReservedExt {
			continue
		}
		exts = append(exts, o)
	}
	if len(pics) > 0 {
		exts = append(exts, asfObj{guidMetadataLib, buildMetadataLib(pics)})
	}
	var inner bytes.Buffer
	for _, o := range exts {
		inner.Write(o.raw)
	}
	var body bytes.Buffer
	body.Write(guidReservedExt[:])
	body.Write([]byte{1, 0})
	sz := make([]byte, 4)
	putLE32(sz, uint32(inner.Len()))
	body.Write(sz)
	body.Write(inner.Bytes())
	g := m.extGUID
	if g == (guid{}) {
		g = guidHeaderExt
	}
	return asfWrap(g, body.Bytes())
}

func buildMetadataLib(pics []Picture) []byte {
	var body bytes.Buffer
	cnt := make([]byte, 2)
	binary.LittleEndian.PutUint16(cnt, uint16(len(pics)))
	body.Write(cnt)
	for _, p := range pics {
		name := encodeUTF16("WM/Picture")
		var val bytes.Buffer
		kind := byte(p.Kind)
		if kind == 0 {
			kind = 3
		}
		val.WriteByte(kind)
		dl := make([]byte, 4)
		putLE32(dl, uint32(len(p.Data)))
		val.Write(dl)
		val.Write(encodeUTF16(p.MIME))
		val.Write(encodeUTF16(p.Desc))
		val.Write(p.Data)
		hdr := make([]byte, 12)
		binary.LittleEndian.PutUint16(hdr[0:2], 0) // language index
		binary.LittleEndian.PutUint16(hdr[2:4], 0) // stream number
		binary.LittleEndian.PutUint16(hdr[4:6], uint16(len(name)))
		binary.LittleEndian.PutUint16(hdr[6:8], 1) // type: bytes
		putLE32(hdr[8:12], uint32(val.Len()))
		body.Write(hdr)
		body.Write(name)
		body.Write(val.Bytes())
	}
	return asfWrap(guidMetadataLib, body.Bytes())
}

// scanWaveFormat locates a WAVEFORMATEX structure inside a stream properties
// object. Real-world WMA files sometimes prepend extra fields, so the scan is
// tolerant about the exact offset.
func scanWaveFormat(info *Info, d []byte) {
	known := map[int]bool{8000: true, 11025: true, 12000: true, 16000: true,
		22050: true, 24000: true, 32000: true, 44100: true, 48000: true,
		64000: true, 88200: true, 96000: true, 176400: true, 192000: true}
	for off := 0; off+16 <= len(d) && off < 90; off += 2 {
		ch := int(le16(d[off+2 : off+4]))
		sr := int(le32(d[off+4 : off+8]))
		if ch >= 1 && ch <= 8 && known[sr] {
			info.Channels = ch
			info.SampleRate = sr
			info.BitDepth = int(le16(d[off+14 : off+16]))
			return
		}
	}
}
