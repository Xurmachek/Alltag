//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const windowClass = "AllTagMainWindow"

const (
	wmTimerFallback = wmApp + 1
	idFallbackTimer = 1
	idSizeTimer     = 2

	imageIcon     = 1
	lrDefaultSize = 0x0040
	lrShared      = 0x8000
)

// Host owns the native window and the embedded browser.
type Host struct {
	Hwnd    syscall.Handle
	URL     string
	DataDir string
	WV      *WebView
	OnPaths func([]string)
	OnQuit  func()

	ready     bool
	failed    bool
	mu        sync.Mutex
	pending   []string
	closing   bool
	browserOK bool
}

var (
	current      *Host
	wndProcCb    = syscall.NewCallback(wndProc)
	hostMutex    syscall.Handle
	minWidth     = int32(980)
	minHeight    = int32(600)
	haveWebview2 bool
)

type minMaxInfo struct {
	PtReserved     point
	PtMaxSize      point
	PtMaxPosition  point
	PtMinTrackSize point
	PtMaxTrackSize point
}

func (h *Host) clientRect() rect {
	var r rect
	procGetClientRect.Call(hwnd(h.Hwnd), uintptr(unsafe.Pointer(&r)))
	return r
}

// ---------------------------------------------------------------- wndproc

func wndProc(hWnd syscall.Handle, uMsg uint32, wParam, lParam uintptr) uintptr {
	h := current
	switch uMsg {
	case wmSize:
		if h != nil && h.WV != nil {
			h.WV.Resize()
		}
		return 0
	case wmGetMinMaxInfo:
		info := (*minMaxInfo)(unsafe.Pointer(lParam))
		info.PtMinTrackSize = point{X: minWidth, Y: minHeight}
		return 0
	case wmClose:
		if h != nil {
			h.mu.Lock()
			h.closing = true
			h.mu.Unlock()
			if h.OnQuit != nil {
				h.OnQuit()
			}
		}
		procDestroyWindow.Call(hwnd(hWnd))
		return 0
	case wmDestroy:
		if h != nil && h.WV != nil {
			h.WV.Close()
		}
		procPostQuitMessage.Call(0)
		return 0
	case wmCopyData:
		if h != nil {
			cds := (*copyDataStruct)(unsafe.Pointer(lParam))
			if cds != nil && cds.LpData != 0 && cds.CbData > 0 {
				raw := unsafe.Slice((*uint16)(unsafe.Pointer(cds.LpData)), cds.CbData/2)
				text := syscall.UTF16ToString(raw)
				paths := splitPathList(text)
				if len(paths) > 0 && h.OnPaths != nil {
					h.OnPaths(paths)
				}
			}
			procShowWindowAsync.Call(hwnd(hWnd), 9) // SW_RESTORE
			procSetForegroundWindow.Call(hwnd(hWnd))
		}
		return 1
	case wmTimerFallback:
		if h != nil {
			h.mu.Lock()
			ok := h.ready
			h.mu.Unlock()
			if !ok {
				h.startBrowserFallback("WebView2 runtime not available")
			}
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd(hWnd), uintptr(uMsg), wParam, lParam)
	return r
}

func splitPathList(s string) []string {
	parts := strings.Split(s, "\x00")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------- window

func registerWindowClass() error {
	hInst := moduleHandle()
	// LoadImageW handles every size in our resource (BMP and PNG alike).
	icon, _, _ := procLoadImageW.Call(uintptr(hInst), 1, imageIcon, 0, 0,
		lrDefaultSize|lrShared)
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, 32512) // IDI_APPLICATION
	}
	cursor, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
	brush, _, _ := procCreateSolidBrush.Call(0x00121212)
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		Style:         0x0020, // CS_OWNDC
		LpfnWndProc:   wndProcCb,
		HInstance:     hInst,
		HIcon:         syscall.Handle(icon),
		HCursor:       syscall.Handle(cursor),
		HbrBackground: syscall.Handle(brush),
		LpszClassName: utf16p(windowClass),
		HIconSm:       syscall.Handle(icon),
	}
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return err
	}
	return nil
}

func enableDarkTitleBar(h syscall.Handle) {
	var on int32 = 1
	// DWMWA_USE_IMMERSIVE_DARK_MODE = 20 (19 on older Windows 10 builds)
	if r, _, _ := procDwmSetWindowAttribute.Call(uintptr(h), 20,
		uintptr(unsafe.Pointer(&on)), 4); r != 0 {
		procDwmSetWindowAttribute.Call(uintptr(h), 19, uintptr(unsafe.Pointer(&on)), 4)
	}
}

// Run opens the native window and hosts the UI until it is closed.
func Run(url, dataDir string, initialPaths []string, onPaths func([]string), onQuit func()) error {
	SetDarkModeForProcess()
	procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	defer procCoUninitialize.Call()

	if err := registerWindowClass(); err != nil {
		return fmt.Errorf("register window class: %w", err)
	}

	h := &Host{URL: url, DataDir: dataDir, OnPaths: onPaths, OnQuit: onQuit}
	h.pending = append(h.pending, initialPaths...)
	current = h

	sw := int32(1500)
	sh := int32(940)
	screenW, _, _ := procGetSystemMetrics.Call(0)
	screenH, _, _ := procGetSystemMetrics.Call(1)
	if int(screenW) > 0 && int(sw) > int(screenW)-80 {
		sw = int32(screenW) - 120
	}
	if int(screenH) > 0 && int(sh) > int(screenH)-120 {
		sh = int32(screenH) - 140
	}
	var wr = rect{0, 0, sw, sh}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&wr)), wsOverlappedWindow, 0)
	w := wr.Right - wr.Left
	hh := wr.Bottom - wr.Top
	x := (int32(screenW) - w) / 2
	y := (int32(screenH) - hh) / 2

	hwndVal, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16p(windowClass))),
		uintptr(unsafe.Pointer(utf16p("AllTag"))),
		uintptr(wsOverlappedWindow|wsVisible),
		uintptr(x), uintptr(y), uintptr(w), uintptr(hh),
		0, 0, uintptr(moduleHandle()), 0)
	if hwndVal == 0 {
		return fmt.Errorf("create window: %w", err)
	}
	h.Hwnd = syscall.Handle(hwndVal)
	enableDarkTitleBar(h.Hwnd)

	started := time.Now()
	err = BeginWebView2(h.Hwnd, dataDir, url, func(wv *WebView, err error) {
		h.mu.Lock()
		if err != nil {
			h.failed = true
			h.mu.Unlock()
			h.startBrowserFallback(err.Error())
			return
		}
		h.WV = wv
		h.ready = true
		pending := h.pending
		h.pending = nil
		h.mu.Unlock()
		if len(pending) > 0 && h.OnPaths != nil {
			go h.OnPaths(pending)
		}
	})
	if err != nil {
		h.failed = true
		h.startBrowserFallback(err.Error())
	} else {
		// give WebView2 ten seconds, then fall back to the system browser
		procSetTimerFallback(10 * time.Second)
	}
	_ = started

	procShowWindow.Call(hwnd(h.Hwnd), swShow)
	procUpdateWindow.Call(hwnd(h.Hwnd))

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if h.WV != nil {
		h.WV.Close()
	}
	return nil
}

func procSetTimerFallback(d time.Duration) {
	go func() {
		time.Sleep(d)
		if current != nil {
			procPostMessageW.Call(hwnd(current.Hwnd), wmTimerFallback, 0, 0)
		}
	}()
}

// startBrowserFallback shows the UI in Edge/Chrome app mode and keeps the
// process alive as a local server.
func (h *Host) startBrowserFallback(reason string) {
	h.mu.Lock()
	if h.browserOK || h.closing {
		h.mu.Unlock()
		return
	}
	h.browserOK = true
	h.mu.Unlock()

	prof := ""
	if dir, err := os.UserCacheDir(); err == nil {
		prof = dir + string(os.PathSeparator) + "AllTag" + string(os.PathSeparator) + "browser"
		_ = os.MkdirAll(prof, 0o755)
	}
	plat := &WinPlatform{OwnerHWND: h.Hwnd, ExePath: exePath()}
	if err := plat.LaunchBrowser(h.URL, prof); err != nil {
		messageBox("AllTag", "Не удалось запустить браузер: "+err.Error(), 0x10)
		procPostQuitMessage.Call(0)
		return
	}
	// hide the empty native window but keep the message loop for lifecycle
	procShowWindow.Call(hwnd(h.Hwnd), swHide)
	messageBox("AllTag",
		"Встроенный просмотр WebView2 недоступен, поэтому интерфейс открыт в браузере.\n\n"+
			"Причина: "+reason+"\n\n"+
			"Закройте окно AllTag в браузере, чтобы завершить программу.", 0x40)
}

// CloseNativeWindow asks the window to close (used by the quit API).
func CloseNativeWindow() {
	if current != nil {
		procPostMessageW.Call(hwnd(current.Hwnd), wmClose, 0, 0)
	}
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// ---------------------------------------------------------------- single instance

// AcquireSingleInstance returns false when AllTag already runs.
func AcquireSingleInstance() bool {
	name := utf16p("Local\\AllTag.SingleInstance.v1")
	m, _, errno := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if m == 0 {
		_ = errno
		return true
	}
	hostMutex = syscall.Handle(m)
	// ERROR_ALREADY_EXISTS (183) means another instance owns the mutex
	if errno == syscall.Errno(183) {
		return false
	}
	return true
}

// SendPathsToRunningInstance forwards file/folder arguments to the first
// instance and reports whether one was found.
func SendPathsToRunningInstance(paths []string) bool {
	hwndVal, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16p(windowClass))), 0)
	if hwndVal == 0 {
		return false
	}
	payload := ""
	for _, p := range paths {
		payload += p + "\x00"
	}
	payload += "\x00"
	utf, err := syscall.UTF16FromString(strings.TrimSuffix(payload, "\x00"))
	if err != nil || len(utf) == 0 {
		return false
	}
	// ensure double NUL termination
	buf := append([]uint16{}, utf...)
	buf = append(buf, 0)
	cds := copyDataStruct{DwData: 0x414C5447, CbData: uint32(len(buf) * 2),
		LpData: uintptr(unsafe.Pointer(&buf[0]))}
	procSendMessageW.Call(hwndVal, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)))
	return true
}

// BringToFront restores and focuses the main window.
func BringToFront() {
	if current != nil {
		procShowWindowAsync.Call(hwnd(current.Hwnd), 9)
		procSetForegroundWindow.Call(hwnd(current.Hwnd))
	}
}

// Fatal shows a blocking error message box (MB_ICONERROR).
func Fatal(title, msg string) {
	messageBox(title, msg, 0x00000010)
}
