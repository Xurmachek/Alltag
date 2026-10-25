//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// WinPlatform implements app.Platform on Windows.
type WinPlatform struct {
	OwnerHWND syscall.Handle
	ExePath   string
}

// Name identifies the platform in the API.
func (p *WinPlatform) Name() string { return "windows" }

// DataDir returns %APPDATA%\AllTag.
func (p *WinPlatform) DataDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	out := filepath.Join(dir, "AllTag")
	_ = os.MkdirAll(out, 0o755)
	return out
}

// PickFolder shows the native folder chooser.
func (p *WinPlatform) PickFolder(appendMode bool) (string, error) {
	title := "Выберите папку с музыкой"
	if appendMode {
		title = "Добавить папку к списку"
	}
	buf := make([]uint16, syscall.MAX_PATH)
	bi := browseInfoW{
		HwndOwner:      p.OwnerHWND,
		PszDisplayName: &buf[0],
		LpszTitle:      utf16p(title),
		UlFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditBox,
	}
	r, _, _ := procSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if r == 0 {
		return "", nil // cancelled
	}
	defer procCoTaskMemFree.Call(r)
	res := make([]uint16, syscall.MAX_PATH)
	ok, _, _ := procSHGetPathFromIDListW.Call(r, uintptr(unsafe.Pointer(&res[0])))
	if ok == 0 {
		return "", errors.New("не удалось получить путь папки")
	}
	return syscall.UTF16ToString(res), nil
}

// ---------------------------------------------------------------- registry

const ctxKeyBackground = `Software\Classes\Directory\Background\shell\AllTag`
const ctxKeyDirectory = `Software\Classes\Directory\shell\AllTag`

func openKey(path string, create bool) (syscall.Handle, error) {
	var h syscall.Handle
	var disp uint32
	var action uint32 = 3 // REG_OPENED_EXISTING_KEY
	if create {
		action = 2 // REG_CREATED_NEW_KEY
	}
	r, _, err := procRegCreateKeyExW.Call(
		uintptr(syscall.HKEY_CURRENT_USER),
		uintptr(unsafe.Pointer(utf16p(path))),
		0, 0, uintptr(action), uintptr(0x000F003F), // KEY_ALL_ACCESS
		0,
		uintptr(unsafe.Pointer(&h)),
		uintptr(unsafe.Pointer(&disp)))
	if r != 0 {
		return 0, err
	}
	return h, nil
}

func setSZ(h syscall.Handle, name, value string) error {
	val := syscall.StringToUTF16(value)
	r, _, err := procRegSetValueExW.Call(uintptr(h),
		uintptr(unsafe.Pointer(utf16p(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&val[0])),
		uintptr(len(val)*2))
	if r != 0 {
		return err
	}
	return nil
}

func keyExists(path string) bool {
	h, err := openKey(path, false)
	if err != nil {
		return false
	}
	procRegCloseKey.Call(uintptr(h))
	return true
}

func deleteTree(path string) {
	procRegDeleteTreeW.Call(uintptr(syscall.HKEY_CURRENT_USER),
		uintptr(unsafe.Pointer(utf16p(path))))
}

// ContextMenuEnabled reports whether the shell integration is registered.
func (p *WinPlatform) ContextMenuEnabled() bool {
	return keyExists(ctxKeyBackground) || keyExists(ctxKeyDirectory)
}

// SetContextMenu writes or removes the Explorer context menu entries.
func (p *WinPlatform) SetContextMenu(enabled bool) error {
	if !enabled {
		deleteTree(ctxKeyBackground)
		deleteTree(ctxKeyDirectory)
		return nil
	}
	exe := p.ExePath
	if exe == "" {
		exe, _ = os.Executable()
	}
	if exe == "" {
		return errors.New("cannot determine executable path")
	}
	quoted := `"` + exe + `"`
	entries := []struct {
		key  string
		arg  string
		text string
	}{
		{ctxKeyBackground, "%V", "Открыть в AllTag"},
		{ctxKeyDirectory, "%1", "Открыть в AllTag"},
	}
	for _, e := range entries {
		h, err := openKey(e.key, true)
		if err != nil {
			return err
		}
		_ = setSZ(h, "", e.text)
		_ = setSZ(h, "Icon", quoted)
		procRegCloseKey.Call(uintptr(h))

		cmdKey := e.key + `\command`
		h2, err := openKey(cmdKey, true)
		if err != nil {
			return err
		}
		_ = setSZ(h2, "", quoted+` "`+e.arg+`"`)
		procRegCloseKey.Call(uintptr(h2))
	}
	return nil
}

// RevealInExplorer opens Explorer with the file selected.
func (p *WinPlatform) RevealInExplorer(path string) error {
	return shellExec("open", "explorer.exe", `/select,"`+path+`"`)
}

// OpenURL opens a link in the default browser.
func (p *WinPlatform) OpenURL(url string) error {
	return shellExec("open", url, "")
}

// Notify shows a native message box.
func (p *WinPlatform) Notify(title, message string) {
	messageBox(title, message, 0x00000040) // MB_ICONINFORMATION
}

// LaunchBrowser opens the UI in an app-style browser window (fallback when
// WebView2 is unavailable).
func (p *WinPlatform) LaunchBrowser(url, profileDir string) error {
	candidates := edgeCandidates()
	for _, exe := range candidates {
		if _, err := os.Stat(exe); err != nil {
			continue
		}
		args := "--app=" + url + " --window-size=1500,940 --no-first-run " +
			"--no-default-browser-check --disable-features=Translate,MediaRouter " +
			"--user-data-dir=\"" + profileDir + "\""
		if err := shellExec("open", exe, args); err == nil {
			return nil
		}
	}
	// last resort: default browser
	return shellExec("open", url, "")
}

func edgeCandidates() []string {
	var out []string
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		out = append(out,
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
	}
	return out
}
