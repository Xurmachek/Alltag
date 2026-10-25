package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleDir = "/opt/samples"

// stage copies a few real files into a temporary album folder.
func stage(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(sampleDir, name))
		if err != nil {
			t.Skipf("sample %s unavailable: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanAndStats(t *testing.T) {
	dir := stage(t, "silence-44-s.flac", "empty.ogg", "lame_cbr.mp3", "no-tags.m4a",
		"silence-1.wma", "mac-399.ape", "empty.wav")
	lg := NewLibrary()
	added, err := lg.ScanPaths([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if added != 7 {
		t.Fatalf("expected 7 tracks, got %d", added)
	}
	stats := lg.Stats()
	if stats.Count != 7 || stats.Formats != 7 {
		t.Fatalf("unexpected stats %+v", stats)
	}
	if len(lg.Folders()) != 1 || lg.Folders()[0] != dir {
		t.Fatalf("folders: %v", lg.Folders())
	}
	// formats are detected from content, not only the extension
	seen := map[string]bool{}
	for _, tr := range lg.Tracks() {
		seen[tr.Format] = true
		if tr.Path == "" || tr.Name == "" {
			t.Fatalf("incomplete track %+v", tr)
		}
	}
	for _, want := range []string{"FLAC", "OGG", "MP3", "M4A", "WMA", "APE", "WAV"} {
		if !seen[want] {
			t.Errorf("format %s missing (have %v)", want, seen)
		}
	}
	// scanning the same folder twice must not duplicate entries
	again, err := lg.ScanPaths([]string{dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 || lg.Stats().Count != 7 {
		t.Fatalf("rescan added %d, count %d", again, lg.Stats().Count)
	}
}

func TestMergeDetectsMixedValues(t *testing.T) {
	dir := stage(t, "silence-44-s.flac", "empty.ogg")
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	ids := idsOf(lg)
	if _, _, _, n := lg.Merge(ids); n != 2 {
		t.Fatalf("merge count %d", n)
	}
	if _, _, err := lg.Apply(ApplyRequest{
		IDs: ids,
		Set: map[string]string{"TITLE": "Same", "ARTIST": "Same", "ALBUM": "Album"},
	}); err != nil {
		t.Fatal(err)
	}
	fields, _, _, _ := lg.Merge(ids)
	if fields["TITLE"].Mixed || fields["TITLE"].Value != "Same" {
		t.Fatalf("title should be uniform: %+v", fields["TITLE"])
	}
	// change one file so the value becomes mixed again
	if _, _, err := lg.Apply(ApplyRequest{
		IDs: ids[:1], Set: map[string]string{"TITLE": "Different"},
	}); err != nil {
		t.Fatal(err)
	}
	fields, _, _, _ = lg.Merge(ids)
	if !fields["TITLE"].Mixed {
		t.Fatalf("title should be mixed: %+v", fields["TITLE"])
	}
}

func TestBatchLeaveUnchanged(t *testing.T) {
	dir := stage(t, "silence-44-s.flac", "mac-399.ape")
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	ids := idsOf(lg)
	base := map[string]string{
		"TITLE": "Title", "ARTIST": "Artist", "ALBUM": "Album",
		"YEAR": "1999", "TRACK": "4", "GENRE": "Rock", "COMMENT": "keep me",
	}
	if _, errs, err := lg.Apply(ApplyRequest{IDs: ids, Set: base}); err != nil || len(errs) > 0 {
		t.Fatalf("seed apply: %v %v", err, errs)
	}
	// edit a single field across the selection
	if _, errs, err := lg.Apply(ApplyRequest{IDs: ids, Set: map[string]string{"TITLE": "New"}}); err != nil || len(errs) > 0 {
		t.Fatalf("edit apply: %v %v", err, errs)
	}
	for _, tr := range lg.Tracks() {
		if tr.Title != "New" {
			t.Errorf("%s: title %q", tr.Name, tr.Title)
		}
		if tr.Artist != "Artist" || tr.Album != "Album" || tr.Year != "1999" ||
			tr.Track != "4" || tr.Genre != "Rock" || tr.Comment != "keep me" {
			t.Errorf("%s: untouched fields changed: %+v", tr.Name, tr)
		}
	}
	// an empty value deletes the field
	if _, _, err := lg.Apply(ApplyRequest{IDs: ids, Set: map[string]string{"COMMENT": ""}}); err != nil {
		t.Fatal(err)
	}
	for _, tr := range lg.Tracks() {
		if tr.Comment != "" {
			t.Errorf("%s: comment not removed (%q)", tr.Name, tr.Comment)
		}
	}
}

func TestExtendedTags(t *testing.T) {
	dir := stage(t, "silence-44-s.flac")
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	id := lg.Tracks()[0].ID
	if _, _, err := lg.Apply(ApplyRequest{IDs: []int{id}, Set: map[string]string{"TITLE": "T"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lg.ExtendedSave([]int{id}, map[string]string{"MOOD": "calm", "ISRC": "ABC123"}, nil); err != nil {
		t.Fatal(err)
	}
	ext, err := lg.ExtendedFor(id)
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{}
	for _, f := range ext.Fields {
		vals[f.Key] = f.Value
	}
	if vals["MOOD"] != "calm" || vals["ISRC"] != "ABC123" || vals["TITLE"] != "T" {
		t.Fatalf("extended fields: %v", vals)
	}
	if _, _, err := lg.ExtendedSave([]int{id}, nil, []string{"MOOD"}); err != nil {
		t.Fatal(err)
	}
	ext, _ = lg.ExtendedFor(id)
	for _, f := range ext.Fields {
		if f.Key == "MOOD" {
			t.Fatalf("MOOD was not removed: %v", ext.Fields)
		}
	}
}

func TestArtworkRoundTrip(t *testing.T) {
	dir := stage(t, "no-tags.m4a", "silence-1.wma")
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
		0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	ids := idsOf(lg)
	if _, errs, err := lg.Apply(ApplyRequest{
		IDs: ids, Set: map[string]string{"TITLE": "with art"},
		PictureAction: "set", PictureData: png, PictureMIME: "image/png",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("set art: %v %v", err, errs)
	}
	for _, tr := range lg.Tracks() {
		if !tr.HasArt {
			t.Fatalf("%s: artwork flag not set", tr.Name)
		}
		data, mime, err := lg.Artwork(tr.ID)
		if err != nil {
			t.Fatalf("%s: %v", tr.Name, err)
		}
		if len(data) != len(png) || mime != "image/png" {
			t.Fatalf("%s: art %d bytes mime %s", tr.Name, len(data), mime)
		}
	}
	if _, errs, err := lg.Apply(ApplyRequest{IDs: ids, RemoveAllArt: true}); err != nil || len(errs) > 0 {
		t.Fatalf("remove art: %v %v", err, errs)
	}
	for _, tr := range lg.Tracks() {
		if tr.HasArt {
			t.Fatalf("%s: art still present", tr.Name)
		}
	}
}

func TestRenameRoundTrip(t *testing.T) {
	dir := stage(t, "silence-44-s.flac", "empty.ogg")
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	ids := idsOf(lg)
	if _, _, err := lg.Apply(ApplyRequest{IDs: ids, Set: map[string]string{
		"ARTIST": "Nina", "TITLE": "Song", "TRACK": "7",
	}}); err != nil {
		t.Fatal(err)
	}
	pattern := "%track% - %artist% - %title%"
	previews := lg.PreviewRename(ids, pattern)
	if len(previews) != 2 {
		t.Fatalf("previews: %d", len(previews))
	}
	for _, p := range previews {
		if !p.Changed || p.Error != "" {
			t.Fatalf("unexpected preview %+v", p)
		}
		if !strings.HasPrefix(p.New, "07 - Nina - Song") &&
			!strings.HasPrefix(p.New, "7 - Nina - Song") {
			t.Fatalf("bad target name %q", p.New)
		}
		if filepath.Ext(p.New) != filepath.Ext(p.Old) {
			t.Fatalf("extension lost: %q", p.New)
		}
	}
	// previews must not touch the disk
	for _, p := range previews {
		if _, err := os.Stat(filepath.Join(dir, p.Old)); err != nil {
			t.Fatalf("preview renamed files already: %v", err)
		}
	}
	n, errs := lg.ApplyRename(previews)
	if n != 2 || len(errs) > 0 {
		t.Fatalf("rename: %d %v", n, errs)
	}
	for _, p := range previews {
		if _, err := os.Stat(filepath.Join(dir, p.New)); err != nil {
			t.Fatalf("renamed file missing: %v", err)
		}
	}
	for _, tr := range lg.Tracks() {
		if !strings.HasPrefix(tr.Name, "07 - Nina - Song") && !strings.HasPrefix(tr.Name, "7 - Nina - Song") {
			t.Fatalf("library not refreshed: %q", tr.Name)
		}
	}
	// the tags survived the rename
	if lg.Tracks()[0].Artist != "Nina" {
		t.Fatalf("tags lost after rename")
	}
}

func TestFilenameToTag(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join(sampleDir, "silence-44-s.flac"))
	if err != nil {
		t.Skip("sample unavailable")
	}
	target := filepath.Join(dir, "03 - Portishead - Roads.flac")
	if err := os.WriteFile(target, src, 0o644); err != nil {
		t.Fatal(err)
	}
	lg := NewLibrary()
	if _, err := lg.ScanPaths([]string{dir}, false); err != nil {
		t.Fatal(err)
	}
	id := lg.Tracks()[0].ID
	pattern := "%track% - %artist% - %title%"
	previews := lg.PreviewParse([]int{id}, pattern)
	if len(previews) != 1 || !previews[0].Matched {
		t.Fatalf("preview did not match: %+v", previews)
	}
	if previews[0].Fields["ARTIST"] != "Portishead" || previews[0].Fields["TITLE"] != "Roads" {
		t.Fatalf("parsed fields: %v", previews[0].Fields)
	}
	n, errs := lg.ParseFilename([]int{id}, pattern)
	if n != 1 || len(errs) > 0 {
		t.Fatalf("parse: %d %v", n, errs)
	}
	tr := lg.Tracks()[0]
	track := strings.TrimLeft(tr.Track, "0")
	if tr.Artist != "Portishead" || tr.Title != "Roads" || track != "3" {
		t.Fatalf("tags not applied: %+v", tr)
	}
}

func idsOf(lg *Library) []int {
	var ids []int
	for _, tr := range lg.Tracks() {
		ids = append(ids, tr.ID)
	}
	return ids
}
