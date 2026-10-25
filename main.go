package main

// AllTag — a modern, portable MP3tag alternative.
//
// The program is a single executable: the Material Design 3 interface, the
// metadata engine and the native window all live inside this binary.

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"alltag/internal/app"
)

//go:embed assets
var assets embed.FS

func main() {
	paths := collectPaths(os.Args[1:])

	// A second launch forwards its arguments to the running instance.
	if !acquireSingleInstance() {
		sendPathsToRunningInstance(paths)
		return
	}

	dataDir := platformDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	settings := app.LoadSettings(filepath.Join(dataDir, "settings.json"))
	lib := app.NewLibrary()
	plat := newPlatform(dataDir)

	srv, err := app.NewServer(lib, plat, assets, settings)
	if err != nil {
		fatal("AllTag", "Не удалось запустить локальный сервер: "+err.Error())
		return
	}
	if err := srv.Start(); err != nil {
		fatal("AllTag", "Не удалось запустить локальный сервер: "+err.Error())
		return
	}
	defer srv.Stop()

	srv.OnQuit = func() {
		go func() {
			srv.Stop()
			quitApp()
		}()
	}

	// First load: command line paths, otherwise the previous folder.
	if len(paths) == 0 && settings.LastFolder != "" {
		if st, err := os.Stat(settings.LastFolder); err == nil && st.IsDir() {
			paths = []string{settings.LastFolder}
		}
	}
	if len(paths) > 0 {
		_, _ = lib.ScanPaths(paths, false)
	}

	err = runUI(srv.URL(), dataDir, paths,
		func(newPaths []string) {
			added, _ := lib.ScanPaths(newPaths, true)
			if added > 0 {
				srv.Broadcast("tracks", map[string]any{
					"tracks": lib.Tracks(), "stats": lib.Stats(),
					"folders": lib.Folders(),
				})
			}
		},
		func() { srv.Stop() })
	if err != nil {
		fatal("AllTag", "Ошибка запуска окна: "+err.Error())
	}
}

// collectPaths turns raw arguments into existing file/folder paths.
func collectPaths(args []string) []string {
	var out []string
	for _, a := range args {
		a = strings.TrimSpace(strings.Trim(a, `"`))
		if a == "" || strings.HasPrefix(a, "-") || strings.HasPrefix(a, "/") && len(a) < 4 {
			continue
		}
		if _, err := os.Stat(a); err == nil {
			out = append(out, a)
		}
	}
	return out
}
