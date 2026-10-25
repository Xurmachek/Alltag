// Package app implements AllTag's document model: the track list, scanning,
// batch editing, the tag⇄filename converter and the extended tag editor.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"alltag/internal/audiometa"
)

// Track is the flattened view of one file used by the UI table.
type Track struct {
	ID       int     `json:"id"`
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Dir      string  `json:"dir"`
	Format   string  `json:"format"`
	TagType  string  `json:"tagType"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Year     string  `json:"year"`
	Track    string  `json:"track"`
	Genre    string  `json:"genre"`
	Comment  string  `json:"comment"`
	Bitrate  int     `json:"bitrate"`
	Duration float64 `json:"duration"`
	Size     int64   `json:"size"`
	HasArt   bool    `json:"hasArt"`
	HasTags  bool    `json:"hasTags"`
	Modified int64   `json:"modified"`
	Error    string  `json:"error,omitempty"`

	full *audiometa.Tags
}

// Library is the open document.
type Library struct {
	mu      sync.Mutex
	tracks  []*Track
	nextID  int
	folders []string
}

// NewLibrary creates an empty library.
func NewLibrary() *Library { return &Library{nextID: 1} }

// Folders returns the folders that were loaded.
func (l *Library) Folders() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.folders...)
}

// Tracks returns a snapshot of the current list.
func (l *Library) Tracks() []*Track {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*Track{}, l.tracks...)
}

// Clear empties the document.
func (l *Library) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tracks = nil
	l.folders = nil
}

// ScanPaths loads files and folders, appending or replacing the list.
func (l *Library) ScanPaths(paths []string, append_ bool) (added int, err error) {
	var files []string
	var dirs []string
	for _, p := range paths {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			dirs = append(dirs, p)
			found, err := collectAudioFiles(p)
			if err != nil {
				continue
			}
			files = append(files, found...)
		} else if audiometa.SupportedExt(filepath.Ext(p)) {
			files = append(files, p)
		}
	}
	if !append_ {
		l.Clear()
	}
	l.mu.Lock()
	known := map[string]bool{}
	for _, t := range l.tracks {
		known[strings.ToLower(t.Path)] = true
	}
	l.mu.Unlock()
	for _, d := range dirs {
		l.mu.Lock()
		dup := false
		for _, f := range l.folders {
			if strings.EqualFold(f, d) {
				dup = true
			}
		}
		if !dup {
			l.folders = append(l.folders, d)
		}
		l.mu.Unlock()
	}
	sort.Strings(files)
	for _, f := range files {
		if known[strings.ToLower(f)] {
			continue
		}
		known[strings.ToLower(f)] = true
		l.mu.Lock()
		id := l.nextID
		l.nextID++
		l.mu.Unlock()
		tr := loadTrack(id, f)
		l.mu.Lock()
		l.tracks = append(l.tracks, tr)
		l.mu.Unlock()
		added++
	}
	return added, nil
}

func collectAudioFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if audiometa.SupportedExt(filepath.Ext(path)) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

func loadTrack(id int, path string) *Track {
	st, _ := os.Stat(path)
	tr := &Track{ID: id, Path: path, Name: filepath.Base(path),
		Dir: filepath.Dir(path), Format: audiometa.FormatFromPath(path)}
	if st != nil {
		tr.Size = st.Size()
		tr.Modified = st.ModTime().Unix()
	}
	tags, err := audiometa.Read(path)
	if err != nil {
		tr.Error = err.Error()
		return tr
	}
	applyTags(tr, tags)
	return tr
}

func applyTags(tr *Track, tags *audiometa.Tags) {
	tr.full = tags
	tr.Format = tags.Format
	tr.TagType = tags.TagType
	tr.Title = tags.Get("TITLE")
	tr.Artist = tags.Get("ARTIST")
	tr.Album = tags.Get("ALBUM")
	tr.Year = tags.Get("YEAR")
	tr.Track = tags.Get("TRACK")
	tr.Genre = tags.Get("GENRE")
	tr.Comment = tags.Get("COMMENT")
	tr.Bitrate = tags.Info.Bitrate
	tr.Duration = tags.Info.Duration
	tr.HasArt = len(tags.Pictures) > 0
	tr.HasTags = len(tags.Fields) > 0
	tr.Error = ""
}

// Reload re-reads a single track from disk.
func (l *Library) Reload(id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range l.tracks {
		if t.ID == id {
			fresh := loadTrack(id, t.Path)
			*t = *fresh
			return
		}
	}
}

// Selection is a list of track ids.
type Selection struct {
	IDs []int `json:"ids"`
}

// Selected resolves ids to tracks (all tracks when ids is empty).
func (l *Library) Selected(ids []int) []*Track {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(ids) == 0 {
		return append([]*Track{}, l.tracks...)
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*Track
	for _, t := range l.tracks {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// editorFields are the fields shown in the left panel.
var editorFields = []string{"TITLE", "ARTIST", "ALBUM", "YEAR", "TRACK",
	"GENRE", "COMMENT", "ALBUMARTIST", "COMPOSER", "DISCNUMBER", "BPM", "LYRICS"}

// MergedValue is the state of one editor field across the selection.
type MergedValue struct {
	Value string `json:"value"`
	Mixed bool   `json:"mixed"`
	Count int    `json:"count"`
}

// Merge returns the editor state for the current selection.
func (l *Library) Merge(ids []int) (map[string]MergedValue, bool, bool, int) {
	tracks := l.Selected(ids)
	out := map[string]MergedValue{}
	anyArt, allArt := false, len(tracks) > 0
	for _, k := range editorFields {
		first := ""
		mixed := false
		for i, t := range tracks {
			v := ""
			if t.full != nil {
				v = t.full.Get(k)
			}
			if i == 0 {
				first = v
			} else if v != first {
				mixed = true
			}
		}
		out[k] = MergedValue{Value: first, Mixed: mixed, Count: len(tracks)}
	}
	for _, t := range tracks {
		if t.HasArt {
			anyArt = true
		} else {
			allArt = false
		}
	}
	return out, anyArt, allArt, len(tracks)
}

// ApplyRequest is the payload of a save operation.
type ApplyRequest struct {
	IDs           []int             `json:"ids"`
	Set           map[string]string `json:"set"`
	PictureAction string            `json:"pictureAction"`
	PictureData   []byte            `json:"pictureData"`
	PictureMIME   string            `json:"pictureMime"`
	RemoveAllArt  bool              `json:"removeAllArt"`
}

// Apply writes the edits to every selected file.
func (l *Library) Apply(req ApplyRequest) (int, []string, error) {
	tracks := l.Selected(req.IDs)
	if len(tracks) == 0 {
		return 0, nil, errors.New("no files selected")
	}
	edits := audiometa.Edits{
		Set:               map[string]string{},
		PictureAction:     req.PictureAction,
		Picture:           req.PictureData,
		PictureMIME:       req.PictureMIME,
		RemoveAllPictures: req.RemoveAllArt,
	}
	for k, v := range req.Set {
		key := audiometa.CanonicalKey(k)
		if key == "" {
			continue
		}
		edits.Set[key] = v
	}
	if len(edits.Set) == 0 && req.PictureAction == "" && !req.RemoveAllArt {
		return 0, nil, errors.New("nothing to save")
	}
	var errs []string
	ok := 0
	for _, t := range tracks {
		if err := audiometa.ApplyEdits(t.Path, edits); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.Name, err))
			continue
		}
		ok++
	}
	// refresh the model from disk so the table shows exactly what was written
	for _, t := range tracks {
		fresh, err := audiometa.Read(t.Path)
		if err != nil {
			continue
		}
		l.mu.Lock()
		applyTags(t, fresh)
		l.mu.Unlock()
	}
	return ok, errs, nil
}

// ExtendedField is one key/value pair in the extended editor.
type ExtendedField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Tech  bool   `json:"tech"`
}

// Extended returns every tag of one file.
type Extended struct {
	ID       int             `json:"id"`
	Path     string          `json:"path"`
	Name     string          `json:"name"`
	Format   string          `json:"format"`
	TagType  string          `json:"tagType"`
	Fields   []ExtendedField `json:"fields"`
	Pictures []PictureInfo   `json:"pictures"`
}

// PictureInfo describes an embedded image.
type PictureInfo struct {
	Index int    `json:"index"`
	MIME  string `json:"mime"`
	Kind  int    `json:"kind"`
	Size  int    `json:"size"`
	Desc  string `json:"desc"`
}

// ExtendedFor reads the full tag set of a track from disk.
func (l *Library) ExtendedFor(id int) (*Extended, error) {
	tracks := l.Selected([]int{id})
	if len(tracks) == 0 {
		return nil, errors.New("file not found in list")
	}
	tags, err := audiometa.Read(tracks[0].Path)
	if err != nil {
		return nil, err
	}
	out := &Extended{ID: id, Path: tracks[0].Path, Name: tracks[0].Name,
		Format: tags.Format, TagType: tags.TagType}
	for _, f := range tags.Fields {
		out.Fields = append(out.Fields, ExtendedField{Key: f.Key, Value: f.Value})
	}
	for i, p := range tags.Pictures {
		out.Pictures = append(out.Pictures, PictureInfo{Index: i, MIME: p.MIME,
			Kind: p.Kind, Size: len(p.Data), Desc: p.Desc})
	}
	return out, nil
}

// ExtendedSave writes arbitrary key/value changes (including deletions).
func (l *Library) ExtendedSave(ids []int, set map[string]string, remove []string) (int, []string, error) {
	tracks := l.Selected(ids)
	if len(tracks) == 0 {
		return 0, nil, errors.New("no files selected")
	}
	e := audiometa.Edits{Set: map[string]string{}}
	for k, v := range set {
		key := audiometa.CanonicalKey(k)
		if key == "" {
			continue
		}
		e.Set[key] = v
	}
	for _, k := range remove {
		key := audiometa.CanonicalKey(k)
		if key != "" {
			e.Remove = append(e.Remove, key)
		}
	}
	var errs []string
	ok := 0
	for _, t := range tracks {
		if err := audiometa.ApplyEdits(t.Path, e); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.Name, err))
			continue
		}
		ok++
		if fresh, err := audiometa.Read(t.Path); err == nil {
			l.mu.Lock()
			applyTags(t, fresh)
			l.mu.Unlock()
		}
	}
	return ok, errs, nil
}

// Artwork returns the front cover of a track.
func (l *Library) Artwork(id int) ([]byte, string, error) {
	tracks := l.Selected([]int{id})
	if len(tracks) == 0 {
		return nil, "", errors.New("file not found")
	}
	tags, err := audiometa.Read(tracks[0].Path)
	if err != nil {
		return nil, "", err
	}
	if len(tags.Pictures) == 0 {
		return nil, "", errors.New("no artwork")
	}
	pic := tags.Pictures[0]
	for _, p := range tags.Pictures {
		if p.Kind == 3 {
			pic = p
			break
		}
	}
	return pic.Data, pic.MIME, nil
}

// ---------------------------------------------------------------- converter

// PatternValue resolves %macros% in a filename or tag pattern.
func PatternValue(t *Track, pattern string) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '%' {
			sb.WriteByte(c)
			continue
		}
		end := strings.IndexByte(pattern[i+1:], '%')
		if end < 0 {
			sb.WriteByte(c)
			continue
		}
		macro := strings.ToLower(pattern[i+1 : i+1+end])
		sb.WriteString(macroValue(t, macro))
		i += end + 1
	}
	return sb.String()
}

func macroValue(t *Track, macro string) string {
	get := func(k string) string {
		if t.full == nil {
			return ""
		}
		return t.full.Get(k)
	}
	switch macro {
	case "title":
		return get("TITLE")
	case "artist":
		return get("ARTIST")
	case "album":
		return get("ALBUM")
	case "albumartist":
		return get("ALBUMARTIST")
	case "track":
		return get("TRACK")
	case "year", "date":
		return get("YEAR")
	case "genre":
		return get("GENRE")
	case "comment":
		return get("COMMENT")
	case "composer":
		return get("COMPOSER")
	case "disc", "discnumber":
		return get("DISCNUMBER")
	case "filename":
		return strings.TrimSuffix(t.Name, filepath.Ext(t.Name))
	case "ext":
		return strings.TrimPrefix(filepath.Ext(t.Name), ".")
	case "folder":
		return filepath.Base(t.Dir)
	case "_filename":
		return t.Name
	}
	return ""
}

// RenamePreview is one planned rename.
type RenamePreview struct {
	ID      int    `json:"id"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Changed bool   `json:"changed"`
	Error   string `json:"error,omitempty"`
}

// PreviewRename builds "tag -> filename" proposals.
func (l *Library) PreviewRename(ids []int, pattern string) []RenamePreview {
	tracks := l.Selected(ids)
	var out []RenamePreview
	used := map[string]int{}
	for _, t := range tracks {
		name := sanitizeFilename(PatternValue(t, pattern))
		ext := filepath.Ext(t.Name)
		if !strings.Contains(strings.ToLower(pattern), "%ext%") {
			name += ext
		}
		if strings.TrimSpace(name) == "" {
			out = append(out, RenamePreview{ID: t.ID, Old: t.Name, Error: "empty name"})
			continue
		}
		cand := name
		for i := 2; used[strings.ToLower(filepath.Join(t.Dir, cand))] > 0; i++ {
			base := strings.TrimSuffix(name, filepath.Ext(name))
			cand = fmt.Sprintf("%s (%d)%s", base, i, filepath.Ext(name))
		}
		used[strings.ToLower(filepath.Join(t.Dir, cand))]++
		target := filepath.Join(t.Dir, cand)
		out = append(out, RenamePreview{ID: t.ID, Old: t.Name, New: cand,
			Changed: cand != t.Name && target != t.Path})
	}
	return out
}

// ParsePreview shows what "filename -> tag" would extract.
type ParsePreview struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Fields  map[string]string `json:"fields"`
	Matched bool              `json:"matched"`
}

// PreviewParse runs the mask in preview mode.
func (l *Library) PreviewParse(ids []int, pattern string) []ParsePreview {
	tracks := l.Selected(ids)
	out := make([]ParsePreview, 0, len(tracks))
	for _, t := range tracks {
		pp := ParsePreview{ID: t.ID, Name: t.Name, Fields: map[string]string{}}
		vals, ok := matchPattern(strings.TrimSuffix(t.Name, filepath.Ext(t.Name)), pattern)
		if !ok {
			out = append(out, pp)
			continue
		}
		pp.Matched = true
		for macro, v := range vals {
			key := audiometa.CanonicalKey(macro)
			if key == "" {
				continue
			}
			v = strings.TrimSpace(v)
			if macro == "track" {
				v = digitsOnly(v)
			}
			if v != "" {
				pp.Fields[key] = v
			}
		}
		out = append(out, pp)
	}
	return out
}

// ApplyRename performs the renames on disk.
func (l *Library) ApplyRename(previews []RenamePreview) (int, []string) {
	ok := 0
	var errs []string
	for _, p := range previews {
		if !p.Changed || p.Error != "" {
			continue
		}
		tracks := l.Selected([]int{p.ID})
		if len(tracks) == 0 {
			continue
		}
		t := tracks[0]
		target := filepath.Join(t.Dir, p.New)
		if _, err := os.Stat(target); err == nil && !strings.EqualFold(target, t.Path) {
			errs = append(errs, fmt.Sprintf("%s: target already exists", p.New))
			continue
		}
		if err := os.Rename(t.Path, target); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Old, err))
			continue
		}
		l.mu.Lock()
		t.Path = target
		t.Name = filepath.Base(target)
		t.Dir = filepath.Dir(target)
		l.mu.Unlock()
		ok++
	}
	return ok, errs
}

func sanitizeFilename(s string) string {
	repl := map[rune]rune{'\\': '-', '/': '-', ':': '-', '*': '_', '?': '_',
		'"': '\'', '<': '(', '>': ')', '|': '-'}
	var sb strings.Builder
	for _, r := range s {
		if rr, ok := repl[r]; ok {
			sb.WriteRune(rr)
			continue
		}
		if r < 32 {
			continue
		}
		sb.WriteRune(r)
	}
	out := strings.TrimSpace(sb.String())
	out = strings.TrimRight(out, ". ")
	if len(out) > 180 {
		out = out[:180]
	}
	return out
}

// ParseFilename applies a "filename -> tag" mask to the given tracks.
func (l *Library) ParseFilename(ids []int, pattern string) (int, []string) {
	tracks := l.Selected(ids)
	ok := 0
	var errs []string
	for _, t := range tracks {
		vals, ok2 := matchPattern(strings.TrimSuffix(t.Name, filepath.Ext(t.Name)), pattern)
		if !ok2 {
			errs = append(errs, t.Name+": does not match the mask")
			continue
		}
		e := audiometa.Edits{Set: map[string]string{}}
		for macro, v := range vals {
			key := audiometa.CanonicalKey(macro)
			val := strings.TrimSpace(v)
			if macro == "track" {
				val = digitsOnly(val)
			}
			if key != "" && val != "" {
				e.Set[key] = val
			}
		}
		if len(e.Set) == 0 {
			continue
		}
		if err := audiometa.ApplyEdits(t.Path, e); err != nil {
			errs = append(errs, t.Name+": "+err.Error())
			continue
		}
		if fresh, err := audiometa.Read(t.Path); err == nil {
			l.mu.Lock()
			applyTags(t, fresh)
			l.mu.Unlock()
		}
		ok++
	}
	return ok, errs
}

// matchPattern matches a mask like "%track% - %artist% - %title%" against a
// bare file name and returns the captured macro values.
func matchPattern(name, pattern string) (map[string]string, bool) {
	out := map[string]string{}
	// split both sides into literal / macro tokens
	var tokens []struct {
		macro string
		text  string
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '%' {
			if end := strings.IndexByte(pattern[i+1:], '%'); end >= 0 {
				tokens = append(tokens, struct {
					macro string
					text  string
				}{macro: strings.ToLower(pattern[i+1 : i+1+end])})
				i += end + 1
				continue
			}
		}
		if len(tokens) > 0 && tokens[len(tokens)-1].macro == "" {
			tokens[len(tokens)-1].text += string(pattern[i])
		} else {
			tokens = append(tokens, struct {
				macro string
				text  string
			}{text: string(pattern[i])})
		}
	}
	pos := 0
	for i, tok := range tokens {
		if tok.macro == "" {
			if !strings.HasPrefix(strings.ToLower(name[pos:]), strings.ToLower(tok.text)) {
				return nil, false
			}
			pos += len(tok.text)
			continue
		}
		// find the next literal to bound this capture
		next := ""
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].macro == "" && tokens[j].text != "" {
				next = tokens[j].text
				break
			}
		}
		if next == "" {
			out[tok.macro] = name[pos:]
			pos = len(name)
			continue
		}
		idx := strings.Index(strings.ToLower(name[pos:]), strings.ToLower(next))
		if idx < 0 {
			return nil, false
		}
		out[tok.macro] = name[pos : pos+idx]
		pos += idx
	}
	return out, true
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

// Stats summarises the list for the status bar.
type Stats struct {
	Count    int     `json:"count"`
	Duration float64 `json:"duration"`
	Size     int64   `json:"size"`
	Formats  int     `json:"formats"`
}

// Stats computes aggregate numbers.
func (l *Library) Stats() Stats {
	tracks := l.Tracks()
	st := Stats{Count: len(tracks)}
	seen := map[string]bool{}
	for _, t := range tracks {
		st.Duration += t.Duration
		st.Size += t.Size
		seen[t.Format] = true
	}
	st.Formats = len(seen)
	return st
}

// Find returns one track by id.
func (l *Library) Find(id int) *Track {
	for _, t := range l.Tracks() {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Version of the application, injected at build time.
var Version = "1.0.0"

func init() {
	if v := os.Getenv("ALLTAG_VERSION"); v != "" {
		Version = v
	}
}

var _ = strconv.Itoa
