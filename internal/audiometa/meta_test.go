package audiometa

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDir = "/opt/samples"

func copySample(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join(sampleDir, name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("sample %s not available", name)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// audioFingerprint hashes the media payload of a file, ignoring metadata so we
// can prove that saving tags never touches the audio stream.
func audioFingerprint(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	var payload []byte
	switch ext {
	case "mp3":
		start := 0
		if len(data) > 10 && string(data[:3]) == "ID3" {
			start = 10 + synchsafe(data[6:10])
		}
		end := len(data)
		if end >= 128 && string(data[end-128:end-125]) == "TAG" {
			end -= 128
		}
		if at, st := findAPETag(data[:end], end); at != nil {
			end = st
		}
		payload = data[start:end]
	case "flac":
		pos := 4
		for pos+4 <= len(data) {
			last := data[pos]&0x80 != 0
			l := be24(data[pos+1 : pos+4])
			pos += 4 + l
			if last {
				break
			}
		}
		payload = data[pos:]
	case "ogg":
		st, err := parseOggPages(data)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range st.packets {
			if i < 3 {
				continue
			}
			payload = append(payload, p.data...)
		}
	case "m4a":
		roots, err := parseAtoms(data, 0)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		for _, a := range roots {
			if a.typ == "mdat" {
				buf.Write(a.data)
			}
		}
		payload = buf.Bytes()
	case "wma":
		m, err := parseASF(data)
		if err != nil {
			t.Fatal(err)
		}
		payload = m.tail
	case "ape":
		end := len(data)
		if at, st := findAPETag(data, end); at != nil {
			end = st
		}
		payload = data[:end]
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x:%d", sum[:8], len(payload))
}

func TestReadCorpus(t *testing.T) {
	entries, err := os.ReadDir(sampleDir)
	if err != nil {
		t.Skip("sample corpus missing")
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !SupportedExt(ext) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			tags, err := Read(filepath.Join(sampleDir, name))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if tags.Format == "" || tags.TagType == "" {
				t.Fatalf("missing format/tagtype: %+v", tags)
			}
			if tags.Info.Duration < 0 || tags.Info.Duration > 1e6 {
				t.Fatalf("implausible duration %v", tags.Info.Duration)
			}
			if tags.Info.SampleRate > 0 && (tags.Info.SampleRate < 1000 || tags.Info.SampleRate > 400000) {
				t.Fatalf("implausible sample rate %d", tags.Info.SampleRate)
			}
		})
	}
}

func TestWriteRoundTrip(t *testing.T) {
	cases := []string{"empty.mp3", "silence-44-s.flac", "empty.ogg", "no-tags.m4a",
		"silence-1.wma", "mac-399.ape", "no-tags.flac"}
	want := map[string]string{
		"TITLE":                 "Ünïcode Tïtle ✓",
		"ARTIST":                "Artist One",
		"ALBUM":                 "Album X",
		"YEAR":                  "2004",
		"TRACK":                 "7/12",
		"GENRE":                 "Electronic",
		"COMMENT":               "written by AllTag",
		"ALBUMARTIST":           "Various",
		"COMPOSER":              "Comp Oser",
		"BPM":                   "128",
		"REPLAYGAIN_TRACK_GAIN": "-7.25 dB",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			path := copySample(t, name)
			before := audioFingerprint(t, path)
			set := map[string]string{}
			for k, v := range want {
				set[k] = v
			}
			if err := ApplyEdits(path, Edits{Set: set}); err != nil {
				t.Fatalf("apply: %v", err)
			}
			after := audioFingerprint(t, path)
			if before != after {
				t.Fatalf("audio payload changed: %s != %s", before, after)
			}
			tags, err := Read(path)
			if err != nil {
				t.Fatalf("reread: %v", err)
			}
			for k, v := range want {
				got := tags.Get(k)
				if k == "BPM" && name == "mac-399.ape" {
					continue
				}
				if got != v {
					t.Errorf("field %s: got %q want %q (fields=%v)", k, got, v, tags.Fields)
				}
			}
			// second save must be stable
			if err := ApplyEdits(path, Edits{Set: map[string]string{"ARTIST": "Second"}}); err != nil {
				t.Fatalf("apply 2: %v", err)
			}
			tags, _ = Read(path)
			if tags.Get("ARTIST") != "Second" || tags.Get("ALBUM") != "Album X" {
				t.Fatalf("second write lost data: %v", tags.Fields)
			}
			if audioFingerprint(t, path) != before {
				t.Fatalf("audio payload changed on second save")
			}
		})
	}
}

var pngOnePixel = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestPictures(t *testing.T) {
	cases := []string{"empty.mp3", "silence-44-s.flac", "empty.ogg", "no-tags.m4a", "silence-1.wma", "mac-399.ape"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			path := copySample(t, name)
			before := audioFingerprint(t, path)
			if err := ApplyEdits(path, Edits{PictureAction: "set", Picture: pngOnePixel, PictureMIME: "image/png"}); err != nil {
				t.Fatalf("set picture: %v", err)
			}
			tags, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(tags.Pictures) != 1 {
				t.Fatalf("expected 1 picture, got %d", len(tags.Pictures))
			}
			if !bytes.Equal(tags.Pictures[0].Data, pngOnePixel) {
				t.Fatalf("picture payload corrupted (%d bytes)", len(tags.Pictures[0].Data))
			}
			if audioFingerprint(t, path) != before {
				t.Fatalf("audio payload changed after picture write")
			}
			if err := ApplyEdits(path, Edits{RemoveAllPictures: true}); err != nil {
				t.Fatalf("remove picture: %v", err)
			}
			tags, _ = Read(path)
			if len(tags.Pictures) != 0 {
				t.Fatalf("expected pictures to be removed, got %d", len(tags.Pictures))
			}
		})
	}
}

func TestDeleteFields(t *testing.T) {
	path := copySample(t, "silence-44-s.flac")
	if err := ApplyEdits(path, Edits{Set: map[string]string{"MOOD": "calm"}}); err != nil {
		t.Fatal(err)
	}
	if tags, _ := Read(path); tags.Get("MOOD") != "calm" {
		t.Fatalf("custom field not written: %v", tags.Fields)
	}
	if err := ApplyEdits(path, Edits{Set: map[string]string{"MOOD": ""}}); err != nil {
		t.Fatal(err)
	}
	if tags, _ := Read(path); tags.Get("MOOD") != "" {
		t.Fatalf("custom field not removed: %v", tags.Fields)
	}
}

func TestMP4ChunkOffsets(t *testing.T) {
	// A file whose moov precedes mdat must keep playable chunk offsets after
	// the metadata grows.
	path := copySample(t, "has-tags.m4a")
	for i := 0; i < 3; i++ {
		if err := ApplyEdits(path, Edits{Set: map[string]string{
			"TITLE": strings.Repeat("long title ", 40+i),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	roots, _ := parseAtoms(data, 0)
	var mdatStart int
	off := 0
	for _, a := range roots {
		if a.typ == "mdat" {
			mdatStart = off + 8
			break
		}
		off += len(a.encode())
	}
	m, err := parseMP4(data)
	if err != nil {
		t.Fatal(err)
	}
	stco := m.moov.find("stco")
	if stco == nil {
		t.Skip("no stco")
	}
	n := int(be32(stco.data[4:8]))
	for i := 0; i < n; i++ {
		v := int(be32(stco.data[8+i*4 : 12+i*4]))
		if v < mdatStart || v > len(data) {
			t.Fatalf("chunk offset %d outside mdat (%d..%d)", v, mdatStart, len(data))
		}
	}
}
