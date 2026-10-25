//go:build !windows

package main

import (
	"alltag/internal/app"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func newPlatform(dataDir string) app.Platform { return &devPlatform{dir: dataDir} }

func platformDataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return dir + "/AllTag"
}

func acquireSingleInstance() bool               { return true }
func sendPathsToRunningInstance(paths []string) {}
func quitApp()                                  { os.Exit(0) }
func fatal(title, msg string)                   { fmt.Fprintf(os.Stderr, "%s: %s\n", title, msg); os.Exit(1) }

func runUI(url, dataDir string, paths []string, onPaths func([]string), onQuit func()) error {
	fmt.Printf("AllTag dev server: %s\n", url)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	return nil
}

type devPlatform struct{ dir string }

func (d *devPlatform) Name() string                    { return "linux" }
func (d *devPlatform) DataDir() string                 { return d.dir }
func (d *devPlatform) OpenURL(url string) error        { fmt.Println("open:", url); return nil }
func (d *devPlatform) RevealInExplorer(p string) error { return nil }
func (d *devPlatform) Notify(t, m string)              { fmt.Printf("%s: %s\n", t, m) }
func (d *devPlatform) ContextMenuEnabled() bool        { return false }
func (d *devPlatform) SetContextMenu(bool) error       { return nil }
func (d *devPlatform) PickFolder(appendMode bool) (string, error) {
	return "", fmt.Errorf("folder picker is only available on Windows")
}
