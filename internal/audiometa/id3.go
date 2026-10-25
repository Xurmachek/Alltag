package audiometa

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ------------------------------------------------------------------ ID3 genres

var id3Genres = []string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge",
	"Hip-Hop", "Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B",
	"Rap", "Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska",
	"Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient",
	"Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance", "Classical",
	"Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"AlternRock", "Bass", "Soul", "Punk", "Space", "Meditative",
	"Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic", "Darkwave",
	"Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream",
	"Southern Rock", "Comedy", "Cult", "Gangsta", "Top 40", "Christian Rap",
	"Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave",
	"Psychedelic", "Rave", "Showtunes", "Trailer", "Lo-Fi", "Tribal",
	"Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll",
	"Hard Rock", "Folk", "Folk-Rock", "National Folk", "Swing", "Fast Fusion",
	"Bebob", "Latin", "Revival", "Celtic", "Bluegrass", "Avantgarde",
	"Gothic Rock", "Progressive Rock", "Psychedelic Rock", "Symphonic Rock",
	"Slow Rock", "Big Band", "Chorus", "Easy Listening", "Acoustic", "Humour",
	"Speech", "Chanson", "Opera", "Chamber Music", "Sonata", "Symphony",
	"Booty Bass", "Primus", "Porn Groove", "Satire", "Slow Jam", "Club",
	"Tango", "Samba", "Folklore", "Ballad", "Power Ballad", "Rhythmic Soul",
	"Freestyle", "Duet", "Punk Rock", "Drum Solo", "A capella", "Euro-House",
	"Dance Hall", "Goa", "Drum & Bass", "Club-House", "Hardcore", "Terror",
	"Indie", "BritPop", "Negerpunk", "Polsk Punk", "Beat",
	"Christian Gangsta Rap", "Heavy Metal", "Black Metal", "Crossover",
	"Contemporary Christian", "Christian Rock", "Merengue", "Salsa",
	"Thrash Metal", "Anime", "JPop", "Synthpop",
}

func genreName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2 && s[0] == '(' && s[len(s)-1] == ')' {
		if n, err := strconv.Atoi(s[1 : len(s)-1]); err == nil && n >= 0 && n < len(id3Genres) {
			return id3Genres[n]
		}
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < len(id3Genres) {
		return id3Genres[n]
	}
	return s
}

// ------------------------------------------------------------------ ID3v2 model

type id3Frame struct {
	ID    string
	Flags uint16
	Data  []byte
}

type id3Tag struct {
	Version int // 2, 3, 4
	Frames  []*id3Frame
	Padding int
	used    bool // true when the file already had an ID3v2 tag
}

func newID3Tag(version int) *id3Tag {
	if version == 0 {
		version = 3
	}
	return &id3Tag{Version: version, Padding: 1024, used: true}
}

// ------------------------------------------------------------------ synchsafe

func synchsafe(b []byte) int {
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

func putSynchsafe(b []byte, v int) {
	b[0] = byte(v>>21) & 0x7F
	b[1] = byte(v>>14) & 0x7F
	b[2] = byte(v>>7) & 0x7F
	b[3] = byte(v) & 0x7F
}

func deunsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0x00 {
			i++
		}
	}
	return out
}

// ------------------------------------------------------------------ text codecs

func decodeText(enc byte, b []byte) string {
	switch enc {
	case 0:
		return latin1ToUTF8(b)
	case 1:
		if len(b) < 2 {
			return ""
		}
		var u16 []uint16
		if b[0] == 0xFF && b[1] == 0xFE {
			b = b[2:]
			for i := 0; i+1 < len(b); i += 2 {
				u16 = append(u16, binary.LittleEndian.Uint16(b[i:]))
			}
		} else if b[0] == 0xFE && b[1] == 0xFF {
			b = b[2:]
			for i := 0; i+1 < len(b); i += 2 {
				u16 = append(u16, binary.BigEndian.Uint16(b[i:]))
			}
		} else {
			for i := 0; i+1 < len(b); i += 2 {
				u16 = append(u16, binary.LittleEndian.Uint16(b[i:]))
			}
		}
		return string(utf16.Decode(u16))
	case 2:
		var u16 []uint16
		for i := 0; i+1 < len(b); i += 2 {
			u16 = append(u16, binary.BigEndian.Uint16(b[i:]))
		}
		return string(utf16.Decode(u16))
	case 3:
		return strings.TrimRight(string(b), "\x00")
	}
	return latin1ToUTF8(b)
}

func latin1ToUTF8(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return strings.TrimRight(sb.String(), "\x00")
}

func isLatin1(s string) bool {
	for _, r := range s {
		if r > 0xFF {
			return false
		}
	}
	return true
}

// encodeText writes [encoding byte][payload].
func encodeText(s string, version int) []byte {
	s = strings.TrimRight(s, "\x00")
	if version == 4 {
		return append([]byte{3}, []byte(s)...)
	}
	if isLatin1(s) {
		b := make([]byte, 0, len(s)+1)
		b = append(b, 0)
		for _, r := range s {
			b = append(b, byte(r))
		}
		return b
	}
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 3+len(u)*2)
	b = append(b, 1, 0xFF, 0xFE)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

func splitEncoded(b []byte) (string, []byte) {
	// splits a NUL terminated string inside encoded text; returns str and rest
	if len(b) == 0 {
		return "", nil
	}
	enc := b[0]
	body := b[1:]
	switch enc {
	case 1, 2:
		for i := 0; i+1 < len(body); i += 2 {
			if body[i] == 0 && body[i+1] == 0 {
				return decodeText(enc, body[:i]), body[i+2:]
			}
		}
		return decodeText(enc, body), nil
	default:
		if i := bytes.IndexByte(body, 0); i >= 0 {
			return decodeText(enc, body[:i]), body[i+1:]
		}
		return decodeText(enc, body), nil
	}
}

// ------------------------------------------------------------------ frame ids

var v22to23 = map[string]string{
	"TT1": "TIT1", "TT2": "TIT2", "TT3": "TIT3", "TP1": "TPE1", "TP2": "TPE2",
	"TP3": "TPE3", "TP4": "TPE4", "TAL": "TALB", "TRK": "TRCK", "TPA": "TPOS",
	"TCO": "TCON", "TYE": "TYER", "TDA": "TDAT", "TIM": "TIME", "TRD": "TRDA",
	"TCM": "TCOM", "TBP": "TBPM", "TEN": "TENC", "TPB": "TPUB", "TCR": "TCOP",
	"TXT": "TEXT", "TLA": "TLAN", "TKE": "TKEY", "TMT": "TMED", "TFT": "TFLT",
	"COM": "COMM", "ULT": "USLT", "TXX": "TXXX", "PIC": "APIC", "GEO": "GEOB",
	"WXX": "WXXX", "UFI": "UFID", "CNT": "PCNT", "POP": "POPM", "TS2": "TSO2",
	"TSA": "TSOA", "TSP": "TSOP", "TST": "TSOT",
}

// textFrameKeys maps ID3 frames to canonical keys.
var textFrameKeys = map[string]string{
	"TIT1": "GROUPING", "TIT2": "TITLE", "TIT3": "SUBTITLE",
	"TPE1": "ARTIST", "TPE2": "ALBUMARTIST", "TPE3": "CONDUCTOR",
	"TPE4": "ARRANGER", "TALB": "ALBUM", "TRCK": "TRACK", "TPOS": "DISCNUMBER",
	"TCON": "GENRE", "TCOM": "COMPOSER", "TBPM": "BPM", "TENC": "ENCODER",
	"TPUB": "PUBLISHER", "TCOP": "COPYRIGHT", "TEXT": "LYRICIST",
	"TLAN": "LANGUAGE", "TKEY": "INITIALKEY", "TMED": "MEDIATYPE",
	"TSOA": "ALBUMSORT", "TSOP": "ARTISTSORT", "TSOT": "TITLESORT",
	"TSSE": "ENCODERSETTINGS", "TCMP": "COMPILATION", "TSRC": "ISRC",
	"TOPE": "ORIGINALARTIST", "TOAL": "ORIGINALALBUM", "TSO2": "ALBUMARTISTSORT",
	"TDOR": "ORIGINALDATE", "TDRL": "RELEASEDATE", "TLEN": "LENGTH",
	"TMOO": "MOOD", "TCAT": "CATALOGNUMBER", "TDEN": "ENCODEDDATE",
}

var keyFrames = func() map[string]string {
	m := map[string]string{}
	for f, k := range textFrameKeys {
		m[k] = f
	}
	m["YEAR"] = "TYER"
	m["DATE"] = "TYER"
	m["LYRICS"] = "USLT"
	m["COMMENT"] = "COMM"
	return m
}()

// frameForKey returns the frame id used to store a canonical key.
func frameForKey(key string, version int) (id string, isText bool) {
	key = CanonicalKey(key)
	if key == "YEAR" || key == "DATE" {
		if version == 4 {
			return "TDRC", true
		}
		return "TYER", true
	}
	if f, ok := keyFrames[key]; ok {
		switch f {
		case "COMM", "USLT":
			return f, true
		}
		return f, true
	}
	return "TXXX", true
}

// ------------------------------------------------------------------ parsing

func hasID3(buf []byte) bool {
	return len(buf) >= 10 && string(buf[:3]) == "ID3"
}

// readID3Tag parses a tag starting at the beginning of buf. It returns the tag
// and the total number of bytes consumed (0 when there is no tag).
func readID3Tag(buf []byte) (*id3Tag, int) {
	if !hasID3(buf) {
		return nil, 0
	}
	ver := int(buf[3])
	flags := buf[5]
	size := synchsafe(buf[6:10])
	if size < 0 || 10+size > len(buf) {
		size = len(buf) - 10
		if size < 0 {
			return nil, 0
		}
	}
	body := buf[10 : 10+size]
	if flags&0x80 != 0 {
		body = deunsync(body)
	}
	t := &id3Tag{Version: ver, used: true, Padding: 0}
	if ver == 2 {
		t.Version = 2
	}
	// extended header
	pos := 0
	if flags&0x40 != 0 && len(body) >= 4 {
		if t.Version == 4 {
			pos = synchsafe(body[:4])
		} else {
			pos = int(be32(body[:4])) + 4
		}
		if pos < 0 || pos > len(body) {
			pos = 0
		}
	}
	for pos < len(body) {
		var id string
		var fsize int
		var fflags uint16
		if t.Version == 2 {
			if pos+6 > len(body) {
				break
			}
			id = string(body[pos : pos+3])
			if !validFrameID(id, 3) {
				break
			}
			fsize = be24(body[pos+3 : pos+6])
			pos += 6
		} else {
			if pos+10 > len(body) {
				break
			}
			id = string(body[pos : pos+4])
			if !validFrameID(id, 4) {
				break
			}
			if t.Version == 4 {
				fsize = synchsafe(body[pos+4 : pos+8])
			} else {
				fsize = int(be32(body[pos+4 : pos+8]))
			}
			fflags = binary.BigEndian.Uint16(body[pos+8 : pos+10])
			pos += 10
		}
		if fsize <= 0 || pos+fsize > len(body) {
			break
		}
		data := body[pos : pos+fsize]
		pos += fsize
		flags2 := byte(fflags)
		if t.Version == 4 {
			if fflags&0x000D != 0 { // compression / encryption / unsupported
				continue
			}
			if flags2&0x40 != 0 && len(data) > 1 { // grouping identity
				data = data[1:]
			}
			if flags2&0x02 != 0 { // frame level unsynchronisation
				data = deunsync(data)
			}
			if flags2&0x01 != 0 && len(data) >= 4 { // data length indicator
				data = data[4:]
			}
		} else {
			if fflags&0x00C0 != 0 { // compression / encryption
				continue
			}
			if flags2&0x20 != 0 && len(data) > 1 { // group identity
				data = data[1:]
			}
		}
		if t.Version == 2 {
			if repl, ok := v22to23[id]; ok {
				id = repl
			}
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		t.Frames = append(t.Frames, &id3Frame{ID: id, Flags: fflags, Data: cp})
	}
	return t, 10 + size
}

func validFrameID(id string, n int) bool {
	if len(id) != n {
		return false
	}
	for i := 0; i < n; i++ {
		c := id[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// fields converts the parsed frames to canonical key/value pairs.
func (t *id3Tag) fields() []Field {
	var out []Field
	seen := map[string]bool{}
	add := func(k, v string) {
		if k == "" || v == "" {
			return
		}
		out = append(out, Field{CanonicalKey(k), v})
		seen[CanonicalKey(k)] = true
	}
	for _, f := range t.Frames {
		if len(f.Data) == 0 {
			continue
		}
		switch {
		case f.ID == "TXXX":
			enc := f.Data[0]
			desc, rest := splitEncoded(f.Data)
			_ = enc
			add(desc, decodeText(textEncOf(f.Data), rest))
		case f.ID == "COMM":
			if len(f.Data) < 4 {
				continue
			}
			enc := f.Data[0]
			rest := f.Data[4:]
			_, val := splitEncoded(append([]byte{enc}, rest...))
			add("COMMENT", string(val))
		case f.ID == "USLT":
			if len(f.Data) < 4 {
				continue
			}
			enc := f.Data[0]
			rest := f.Data[4:]
			_, val := splitEncoded(append([]byte{enc}, rest...))
			add("LYRICS", string(val))
		case f.ID == "TCON":
			add("GENRE", genreName(decodeText(textEncOf(f.Data), f.Data[1:])))
		case strings.HasPrefix(f.ID, "T"):
			vals := decodeTextValues(f.Data)
			key, ok := textFrameKeys[f.ID]
			if !ok {
				key = f.ID
			}
			if key == "YEAR" && strings.HasPrefix(strings.TrimSpace(vals), "0000") {
				continue
			}
			add(key, strings.TrimSpace(vals))
		}
	}
	_ = seen
	return out
}

func textEncOf(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// decodeTextValues handles multi-value text frames (ID3v2.4 uses NUL).
func decodeTextValues(data []byte) string {
	if len(data) < 1 {
		return ""
	}
	enc := data[0]
	body := data[1:]
	if enc == 3 {
		parts := strings.Split(strings.TrimRight(string(body), "\x00"), "\x00")
		return strings.Join(parts, "; ")
	}
	s := decodeText(enc, body)
	s = strings.ReplaceAll(s, "\x00", "; ")
	return strings.TrimSpace(s)
}

func (t *id3Tag) pictures() []Picture {
	var out []Picture
	for _, f := range t.Frames {
		if f.ID != "APIC" || len(f.Data) < 4 {
			continue
		}
		enc := f.Data[0]
		rest := f.Data[1:]
		z := bytes.IndexByte(rest, 0)
		if z < 0 {
			continue
		}
		mime := string(rest[:z])
		rest = rest[z+1:]
		if len(rest) < 1 {
			continue
		}
		kind := int(rest[0])
		rest = rest[1:]
		var desc string
		if enc == 1 || enc == 2 {
			for i := 0; i+1 < len(rest); i += 2 {
				if rest[i] == 0 && rest[i+1] == 0 {
					desc = decodeText(enc, rest[:i])
					rest = rest[i+2:]
					break
				}
			}
		} else if i := bytes.IndexByte(rest, 0); i >= 0 {
			desc = decodeText(enc, rest[:i])
			rest = rest[i+1:]
		}
		if mime == "PNG" || mime == "JPG" {
			mime = "image/" + strings.ToLower(mime)
		}
		data := make([]byte, len(rest))
		copy(data, rest)
		if len(data) == 0 {
			continue
		}
		if mime == "" {
			mime = mimeFromData(data)
		}
		out = append(out, Picture{MIME: mime, Kind: kind, Desc: desc, Data: data})
	}
	return out
}

// ------------------------------------------------------------------ mutation

func (t *id3Tag) set(key, value string) {
	key = CanonicalKey(key)
	if value == "" {
		t.delete(key)
		return
	}
	fid, _ := frameForKey(key, t.Version)
	switch fid {
	case "COMM":
		t.setTextFrame("COMM", commData(t.Version, value))
	case "USLT":
		t.setTextFrame("USLT", usltData(t.Version, value))
	case "TXXX":
		t.setTextFrame("TXXX", txxxData(t.Version, key, value))
	default:
		t.setTextFrame(fid, encodeText(value, t.Version))
	}
}

func (t *id3Tag) delete(key string) {
	key = CanonicalKey(key)
	fid, _ := frameForKey(key, t.Version)
	out := t.Frames[:0]
	for _, f := range t.Frames {
		switch fid {
		case "TXXX":
			if f.ID == "TXXX" && txxxKey(f.Data) == key {
				continue
			}
		case "COMM":
			if f.ID == "COMM" && isDefaultDescription(f.Data) {
				continue
			}
		case "USLT":
			if f.ID == "USLT" && isDefaultDescription(f.Data) {
				continue
			}
		default:
			if f.ID == fid {
				continue
			}
			if key == "YEAR" && (f.ID == "TYER" || f.ID == "TDRC" || f.ID == "TDAT") {
				continue
			}
		}
		out = append(out, f)
	}
	t.Frames = out
}

func isDefaultDescription(data []byte) bool {
	if len(data) < 4 {
		return true
	}
	enc := data[0]
	rest := data[4:]
	str, _ := splitEncoded(append([]byte{enc}, rest...))
	return strings.TrimSpace(str) == ""
}

func txxxKey(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	enc := data[0]
	s, _ := splitEncoded(append([]byte{enc}, data[1:]...))
	return CanonicalKey(s)
}

func commData(version int, value string) []byte {
	b := encodeText(value, version)
	enc := b[0]
	out := make([]byte, 0, len(b)+6)
	out = append(out, enc, 'e', 'n', 'g')
	if enc == 1 || enc == 2 {
		out = append(out, 0, 0) // empty short description (UTF-16)
	} else {
		out = append(out, 0) // empty short description
	}
	out = append(out, b[1:]...)
	return out
}

func usltData(version int, value string) []byte {
	return commData(version, value)
}

func txxxData(version int, key, value string) []byte {
	enc := byte(0)
	if version == 4 {
		enc = 3
	} else if !isLatin1(key) || !isLatin1(value) {
		enc = 1
	}
	kb := encodeText(strings.ToUpper(key), version)
	if len(kb) > 0 {
		kb = kb[1:]
	}
	vb := encodeText(value, version)
	if len(vb) > 0 {
		vb = vb[1:]
	}
	out := []byte{enc}
	out = append(out, kb...)
	if enc == 1 || enc == 2 {
		out = append(out, 0, 0)
	} else {
		out = append(out, 0)
	}
	out = append(out, vb...)
	return out
}

func (t *id3Tag) setTextFrame(id string, data []byte) {
	for _, f := range t.Frames {
		if f.ID == id {
			// keep multi-value frames such as TXXX:KEY separate
			if id == "TXXX" {
				continue
			}
			f.Data = data
			return
		}
	}
	t.Frames = append(t.Frames, &id3Frame{ID: id, Data: data})
}

func (t *id3Tag) setPictures(pics []Picture) {
	out := t.Frames[:0]
	for _, f := range t.Frames {
		if f.ID != "APIC" {
			out = append(out, f)
		}
	}
	t.Frames = out
	for _, p := range pics {
		t.Frames = append(t.Frames, &id3Frame{ID: "APIC", Data: apicData(p, t.Version)})
	}
}

func apicData(p Picture, version int) []byte {
	enc := byte(0)
	if version == 4 {
		enc = 3
	} else if !isLatin1(p.Desc) {
		enc = 1
	}
	mime := p.MIME
	if mime == "" {
		mime = mimeFromData(p.Data)
	}
	kind := p.Kind
	if kind == 0 {
		kind = 3
	}
	out := []byte{enc}
	out = append(out, []byte(mime)...)
	out = append(out, 0, byte(kind))
	db := encodeText(p.Desc, version)
	if len(db) > 0 {
		db = db[1:]
	}
	out = append(out, db...)
	if enc == 1 {
		out = append(out, 0, 0)
	} else {
		out = append(out, 0)
	}
	out = append(out, p.Data...)
	return out
}

// encode serialises the tag, including header and padding.
func (t *id3Tag) encode() []byte {
	ver := t.Version
	if ver == 2 {
		ver = 3 // upgrade ID3v2.2 to v2.3 on save
	}
	var body bytes.Buffer
	for _, f := range t.Frames {
		if len(f.Data) == 0 || f.ID == "" {
			continue
		}
		hdr := make([]byte, 10)
		copy(hdr, f.ID)
		if ver == 4 {
			putSynchsafe(hdr[4:8], len(f.Data))
		} else {
			putBE32(hdr[4:8], uint32(len(f.Data)))
		}
		body.Write(hdr)
		body.Write(f.Data)
	}
	pad := t.Padding
	if pad < 0 {
		pad = 0
	}
	size := body.Len() + pad
	out := make([]byte, 10, 10+size)
	copy(out, "ID3")
	out[3] = byte(ver)
	out[4] = 0
	out[5] = 0
	putSynchsafe(out[6:10], size)
	out = append(out, body.Bytes()...)
	out = append(out, make([]byte, pad)...)
	return out
}

// ------------------------------------------------------------------ ID3v1

type id3v1 struct {
	Title, Artist, Album, Year, Comment, Genre string
	Track                                      int
}

func parseID3v1(b []byte) *id3v1 {
	if len(b) < 128 || string(b[:3]) != "TAG" {
		return nil
	}
	t := &id3v1{
		Title:   strings.TrimRight(latin1ToUTF8(b[3:33]), " \x00"),
		Artist:  strings.TrimRight(latin1ToUTF8(b[33:63]), " \x00"),
		Album:   strings.TrimRight(latin1ToUTF8(b[63:93]), " \x00"),
		Year:    strings.TrimRight(latin1ToUTF8(b[93:97]), " \x00"),
		Comment: strings.TrimRight(latin1ToUTF8(b[97:127]), " \x00"),
	}
	if b[125] == 0 && b[126] != 0 {
		t.Comment = strings.TrimRight(latin1ToUTF8(b[97:125]), " \x00")
		t.Track = int(b[126])
	}
	if g := int(b[127]); g < len(id3Genres) {
		t.Genre = id3Genres[g]
	}
	return t
}

func buildID3v1(fields map[string]string) []byte {
	b := make([]byte, 128)
	copy(b, "TAG")
	put := func(dst []byte, s string) {
		if !isLatin1(s) {
			s = transliterate(s)
		}
		for i := 0; i < len(dst) && i < len(s); i++ {
			dst[i] = s[i]
		}
	}
	put(b[3:33], fields["TITLE"])
	put(b[33:63], fields["ARTIST"])
	put(b[63:93], fields["ALBUM"])
	put(b[93:97], fields["YEAR"])
	comment := fields["COMMENT"]
	put(b[97:127], comment)
	track, _ := strconv.Atoi(digitsOnly(fields["TRACK"]))
	if track > 0 && track < 256 {
		put(b[97:125], comment)
		b[125] = 0
		b[126] = byte(track)
	}
	genre := 255
	for i, g := range id3Genres {
		if strings.EqualFold(g, fields["GENRE"]) {
			genre = i
			break
		}
	}
	b[127] = byte(genre)
	return b
}

func transliterate(s string) string {
	rep := map[rune]string{
		'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
		'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
		'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
		'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
		'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
		'А': "A", 'Б': "B", 'В': "V", 'Г': "G", 'Д': "D", 'Е': "E", 'Ё': "E",
		'Ж': "Zh", 'З': "Z", 'И': "I", 'Й': "Y", 'К': "K", 'Л': "L", 'М': "M",
		'Н': "N", 'О': "O", 'П': "P", 'Р': "R", 'С': "S", 'Т': "T", 'У': "U",
		'Ф': "F", 'Х': "Kh", 'Ц': "Ts", 'Ч': "Ch", 'Ш': "Sh", 'Щ': "Sch",
		'Ъ': "", 'Ы': "Y", 'Ь': "", 'Э': "E", 'Ю': "Yu", 'Я': "Ya",
	}
	var sb strings.Builder
	for _, r := range s {
		if rep2, ok := rep[r]; ok {
			sb.WriteString(rep2)
		} else if r < 256 {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('?')
		}
	}
	return sb.String()
}

func digitsOnly(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// ------------------------------------------------------------------ MPEG info

type mpegInfo struct {
	bitrate    int
	sampleRate int
	channels   int
	frames     int
	duration   float64
	layer      int
	version    int
	cbr        bool
}

var bitrateTable = map[[2]int][]int{
	{1, 1}: {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448},
	{1, 2}: {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384},
	{1, 3}: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	{2, 1}: {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256},
	{2, 2}: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	{2, 3}: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}

var sampleRateTable = map[int][]int{
	1:  {44100, 48000, 32000},
	2:  {22050, 24000, 16000},
	25: {11025, 12000, 8000},
}

func parseMPEGHeader(b []byte) (*mpegInfo, int) {
	for i := 0; i+4 <= len(b); i++ {
		if b[i] != 0xFF || b[i+1]&0xE0 != 0xE0 {
			continue
		}
		verBits := (b[i+1] >> 3) & 3
		layerBits := (b[i+1] >> 1) & 3
		if verBits == 1 || layerBits == 0 {
			continue
		}
		brIdx := int(b[i+2] >> 4)
		srIdx := int((b[i+2] >> 2) & 3)
		if brIdx == 0 || brIdx == 15 || srIdx == 3 {
			continue
		}
		version := 1
		switch verBits {
		case 0:
			version = 25
		case 2:
			version = 2
		}
		layer := 4 - int(layerBits)
		m := &mpegInfo{layer: layer, version: version}
		tblKey := [2]int{1, layer}
		if version != 1 {
			tblKey = [2]int{2, layer}
		}
		if tbl, ok := bitrateTable[tblKey]; ok {
			m.bitrate = tbl[brIdx]
		}
		sr := sampleRateTable[version]
		m.sampleRate = sr[srIdx]
		if (b[i+3] >> 6) == 3 {
			m.channels = 1
		} else {
			m.channels = 2
		}
		return m, i
	}
	return nil, -1
}

func mpegSamplesPerFrame(m *mpegInfo) int {
	if m.layer == 1 {
		return 384
	}
	if m.layer == 2 {
		return 1152
	}
	if m.version == 1 {
		return 1152
	}
	return 576
}

// xingInfo looks for a Xing/Info/VBRI header inside the first frame.
func xingInfo(frame []byte, m *mpegInfo) (frames int, bytes int, ok bool) {
	// side info length
	sideInfo := 32
	if m.version != 1 {
		sideInfo = 17
	}
	if m.channels == 1 {
		sideInfo /= 2
	}
	off := 4 + sideInfo
	if len(frame) > off+8 {
		tag := string(frame[off : off+4])
		if tag == "Xing" || tag == "Info" {
			flags := be32(frame[off+4 : off+8])
			p := off + 8
			if flags&1 != 0 && p+4 <= len(frame) {
				frames = int(be32(frame[p : p+4]))
				p += 4
			}
			if flags&2 != 0 && p+4 <= len(frame) {
				bytes = int(be32(frame[p : p+4]))
			}
			return frames, bytes, true
		}
		if tag == "VBRI" {
			if off+18 <= len(frame) {
				return int(be32(frame[off+14 : off+18])), 0, true
			}
		}
	}
	return 0, 0, false
}

// ------------------------------------------------------------------ MP3 read

func readMP3(f *os.File, size int64, path string) (*Tags, error) {
	head := make([]byte, 10)
	n, _ := f.ReadAt(head, 0)
	head = head[:n]
	var tag *id3Tag
	audioStart := int64(0)
	if hasID3(head) {
		full := make([]byte, min64(size, 32<<20))
		f.ReadAt(full, 0)
		var consumed int
		tag, consumed = readID3Tag(full)
		audioStart = int64(consumed)
	}
	// tail: ID3v1 and APE tag live at the end
	tail := make([]byte, min64(size, 128*1024))
	if size > 0 {
		f.ReadAt(tail, size-int64(len(tail)))
	}
	v1 := parseID3v1(tail[len(tail)-128:])
	var ape *apeTag
	apeSearchEnd := len(tail)
	if v1 != nil && apeSearchEnd > 128 {
		apeSearchEnd -= 128
	}
	if a, _ := findAPETag(tail[:apeSearchEnd], apeSearchEnd); a != nil && a.plausible() {
		ape = a
	}

	t := &Tags{Format: "MP3"}
	if tag != nil {
		t.Fields = append(t.Fields, tag.fields()...)
		t.Pictures = tag.pictures()
		t.TagType = fmt.Sprintf("ID3v2.%d", maxInt(tag.Version, 3))
	} else if ape != nil && len(ape.Items) > 0 {
		t.Fields = append(t.Fields, ape.fields()...)
		t.Pictures = append(t.Pictures, ape.pictures()...)
		t.TagType = "APEv2"
	} else if v1 != nil {
		t.Fields = append(t.Fields, v1.fields()...)
		t.TagType = "ID3v1"
	}
	if ape != nil && tag != nil && len(ape.Items) > 0 {
		// merge APE values that ID3 does not carry (e.g. ReplayGain)
		have := map[string]bool{}
		for _, fld := range t.Fields {
			have[fld.Key] = true
		}
		for _, fld := range ape.fields() {
			if !have[fld.Key] {
				t.Fields = append(t.Fields, fld)
			}
		}
	}

	// audio info
	audioEnd := size
	if v1 != nil {
		audioEnd -= 128
	}
	if ape != nil {
		audioEnd -= int64(ape.totalSize)
	}
	if audioEnd < audioStart {
		audioEnd = audioStart
	}
	probe := make([]byte, min64(audioEnd-audioStart, 256*1024))
	if len(probe) > 0 {
		f.ReadAt(probe, audioStart)
	}
	if m, _ := parseMPEGHeader(probe); m != nil {
		t.Info.SampleRate = m.sampleRate
		t.Info.Channels = m.channels
		t.Info.Lossless = false
		codec := "MPEG Layer III"
		switch m.layer {
		case 1:
			codec = "MPEG Layer I"
		case 2:
			codec = "MPEG Layer II"
		}
		t.Info.Codec = codec
		audioBytes := audioEnd - audioStart
		frames, xbytes, ok := xingInfo(probe, m)
		spf := mpegSamplesPerFrame(m)
		if ok && frames > 0 {
			t.Info.Duration = float64(frames*spf) / float64(m.sampleRate)
			if xbytes > 0 {
				t.Info.Bitrate = int(float64(xbytes) * 8 / t.Info.Duration / 1000)
			} else {
				t.Info.Bitrate = int(float64(audioBytes) * 8 / t.Info.Duration / 1000)
			}
		} else if m.bitrate > 0 {
			t.Info.Bitrate = m.bitrate
			t.Info.Duration = float64(audioBytes) * 8 / float64(m.bitrate) / 1000
		}
	}
	if v1 != nil {
		t.Warnings = append(t.Warnings, "ID3v1")
	}
	return t, nil
}

func (v *id3v1) fields() []Field {
	var out []Field
	add := func(k, val string) {
		if val != "" {
			out = append(out, Field{k, val})
		}
	}
	add("TITLE", v.Title)
	add("ARTIST", v.Artist)
	add("ALBUM", v.Album)
	add("YEAR", v.Year)
	add("COMMENT", v.Comment)
	add("GENRE", v.Genre)
	if v.Track > 0 {
		add("TRACK", strconv.Itoa(v.Track))
	}
	return out
}

// ------------------------------------------------------------------ MP3 write

type mp3File struct {
	path      string
	data      []byte
	tag       *id3Tag
	tagLen    int
	v1        *id3v1
	apeStart  int64
	apeEnd    int64
	audioFrom int64
	audioTo   int64
	hasV1     bool
	hasAPE    bool
}

func openMP3(path string) (*mp3File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := &mp3File{path: path, data: data}
	if hasID3(data) {
		m.tag, m.tagLen = readID3Tag(data)
		if m.tag == nil {
			m.tagLen = 0
		}
	}
	if m.tag == nil {
		m.tag = newID3Tag(3)
		m.tag.used = false
	}
	m.audioFrom = int64(m.tagLen)
	audioTo := int64(len(data))
	if len(data) >= 128 && string(data[len(data)-128:len(data)-125]) == "TAG" {
		m.hasV1 = true
		m.v1 = parseID3v1(data[len(data)-128:])
		audioTo -= 128
	}
	// APE tag located before ID3v1
	if int(audioTo) <= len(data) && audioTo > 0 {
		if a, start := findAPETag(data[:audioTo], int(audioTo)); a != nil && a.plausible() {
			m.apeStart = int64(start)
			m.apeEnd = audioTo
			audioTo = int64(start)
			m.hasAPE = true
		}
	}
	m.audioTo = audioTo
	if m.audioTo < m.audioFrom {
		m.audioTo = m.audioFrom
	}
	return m, nil
}

func (m *mp3File) apply(e Edits) error {
	if e.TagVersion == "2.4" {
		m.tag.Version = 4
	}
	pics := m.tag.pictures()
	pics = applyPictureEdits(pics, e)
	if e.PictureAction != "" || e.RemoveAllPictures {
		m.tag.setPictures(pics)
	}
	for k, v := range e.Set {
		if k == "" {
			continue
		}
		m.tag.set(k, v)
	}
	for _, k := range e.Remove {
		m.tag.delete(k)
	}
	if v := lookupCI(e.Set, "GENRE"); v != "" {
		m.tag.set("GENRE", genreName(v))
	}
	return m.write()
}

func (m *mp3File) write() error {
	audio := m.data[m.audioFrom:m.audioTo]
	var out bytes.Buffer
	if len(m.tag.Frames) > 0 {
		out.Write(m.tag.encode())
	}
	out.Write(audio)
	if m.hasAPE && m.apeEnd > m.apeStart {
		out.Write(m.data[m.apeStart:m.apeEnd])
	}
	if m.hasV1 {
		fields := map[string]string{}
		for _, f := range m.tag.fields() {
			fields[f.Key] = f.Value
		}
		out.Write(buildID3v1(fields))
	}
	return writeFileAtomic(m.path, out.Bytes())
}

// ------------------------------------------------------------------ utilities

func applyPictureEdits(pics []Picture, e Edits) []Picture {
	if e.RemoveAllPictures || e.PictureAction == "remove" {
		return nil
	}
	if e.PictureAction == "set" && len(e.Picture) > 0 {
		mime := e.PictureMIME
		if mime == "" {
			mime = mimeFromData(e.Picture)
		}
		return []Picture{{MIME: mime, Kind: 3, Data: e.Picture}}
	}
	return pics
}

func lookupCI(m map[string]string, key string) string {
	for k, v := range m {
		if CanonicalKey(k) == key {
			return v
		}
	}
	return ""
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".alltag-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name)
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil {
		os.Chmod(name, st.Mode())
	}
	return os.Rename(name, path)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func readAt(f io.ReaderAt, buf []byte, off int64) []byte {
	n, _ := f.ReadAt(buf, off)
	return buf[:n]
}
