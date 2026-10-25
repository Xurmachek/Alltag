//go:build windows

package main

import (
	"alltag/internal/app"
	"alltag/internal/platform"
	"os"
	"path/filepath"
)

func newPlatform(dataDir string) app.Platform { return &platform.WinPlatform{ExePath: exePath()} }

func platformDataDir() string { return (&platform.WinPlatform{}).DataDir() }

func acquireSingleInstance() bool { return platform.AcquireSingleInstance() }

func sendPathsToRunningInstance(paths []string) {
	if len(paths) == 0 {
		platform.BringToFront()
		return
	}
	if !platform.SendPathsToRunningInstance(paths) {
		platform.BringToFront()
	}
}

func runUI(url, dataDir string, paths []string, onPaths func([]string), onQuit func()) error {
	return platform.Run(url, dataDir, paths, onPaths, func() { onQuit() })
}

func quitApp()                { platform.CloseNativeWindow() }
func fatal(title, msg string) { platform.Fatal(title, msg) }

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
