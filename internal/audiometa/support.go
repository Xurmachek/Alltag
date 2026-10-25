package audiometa

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Errors returned by the engine.
var (
	ErrNoTag       = errors.New("теги не найдены")
	ErrUnsupported = errors.New("формат не поддерживается")
)

// ---------------------------------------------------------------- number helpers

func be16(b []byte) int    { return int(binary.BigEndian.Uint16(b)) }
func be24(b []byte) int    { return int(b[0])<<16 | int(b[1])<<8 | int(b[2]) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }
func le16(b []byte) int    { return int(binary.LittleEndian.Uint16(b)) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

func putBE32(b []byte, v uint32) []byte { binary.BigEndian.PutUint32(b, v); return b }
func putBE64(b []byte, v uint64) []byte { binary.BigEndian.PutUint64(b, v); return b }
func putLE32(b []byte, v uint32) []byte { binary.LittleEndian.PutUint32(b, v); return b }
func putLE64(b []byte, v uint64) []byte { binary.LittleEndian.PutUint64(b, v); return b }

// synchsafeEncode is the inverse of synchsafe.
func synchsafeEncode(v int) []byte {
	return []byte{
		byte(v >> 21 & 0x7f),
		byte(v >> 14 & 0x7f),
		byte(v >> 7 & 0x7f),
		byte(v & 0x7f),
	}
}

// mimeFromData sniffs the image type from its magic bytes.
func mimeFromData(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 2 && string(data[:2]) == "BM":
		return "image/bmp"
	}
	return "image/jpeg"
}

// ---------------------------------------------------------------- canonical keys

var keyAliases = map[string]string{
	"DATE": "YEAR", "TYER": "YEAR", "TDRC": "YEAR", "YEAR": "YEAR",
	"ORIGINALDATE": "ORIGINALYEAR", "ORIGINALYEAR": "ORIGINALYEAR",
	"TRACKNUMBER": "TRACK", "TRACK": "TRACK", "WM/TRACKNUMBER": "TRACK",
	"TRACKTOTAL": "TRACKTOTAL", "TOTALTRACKS": "TRACKTOTAL",
	"TOTAL TRACKS": "TRACKTOTAL", "TRACKC": "TRACKTOTAL",
	"DISC": "DISCNUMBER", "DISCNUMBER": "DISCNUMBER", "DISKNUMBER": "DISCNUMBER",
	"DISCTOTAL": "DISCTOTAL", "TOTALDISCS": "DISCTOTAL",
	"ALBUM ARTIST": "ALBUMARTIST", "ALBUMARTIST": "ALBUMARTIST",
	"ALBUM_ARTIST": "ALBUMARTIST", "TPE2": "ALBUMARTIST",
	"DESCRIPTION": "COMMENT", "COMMENT": "COMMENT",
	"UNSYNCEDLYRICS": "LYRICS", "LYRICS": "LYRICS",
	"ENCODEDBY": "ENCODEDBY", "ENCODED BY": "ENCODEDBY", "TENC": "ENCODEDBY",
	"ENCODERSETTINGS": "ENCODERSETTINGS", "ENCODED SETTINGS": "ENCODERSETTINGS",
	"TITLE": "TITLE", "ARTIST": "ARTIST", "ALBUM": "ALBUM", "GENRE": "GENRE",
	"COMPOSER": "COMPOSER", "CONDUCTOR": "CONDUCTOR", "LABEL": "LABEL",
	"PUBLISHER": "LABEL", "COPYRIGHT": "COPYRIGHT", "ISRC": "ISRC",
	"GROUPING": "GROUPING", "SUBTITLE": "SUBTITLE", "MOOD": "MOOD",
	"BPM": "BPM", "LANGUAGE": "LANGUAGE", "COMPILATION": "COMPILATION",
	"RATING": "RATING", "LYRICIST": "LYRICIST", "ARRANGER": "ARRANGER",
	"PRODUCER": "PRODUCER", "MEDIA": "MEDIA", "SUPPORT": "MEDIA",
	// Windows Media / ASF spellings
	"ALBUMTITLE": "ALBUM", "BEATSPERMINUTE": "BPM", "PARTOFSET": "DISCNUMBER",
	"AUTHOR": "ARTIST", "WM/AUTHOR": "ARTIST", "GENREID": "GENRE",
	"ORIGINALRELEASEDATE": "ORIGINALYEAR", "CONTENTGROUP": "GROUPING",
}

// CanonicalKey normalises an arbitrary tag name to the display key that the UI
// and the batch editor use.
func CanonicalKey(name string) string {
	k := strings.ToUpper(strings.TrimSpace(name))
	k = strings.TrimPrefix(k, "WM/")
	if k == "" {
		return ""
	}
	if v, ok := keyAliases[k]; ok {
		return v
	}
	return k
}

var canonicalOrder = []string{
	"TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "YEAR", "TRACK", "TRACKTOTAL",
	"DISCNUMBER", "DISCTOTAL", "GENRE", "COMMENT", "COMPOSER", "LYRICS", "BPM",
	"CONDUCTOR", "ARRANGER", "LYRICIST", "PRODUCER", "LABEL", "COPYRIGHT",
	"ISRC", "ENCODEDBY", "ENCODERSETTINGS", "GROUPING", "SUBTITLE",
	"COMPILATION", "MOOD", "LANGUAGE", "RATING",
}

var canonicalRank = func() map[string]int {
	m := make(map[string]int, len(canonicalOrder))
	for i, k := range canonicalOrder {
		m[k] = i
	}
	return m
}()

// sortFields orders fields for display: well known keys first, then the rest.
func sortFields(t *Tags) {
	for i := 1; i < len(t.Fields); i++ {
		for j := i; j > 0 && fieldLess(t.Fields[j], t.Fields[j-1]); j-- {
			t.Fields[j], t.Fields[j-1] = t.Fields[j-1], t.Fields[j]
		}
	}
}

func fieldLess(a, b Field) bool {
	ra, oka := canonicalRank[a.Key]
	rb, okb := canonicalRank[b.Key]
	switch {
	case oka && okb:
		return ra < rb
	case oka:
		return true
	case okb:
		return false
	}
	return a.Key < b.Key
}

// ---------------------------------------------------------------- format detection

// FormatFromPath returns the display name of the container for a file name.
func FormatFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".mp2", ".mp1":
		return "MP3"
	case ".flac":
		return "FLAC"
	case ".ogg", ".oga":
		return "OGG"
	case ".opus":
		return "OPUS"
	case ".spx":
		return "SPX"
	case ".m4a", ".m4b", ".mp4", ".aac", ".alac":
		return "M4A"
	case ".wma", ".asf":
		return "WMA"
	case ".ape":
		return "APE"
	case ".wav", ".wave":
		return "WAV"
	case ".aif", ".aiff", ".aifc":
		return "AIFF"
	case ".wv":
		return "WV"
	case ".mpc", ".mp+":
		return "MPC"
	}
	return strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), "."))
}

// ---------------------------------------------------------------- read / write

// Read parses every tag of one file.
func Read(path string) (*Tags, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	ext := strings.ToLower(filepath.Ext(path))

	var tags *Tags
	switch {
	case ext == ".ape":
		tags, err = readAPEFormat(f, size)
	case ext == ".wv":
		tags, err = readWV(f, size)
	case ext == ".mpc" || ext == ".mp+":
		tags, err = readMPC(f, size)
	case ext == ".flac":
		tags, err = readFLAC(f, size)
	case ext == ".ogg", ext == ".oga", ext == ".opus", ext == ".spx":
		tags, err = readOgg(f, size)
	case ext == ".m4a", ext == ".m4b", ext == ".mp4", ext == ".aac", ext == ".alac":
		tags, err = readMP4(f, size)
	case ext == ".wma", ext == ".asf":
		tags, err = readASF(f, size)
	case ext == ".wav", ext == ".wave":
		tags, err = readRIFF(f, size)
	case ext == ".aif", ext == ".aiff", ext == ".aifc":
		tags, err = readAIFF(f, size)
	default:
		tags, err = readMP3(f, size, path)
	}
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			// A damaged or exotic file still deserves a row in the list.
			return &Tags{
				Path: path, Name: filepath.Base(path),
				Format: FormatFromPath(path), TagType: "нет",
				Warnings: []string{"структура файла не распознана"},
			}, nil
		}
		return nil, err
	}
	tags.Path = path
	tags.Name = filepath.Base(path)
	if tags.Format == "" {
		tags.Format = FormatFromPath(path)
	}
	if tags.TagType == "" {
		tags.TagType = "нет"
	}
	sortFields(tags)
	return tags, nil
}

// ApplyEdits writes a batch of changes to a single file atomically.
func ApplyEdits(path string, e Edits) error {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case ext == ".ape", ext == ".wv", ext == ".mpc", ext == ".mp+":
		m, err := openAPE(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".flac":
		m, err := openFLAC(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".ogg", ext == ".oga", ext == ".opus", ext == ".spx":
		m, err := openOgg(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".m4a", ext == ".m4b", ext == ".mp4", ext == ".aac", ext == ".alac":
		m, err := openMP4(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".wma", ext == ".asf":
		m, err := openASF(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".wav", ext == ".wave":
		m, err := openRIFF(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	case ext == ".aif", ext == ".aiff", ext == ".aifc":
		m, err := openAIFF(path)
		if err != nil {
			return err
		}
		return m.applyAIFF(e)
	default:
		m, err := openMP3(path)
		if err != nil {
			return err
		}
		return m.apply(e)
	}
}
