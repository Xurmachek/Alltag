package app

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Platform abstracts the few OS specific operations the app needs.
type Platform interface {
	PickFolder(appendMode bool) (string, error)
	ContextMenuEnabled() bool
	SetContextMenu(enabled bool) error
	RevealInExplorer(path string) error
	OpenURL(url string) error
	DataDir() string
	Notify(title, message string)
	Name() string
}

// Server exposes the UI and the JSON API on the loopback interface.
type Server struct {
	Lib      *Library
	Plat     Platform
	Assets   embed.FS
	Settings *Settings
	OnQuit   func()

	mu   sync.Mutex
	ln   net.Listener
	srv  *http.Server
	addr string

	eventMu sync.Mutex
	events  map[chan []byte]struct{}

	// BrowserMode is true when the UI runs in an external browser instead of
	// an embedded WebView.
	BrowserMode bool
	lastPing    time.Time
}

// NewServer builds a server bound to 127.0.0.1 on a free port.
func NewServer(lib *Library, plat Platform, assets embed.FS, settings *Settings) (*Server, error) {
	s := &Server{Lib: lib, Plat: plat, Assets: assets, Settings: settings}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

// URL is the base address of the UI.
func (s *Server) URL() string { return "http://" + s.addr + "/" }

// Start serves in the background.
func (s *Server) Start() error {
	go func() {
		_ = s.srv.Serve(s.ln)
	}()
	return nil
}

// Stop shuts the server down.
func (s *Server) Stop() {
	if s.srv != nil {
		_ = s.srv.Close()
	}
}

func (s *Server) routes(mux *http.ServeMux) {
	s.events = map[chan []byte]struct{}{}
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/", s.handleAPI)
	sub, err := fs.Sub(s.Assets, "assets")
	if err != nil {
		return
	}
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", noCache(files))
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, fmt.Errorf("POST required"))
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	var req map[string]any
	if r.ContentLength != 0 {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	if req == nil {
		req = map[string]any{}
	}
	switch method {
	case "ping":
		s.lastPing = time.Now()
		writeJSON(w, map[string]any{"ok": true, "version": Version,
			"platform": s.Plat.Name(), "browserMode": s.BrowserMode})

	case "tracks":
		writeJSON(w, map[string]any{"tracks": s.Lib.Tracks(), "stats": s.Lib.Stats(),
			"folders": s.Lib.Folders()})

	case "scan":
		added, err := s.Lib.ScanPaths(toStrings(req["paths"]), boolOf(req["append"]))
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.rememberFolders()
		writeJSON(w, map[string]any{"added": added, "tracks": s.Lib.Tracks(),
			"stats": s.Lib.Stats(), "folders": s.Lib.Folders()})
		s.Broadcast("tracks", map[string]any{"tracks": s.Lib.Tracks(),
			"stats": s.Lib.Stats(), "folders": s.Lib.Folders()})

	case "pickFolder":
		path, err := s.Plat.PickFolder(boolOf(req["append"]))
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		if path == "" {
			writeJSON(w, map[string]any{"cancelled": true})
			return
		}
		added, err := s.Lib.ScanPaths([]string{path}, boolOf(req["append"]))
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.rememberFolders()
		writeJSON(w, map[string]any{"path": path, "added": added,
			"tracks": s.Lib.Tracks(), "stats": s.Lib.Stats(), "folders": s.Lib.Folders()})

	case "clear":
		s.Lib.Clear()
		writeJSON(w, map[string]any{"tracks": s.Lib.Tracks(), "stats": s.Lib.Stats()})

	case "merge":
		fields, anyArt, allArt, count := s.Lib.Merge(toInts(req["ids"]))
		writeJSON(w, map[string]any{"fields": fields, "anyArt": anyArt,
			"allArt": allArt, "count": count})

	case "apply":
		set := map[string]string{}
		if m, ok := req["set"].(map[string]any); ok {
			for k, v := range m {
				set[k] = stringOf(v)
			}
		}
		ar := ApplyRequest{
			IDs:           toInts(req["ids"]),
			Set:           set,
			PictureAction: stringOf(req["pictureAction"]),
			PictureMIME:   stringOf(req["pictureMime"]),
			RemoveAllArt:  boolOf(req["removeAllArt"]),
		}
		if b64 := stringOf(req["pictureData"]); b64 != "" {
			if i := strings.IndexByte(b64, ','); i >= 0 {
				b64 = b64[i+1:]
			}
			data, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				fail(w, http.StatusBadRequest, fmt.Errorf("bad image data"))
				return
			}
			ar.PictureData = data
		}
		saved, errs, err := s.Lib.Apply(ar)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"saved": saved, "errors": errs,
			"tracks": s.Lib.Tracks(), "stats": s.Lib.Stats()})

	case "extended":
		ext, err := s.Lib.ExtendedFor(intOf(req["id"]))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, ext)

	case "extendedSave":
		set := map[string]string{}
		if m, ok := req["set"].(map[string]any); ok {
			for k, v := range m {
				set[k] = stringOf(v)
			}
		}
		saved, errs, err := s.Lib.ExtendedSave(toInts(req["ids"]), set, toStrings(req["remove"]))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]any{"saved": saved, "errors": errs,
			"tracks": s.Lib.Tracks()})

	case "artwork":
		id := intOf(req["id"])
		data, mime, err := s.Lib.Artwork(id)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, map[string]any{"mime": mime,
			"data": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})

	case "renamePreview":
		writeJSON(w, map[string]any{"previews": s.Lib.PreviewRename(toInts(req["ids"]),
			stringOf(req["pattern"]))})

	case "parsePreview":
		writeJSON(w, map[string]any{"previews": s.Lib.PreviewParse(toInts(req["ids"]),
			stringOf(req["pattern"]))})

	case "renameApply":
		var previews []RenamePreview
		if arr, ok := req["previews"].([]any); ok {
			for _, item := range arr {
				m, _ := item.(map[string]any)
				previews = append(previews, RenamePreview{
					ID: intOf(m["id"]), Old: stringOf(m["old"]),
					New: stringOf(m["new"]), Changed: boolOf(m["changed"]),
				})
			}
		}
		n, errs := s.Lib.ApplyRename(previews)
		writeJSON(w, map[string]any{"renamed": n, "errors": errs,
			"tracks": s.Lib.Tracks()})

	case "parseNames":
		n, errs := s.Lib.ParseFilename(toInts(req["ids"]), stringOf(req["pattern"]))
		writeJSON(w, map[string]any{"applied": n, "errors": errs,
			"tracks": s.Lib.Tracks()})

	case "contextMenu":
		if _, ok := req["enable"]; ok {
			if err := s.Plat.SetContextMenu(boolOf(req["enable"])); err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, map[string]any{"enabled": s.Plat.ContextMenuEnabled()})

	case "settings":
		if req["settings"] != nil {
			s.Settings.Update(req["settings"])
			if err := s.Settings.Save(); err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
		}
		writeJSON(w, map[string]any{"settings": s.Settings})

	case "reveal":
		if t := s.Lib.Find(intOf(req["id"])); t != nil {
			_ = s.Plat.RevealInExplorer(t.Path)
		}
		writeJSON(w, map[string]any{"ok": true})

	case "quit":
		writeJSON(w, map[string]any{"ok": true})
		if s.OnQuit != nil {
			go func() {
				time.Sleep(120 * time.Millisecond)
				s.OnQuit()
			}()
		}

	default:
		fail(w, http.StatusNotFound, fmt.Errorf("unknown method %q", method))
	}
}

func (s *Server) rememberFolders() {
	folders := s.Lib.Folders()
	if len(folders) > 0 && s.Settings != nil {
		s.Settings.LastFolder = folders[len(folders)-1]
		_ = s.Settings.Save()
	}
}

// ---------------------------------------------------------------- helpers

func toStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok && s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func toInts(v any) []int {
	arr, ok := v.([]any)
	if !ok {
		if f, ok := v.(float64); ok {
			return []int{int(f)}
		}
		return nil
	}
	out := make([]int, 0, len(arr))
	for _, item := range arr {
		out = append(out, intOf(item))
	}
	return out
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		var n int
		fmt.Sscanf(t, "%d", &n)
		return n
	}
	return 0
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// ---------------------------------------------------------------- settings

// Settings are persisted between sessions.
type Settings struct {
	Accent      string `json:"accent"`
	LastFolder  string `json:"lastFolder"`
	SortColumn  string `json:"sortColumn"`
	SortAsc     bool   `json:"sortAsc"`
	RenameMask  string `json:"renameMask"`
	Columns     string `json:"columns"`
	WindowState string `json:"windowState"`

	path string
}

// LoadSettings reads (or creates) the settings file.
func LoadSettings(path string) *Settings {
	s := &Settings{Accent: "#C9A7FF", SortAsc: true,
		RenameMask: "%track% - %artist% - %title%", path: path}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
	}
	s.path = path
	return s
}

// Save writes the settings file.
func (s *Settings) Save() error {
	if s.path == "" {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Update merges a partial settings object.
func (s *Settings) Update(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if a, ok := m["accent"].(string); ok && a != "" {
		s.Accent = a
	}
	if f, ok := m["lastFolder"].(string); ok {
		s.LastFolder = f
	}
	if c, ok := m["sortColumn"].(string); ok {
		s.SortColumn = c
	}
	if asc, ok := m["sortAsc"].(bool); ok {
		s.SortAsc = asc
	}
	if r, ok := m["renameMask"].(string); ok && r != "" {
		s.RenameMask = r
	}
	if c, ok := m["columns"].(string); ok {
		s.Columns = c
	}
	if w, ok := m["windowState"].(string); ok {
		s.WindowState = w
	}
}

// ---------------------------------------------------------------- events (SSE)

// Broadcast pushes an event to every connected UI.
func (s *Server) Broadcast(event string, data any) {
	payload, err := json.Marshal(map[string]any{"event": event, "data": data})
	if err != nil {
		return
	}
	payload = append(payload, '\n')
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	for ch := range s.events {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan []byte, 32)
	s.eventMu.Lock()
	s.events[ch] = struct{}{}
	s.eventMu.Unlock()
	defer func() {
		s.eventMu.Lock()
		delete(s.events, ch)
		s.eventMu.Unlock()
		close(ch)
	}()
	fmt.Fprintf(w, "event: hello\ndata: {\"version\":%q}\n\n", Version)
	flusher.Flush()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case msg := <-ch:
			fmt.Fprintf(w, "event: update\ndata: %s\n", msg)
			flusher.Flush()
		}
	}
}

// WatchBrowserExit terminates the app when the browser UI is gone.
func (s *Server) WatchBrowserExit(onQuit func()) {
	go func() {
		seen := false
		for {
			time.Sleep(time.Second)
			last := s.lastPing
			if !last.IsZero() {
				seen = true
				if time.Since(last) > 15*time.Second {
					if onQuit != nil {
						onQuit()
					}
					return
				}
			} else if seen {
				if onQuit != nil {
					onQuit()
				}
				return
			}
		}
	}()
}
