package audiometa

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- MP4 atoms

type atom struct {
	typ      string
	data     []byte // leaf payload
	kids     []*atom
	full     bool // full box: 4 byte version/flags prefix before kids
	verflags uint32
	isItem   bool // ilst entry (container whose children are data/mean/name)
}

var atomContainers = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true,
	"udta": true, "edts": true, "dinf": true, "mvex": true, "moof": true,
	"traf": true, "ilst": true, "mfra": true, "tref": true, "wave": true,
}

func parseAtoms(b []byte, depth int) ([]*atom, error) {
	var out []*atom
	pos := 0
	for pos+8 <= len(b) {
		size := int(be32(b[pos : pos+4]))
		typ := string(b[pos+4 : pos+8])
		hdr := 8
		if size == 1 {
			if pos+16 > len(b) {
				break
			}
			size = int(be64(b[pos+8 : pos+16]))
			hdr = 16
		} else if size == 0 {
			size = len(b) - pos
		}
		if size < hdr || pos+size > len(b) {
			break
		}
		payload := b[pos+hdr : pos+size]
		a := &atom{typ: typ}
		switch {
		case typ == "meta":
			a.full = true
			if len(payload) >= 4 {
				a.verflags = be32(payload[:4])
				a.kids, _ = parseAtoms(payload[4:], depth+1)
			}
		case atomContainers[typ]:
			a.kids, _ = parseAtoms(payload, depth+1)
			if typ == "ilst" {
				for _, k := range a.kids {
					k.isItem = true
					k.kids, _ = parseAtoms(k.data, depth+1)
					k.data = nil
				}
			}
		default:
			a.data = payload
		}
		out = append(out, a)
		pos += size
	}
	return out, nil
}

func (a *atom) encode() []byte {
	var payload []byte
	if a.data != nil && len(a.kids) == 0 {
		payload = a.data
	} else {
		var buf bytes.Buffer
		if a.full {
			var v [4]byte
			putBE32(v[:], a.verflags)
			buf.Write(v[:])
		}
		for _, k := range a.kids {
			buf.Write(k.encode())
		}
		payload = buf.Bytes()
	}
	out := make([]byte, 8, 8+len(payload))
	putBE32(out[:4], uint32(8+len(payload)))
	copy(out[4:8], a.typ)
	return append(out, payload...)
}

func (a *atom) find(types ...string) *atom {
	var search func(n *atom, typ string) *atom
	search = func(n *atom, typ string) *atom {
		for _, k := range n.kids {
			if k.typ == typ {
				return k
			}
		}
		for _, k := range n.kids {
			if found := search(k, typ); found != nil {
				return found
			}
		}
		return nil
	}
	cur := a
	for _, typ := range types {
		next := search(cur, typ)
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func (a *atom) remove(typ string) {
	out := a.kids[:0]
	for _, k := range a.kids {
		if k.typ != typ {
			out = append(out, k)
		}
	}
	a.kids = out
}

// dataAtom builds a 'data' atom with the given well-known type.
func dataAtom(kind uint32, payload []byte) *atom {
	b := make([]byte, 8, 8+len(payload))
	b[3] = byte(kind)  // version 0 + 24 bit type
	putBE32(b[4:8], 0) // locale
	b = append(b, payload...)
	return &atom{typ: "data", data: b}
}

func (a *atom) dataPayload() []byte {
	// data atom: version/flags(4) + locale(4) + payload
	if len(a.data) < 8 {
		return nil
	}
	return a.data[8:]
}

func (a *atom) dataType() uint32 {
	if len(a.data) < 4 {
		return 0
	}
	return be32(a.data[:4]) & 0x00FFFFFF
}

// ---------------------------------------------------------------- key mapping

const cSym = "\xa9"

var mp4Keys = map[string]string{
	cSym + "nam": "TITLE", cSym + "ART": "ARTIST", cSym + "alb": "ALBUM",
	"aART": "ALBUMARTIST", cSym + "day": "YEAR", cSym + "gen": "GENRE",
	cSym + "cmt": "COMMENT", cSym + "wrt": "COMPOSER",
	"trkn": "TRACK", "disk": "DISCNUMBER", "tmpo": "BPM",
	cSym + "lyr": "LYRICS", cSym + "too": "ENCODER", "cprt": "COPYRIGHT",
	"cpil": "COMPILATION", "soaa": "ALBUMSORT", "soar": "ARTISTSORT",
	"sonm": "TITLESORT", "soal": "ALBUMSORT", cSym + "grp": "GROUPING",
	cSym + "con": "CONDUCTOR", "desc": "COMMENT", "ldes": "COMMENT",
	cSym + "st3": "SUBTITLE", "covr": "", // pictures only
}

var keyMP4 = func() map[string]string {
	m := map[string]string{}
	for k, v := range mp4Keys {
		if v == "" || k == "desc" || k == "ldes" {
			continue // read-only aliases
		}
		if _, exists := m[v]; exists {
			continue
		}
		m[v] = k
	}
	m["DATE"] = cSym + "day"
	m["TRACKNUMBER"] = "trkn"
	return m
}()

func mp4ItemKey(a *atom) string {
	if a.typ == "----" {
		name := ""
		for _, k := range a.kids {
			if k.typ == "name" && len(k.data) > 4 {
				name = string(k.data[4:])
			}
		}
		if name != "" {
			return CanonicalKey(name)
		}
		return ""
	}
	if a.typ == "gnre" {
		return "GENRE"
	}
	if k, ok := mp4Keys[a.typ]; ok {
		return k
	}
	return ""
}

func mp4DecodeValue(a *atom, payload []byte) string {
	switch a.typ {
	case "trkn":
		if len(payload) >= 4 {
			tr := be16(payload[2:4])
			tot := 0
			if len(payload) >= 6 {
				tot = be16(payload[4:6])
			}
			if tot > 0 && tr > 0 {
				return itoa(tr) + "/" + itoa(tot)
			}
			return itoa(tr)
		}
	case "disk":
		if len(payload) >= 4 {
			d := be16(payload[2:4])
			tot := 0
			if len(payload) >= 6 {
				tot = be16(payload[4:6])
			}
			if tot > 0 {
				return itoa(d) + "/" + itoa(tot)
			}
			return itoa(d)
		}
	case "tmpo":
		if len(payload) >= 2 {
			return itoa(int(be16(payload)))
		}
	case "cpil":
		if len(payload) >= 1 {
			if payload[0] != 0 {
				return "1"
			}
			return "0"
		}
	case "gnre":
		if len(payload) >= 2 {
			n := int(be16(payload)) - 1
			if n >= 0 && n < len(id3Genres) {
				return id3Genres[n]
			}
		}
	}
	return strings.TrimRight(string(payload), "\x00")
}

func mp4EncodeValue(key, value string) (typ uint32, payload []byte) {
	switch key {
	case "TRACK", "DISCNUMBER":
		num, tot := parsePair(value)
		b := make([]byte, 8)
		be16Put(b[2:4], num)
		be16Put(b[4:6], tot)
		return 0, b
	case "BPM":
		n, _ := strconv.Atoi(digitsOnly(value))
		b := make([]byte, 2)
		be16Put(b, n)
		return 21, b
	case "COMPILATION":
		if value == "1" || strings.EqualFold(value, "true") {
			return 21, []byte{1}
		}
		return 21, []byte{0}
	}
	return 1, []byte(value)
}

func parsePair(s string) (int, int) {
	parts := strings.SplitN(s, "/", 2)
	a, _ := strconv.Atoi(strings.TrimSpace(digitsOnly(parts[0])))
	b := 0
	if len(parts) > 1 {
		b, _ = strconv.Atoi(strings.TrimSpace(digitsOnly(parts[1])))
	}
	return a, b
}

func be16Put(b []byte, v int) {
	b[0], b[1] = byte(v>>8), byte(v)
}

// ---------------------------------------------------------------- read / write

type mp4File struct {
	path     string
	data     []byte
	roots    []*atom
	moov     *atom
	moovOff  int
	moovLen  int
	format   string
	TagType  string
	fields   []Field
	pictures []Picture
	info     Info
}

func readMP4(f *os.File, size int64) (*Tags, error) {
	data, err := readAll(f, size)
	if err != nil {
		return nil, err
	}
	m, err := parseMP4(data)
	if err != nil {
		return nil, err
	}
	t := &Tags{Format: m.format, TagType: m.TagType, Info: m.info}
	t.Fields = m.fields
	t.Pictures = m.pictures
	return t, nil
}

func parseMP4(data []byte) (*mp4File, error) {
	roots, err := parseAtoms(data, 0)
	if err != nil {
		return nil, err
	}
	m := &mp4File{data: data, roots: roots, format: "M4A"}
	m.TagType = "none"
	var moov *atom
	off := 0
	for _, a := range roots {
		sz := len(a.encode())
		if a.typ == "moov" {
			moov = a
			m.moovOff = off
			m.moovLen = sz
		}
		if a.typ == "ftyp" {
			if len(a.data) >= 4 {
				brand := string(a.data[:4])
				switch brand {
				case "M4A ", "M4B ", "mp42", "isom", "iso2", "qt  ":
					m.format = "M4A"
				}
			}
		}
		off += sz
	}
	if moov == nil {
		return nil, ErrUnsupported
	}
	m.moov = moov
	// duration
	if mvhd := moov.find("mvhd"); mvhd != nil && len(mvhd.data) >= 20 {
		if mvhd.data[0] == 1 && len(mvhd.data) >= 28 {
			ts := be32(mvhd.data[20:24])
			dur := be64(mvhd.data[24:32])
			if ts > 0 {
				m.info.Duration = float64(dur) / float64(ts)
			}
		} else {
			ts := be32(mvhd.data[12:16])
			dur := be32(mvhd.data[16:20])
			if ts > 0 {
				m.info.Duration = float64(dur) / float64(ts)
			}
		}
	}
	// codec / sample rate / channels from the sample description
	codec := "AAC"
	if stsd := moov.find("stbl", "stsd"); stsd != nil && len(stsd.data) >= 16 {
		four := string(stsd.data[12:16])
		switch four {
		case "alac":
			codec = "ALAC"
			m.info.Lossless = true
		case "mp4a":
			codec = "AAC"
		case "ac-3":
			codec = "AC-3"
		case "ec-3":
			codec = "E-AC-3"
		case "samr":
			codec = "AMR"
		}
		d := stsd.data
		if len(d) >= 36 {
			m.info.Channels = int(be16(d[32:34]))
			m.info.BitDepth = int(be16(d[34:36]))
		}
		if len(d) >= 44 {
			sr := int(be32(d[40:44]) >> 16)
			if sr >= 4000 && sr <= 400000 {
				m.info.SampleRate = sr
			}
		}
		// ALAC: the real format lives in the nested 'alac' atom
		if four == "alac" {
			if kids, err := parseAtoms(d[44:], 0); err == nil {
				for _, k := range kids {
					if k.typ == "alac" && len(k.data) >= 28 {
						bd := k.data
						m.info.BitDepth = int(bd[9])
						m.info.Channels = int(bd[13])
						m.info.SampleRate = int(be32(bd[24:28]))
					}
				}
			}
		}
	}
	m.info.Codec = codec
	if m.info.Duration > 0 {
		m.info.Bitrate = int(float64(len(data)) * 8 / m.info.Duration / 1000)
	}
	// tags
	ilst := moov.find("udta", "meta", "ilst")
	if ilst == nil {
		ilst = moov.find("meta", "ilst")
	}
	if ilst != nil {
		if len(ilst.kids) > 0 {
			m.TagType = "MP4"
		}
		for _, item := range ilst.kids {
			if item.typ == "covr" {
				for _, d := range item.kids {
					if d.typ != "data" {
						continue
					}
					img := append([]byte{}, d.dataPayload()...)
					if len(img) > 0 {
						mime := "image/jpeg"
						if d.dataType() == 14 {
							mime = "image/png"
						}
						m.pictures = append(m.pictures, Picture{MIME: mime, Kind: 3, Data: img})
					}
				}
				continue
			}
			key := mp4ItemKey(item)
			if key == "" {
				continue
			}
			for _, d := range item.kids {
				if d.typ != "data" {
					continue
				}
				payload := d.dataPayload()
				if d.dataType() == 2 { // UTF-16 text
					m.fields = append(m.fields, Field{key, decodeText(1, payload)})
					continue
				}
				m.fields = append(m.fields, Field{key, mp4DecodeValue(item, payload)})
			}
		}
	}
	return m, nil
}

func openMP4(path string) (*mp4File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := parseMP4(data)
	if err != nil {
		return nil, err
	}
	m.path = path
	return m, nil
}

// ensureIlst returns (creating if needed) moov/udta/meta/ilst.
func (m *mp4File) ensureIlst() *atom {
	moov := m.moov
	udta := moov.find("udta")
	if udta == nil {
		udta = &atom{typ: "udta"}
		moov.kids = append(moov.kids, udta)
	}
	meta := udta.find("meta")
	if meta == nil {
		meta = &atom{typ: "meta", full: true}
		hdlr := &atom{typ: "hdlr", data: []byte{
			0, 0, 0, 0, // version/flags
			0, 0, 0, 0, // pre_defined
			'm', 'd', 'i', 'r', // handler type
			'a', 'p', 'p', 'l', // manufacturer
			0, 0, 0, 0, 0, 0, 0, 0, // reserved
			0, // name (empty, pascal string)
		}}
		meta.kids = append(meta.kids, hdlr)
		udta.kids = append(udta.kids, meta)
	}
	ilst := meta.find("ilst")
	if ilst == nil {
		ilst = &atom{typ: "ilst"}
		meta.kids = append(meta.kids, ilst)
	}
	return ilst
}

func (m *mp4File) apply(e Edits) error {
	ilst := m.ensureIlst()
	pics := []Picture{}
	for _, item := range ilst.kids {
		if item.typ == "covr" {
			for _, d := range item.kids {
				if d.typ == "data" {
					mime := "image/jpeg"
					if d.dataType() == 14 {
						mime = "image/png"
					}
					pics = append(pics, Picture{MIME: mime, Kind: 3, Data: append([]byte{}, d.dataPayload()...)})
				}
			}
		}
	}
	pics = applyPictureEdits(pics, e)
	newPics := e.PictureAction != "" || e.RemoveAllPictures
	if newPics {
		ilst.remove("covr")
		if len(pics) > 0 {
			p := pics[0]
			kind := uint32(13)
			if strings.Contains(p.MIME, "png") {
				kind = 14
			}
			item := &atom{typ: "covr", isItem: true, kids: []*atom{dataAtom(kind, p.Data)}}
			ilst.kids = append(ilst.kids, item)
		}
	}
	for k, v := range e.Set {
		key := CanonicalKey(k)
		if key == "" {
			continue
		}
		if v == "" {
			m.deleteKey(ilst, key)
			continue
		}
		m.setKey(ilst, key, v)
	}
	for _, k := range e.Remove {
		m.deleteKey(ilst, CanonicalKey(k))
	}
	return m.write()
}

func (m *mp4File) setKey(ilst *atom, key, value string) {
	if typ, ok := keyMP4[key]; ok {
		typ2, payload := mp4EncodeValue(key, value)
		item := m.itemByType(ilst, typ)
		if item == nil {
			item = &atom{typ: typ, isItem: true}
			ilst.kids = append(ilst.kids, item)
		}
		item.kids = []*atom{dataAtom(typ2, payload)}
		return
	}
	// freeform ----:com.apple.iTunes:KEY
	for _, item := range ilst.kids {
		if item.typ == "----" && mp4ItemKey(item) == key {
			item.kids = []*atom{
				{typ: "mean", data: append([]byte{0, 0, 0, 0}, "com.apple.iTunes"...)},
				{typ: "name", data: append([]byte{0, 0, 0, 0}, strings.ToUpper(key)...)},
				dataAtom(1, []byte(value)),
			}
			return
		}
	}
	item := &atom{typ: "----", isItem: true, kids: []*atom{
		{typ: "mean", data: append([]byte{0, 0, 0, 0}, "com.apple.iTunes"...)},
		{typ: "name", data: append([]byte{0, 0, 0, 0}, strings.ToUpper(key)...)},
		dataAtom(1, []byte(value)),
	}}
	ilst.kids = append(ilst.kids, item)
}

func (m *mp4File) itemByType(ilst *atom, typ string) *atom {
	for _, item := range ilst.kids {
		if item.typ == typ {
			return item
		}
	}
	return nil
}

func (m *mp4File) deleteKey(ilst *atom, key string) {
	if typ, ok := keyMP4[key]; ok {
		ilst.remove(typ)
		if key == "GENRE" {
			ilst.remove("gnre")
		}
		return
	}
	var out []*atom
	for _, item := range ilst.kids {
		if item.typ == "----" && mp4ItemKey(item) == key {
			continue
		}
		out = append(out, item)
	}
	ilst.kids = out
}

func (m *mp4File) write() error {
	newMoov := m.moov.encode()
	delta := len(newMoov) - m.moovLen
	if delta != 0 {
		m.shiftChunkOffsets(delta)
		newMoov = m.moov.encode()
		if m.moovOff+int(m.moovLen) < len(m.data) {
			// nothing else to do: the tail is copied verbatim below
		}
	}
	var out bytes.Buffer
	out.Write(m.data[:m.moovOff])
	out.Write(newMoov)
	out.Write(m.data[m.moovOff+int(m.moovLen):])
	return writeFileAtomic(m.path, out.Bytes())
}

// shiftChunkOffsets fixes stco/co64 entries when the moov box changes size.
func (m *mp4File) shiftChunkOffsets(delta int) {
	if delta == 0 {
		return
	}
	moovEnd := m.moovOff + m.moovLen
	var walk func(a *atom)
	walk = func(a *atom) {
		if a.typ == "stco" && len(a.data) >= 8 {
			n := int(be32(a.data[4:8]))
			for i := 0; i < n; i++ {
				p := 8 + i*4
				if p+4 > len(a.data) {
					break
				}
				v := int(be32(a.data[p : p+4]))
				if v >= moovEnd {
					putBE32(a.data[p:p+4], uint32(v+delta))
				}
			}
		}
		if a.typ == "co64" && len(a.data) >= 8 {
			n := int(be32(a.data[4:8]))
			for i := 0; i < n; i++ {
				p := 8 + i*8
				if p+8 > len(a.data) {
					break
				}
				v := int(be64(a.data[p : p+8]))
				if v >= moovEnd {
					putBE64(a.data[p:p+8], uint64(v+delta))
				}
			}
		}
		for _, k := range a.kids {
			walk(k)
		}
	}
	walk(m.moov)
}
