//go:build windows

package platform

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// The WebView2 loader is Microsoft's redistributable (see
// webview2/LICENSE-WebView2Loader.txt). It is embedded so AllTag stays a
// single portable executable; on first start it is unpacked next to the
// user's profile data.
//
//go:embed webview2/WebView2Loader.x64.dll
var webviewAssets embed.FS

func loaderBytes() ([]byte, error) {
	return webviewAssets.ReadFile("webview2/WebView2Loader.x64.dll")
}

func runtimeLoaderPath(dataDir string) (string, error) {
	data, err := loaderBytes()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(data))[:16]
	path := filepath.Join(dir, "WebView2Loader-"+sum+".dll")
	if st, err := os.Stat(path); err == nil && st.Size() == int64(len(data)) {
		return path, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// ---------------------------------------------------------------- COM glue

func comCall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	if obj == nil {
		return 0x80004003 // E_POINTER
	}
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	if fn == 0 {
		return 0x80004003
	}
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(obj))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func comQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv != 0 {
		*(*uintptr)(unsafe.Pointer(ppv)) = this
	}
	return 0
}

func comAddRef(this uintptr) uintptr  { return 1 }
func comRelease(this uintptr) uintptr { return 1 }

// ---------------------------------------------------------------- handlers

// setup tracks one WebView2 activation, driven entirely from the UI thread.
type setup struct {
	hwnd     syscall.Handle
	url      string
	onDone   func(*WebView, error)
	finished bool
	envH     *envHandler
	ctrlH    *ctrlHandler
	ctrl     unsafe.Pointer
}

type envHandler struct {
	vtbl  *[4]uintptr
	owner *setup
}

type ctrlHandler struct {
	vtbl  *[4]uintptr
	owner *setup
}

var (
	envVtbl = [4]uintptr{
		syscall.NewCallback(comQueryInterface),
		syscall.NewCallback(comAddRef),
		syscall.NewCallback(comRelease),
		syscall.NewCallback(envInvoke),
	}
	ctrlVtbl = [4]uintptr{
		syscall.NewCallback(comQueryInterface),
		syscall.NewCallback(comAddRef),
		syscall.NewCallback(comRelease),
		syscall.NewCallback(ctrlInvoke),
	}
	// handlers must stay reachable for the lifetime of the process
	liveHandlers []any
)

func envInvoke(this, hr, env uintptr) uintptr {
	h := (*envHandler)(unsafe.Pointer(this))
	if h == nil || h.owner == nil {
		return 0
	}
	s := h.owner
	if hr != 0 || env == 0 {
		s.finish(nil, fmt.Errorf("WebView2 environment: 0x%08x", uint32(hr)))
		return 0
	}
	if s.ctrlH == nil {
		s.ctrlH = &ctrlHandler{vtbl: &ctrlVtbl, owner: s}
		liveHandlers = append(liveHandlers, s.ctrlH)
	}
	if res := comCall(unsafe.Pointer(env), 3, uintptr(s.hwnd), uintptr(unsafe.Pointer(s.ctrlH))); int32(res) < 0 {
		s.finish(nil, fmt.Errorf("WebView2 controller request: 0x%08x", uint32(res)))
	}
	return 0
}

func ctrlInvoke(this, hr, ctrl uintptr) uintptr {
	h := (*ctrlHandler)(unsafe.Pointer(this))
	if h == nil || h.owner == nil {
		return 0
	}
	s := h.owner
	if hr != 0 || ctrl == 0 {
		s.finish(nil, fmt.Errorf("WebView2 controller: 0x%08x", uint32(hr)))
		return 0
	}
	s.ctrl = unsafe.Pointer(ctrl)
	wv := &WebView{controller: s.ctrl, hwnd: s.hwnd, url: s.url}
	comCall(s.ctrl, 4, 1)           // put_IsVisible(TRUE)
	comCall(s.ctrl, 28, 0xFF121212) // put_DefaultBackgroundColor(COREWEBVIEW2_COLOR)
	var core unsafe.Pointer
	if res := comCall(s.ctrl, 26, uintptr(unsafe.Pointer(&core))); int32(res) >= 0 && core != nil {
		wv.webview = core
		var settings unsafe.Pointer
		if res := comCall(core, 3, uintptr(unsafe.Pointer(&settings))); int32(res) >= 0 && settings != nil {
			comCall(settings, 10, 0) // IsStatusBarEnabled
			comCall(settings, 12, 0) // AreDevToolsEnabled
			comCall(settings, 14, 0) // AreDefaultContextMenusEnabled
			comCall(settings, 18, 0) // IsZoomControlEnabled
		}
		comCall(core, 5, uintptr(unsafe.Pointer(utf16p(s.url)))) // Navigate
	}
	wv.Resize()
	s.finish(wv, nil)
	return 0
}

func (s *setup) finish(wv *WebView, err error) {
	if s.finished {
		return
	}
	s.finished = true
	if s.onDone != nil {
		s.onDone(wv, err)
	}
}

// ---------------------------------------------------------------- WebView

// WebView is a hosted WebView2 instance.
type WebView struct {
	controller unsafe.Pointer
	webview    unsafe.Pointer
	hwnd       syscall.Handle
	url        string
	dead       bool
}

// Resize fits the web view to the window client area.
func (w *WebView) Resize() {
	if w == nil || w.controller == nil {
		return
	}
	var r rect
	procGetClientRect.Call(hwnd(w.hwnd), uintptr(unsafe.Pointer(&r)))
	comCall(w.controller, 6, uintptr(unsafe.Pointer(&r)))
}

// Close releases the controller.
func (w *WebView) Close() {
	if w == nil || w.controller == nil || w.dead {
		return
	}
	comCall(w.controller, 25)
	w.dead = true
}

// Navigate loads a URL in the web view.
func (w *WebView) Navigate(url string) {
	if w == nil || w.webview == nil {
		return
	}
	comCall(w.webview, 5, uintptr(unsafe.Pointer(utf16p(url))))
}

// BeginWebView2 starts WebView2 inside hwnd. Activation is asynchronous: the
// completion handler runs while the window pumps messages and calls onDone.
// A synchronously returned error means the runtime cannot be used at all.
func BeginWebView2(hwnd_ syscall.Handle, dataDir, startURL string, onDone func(*WebView, error)) error {
	dllPath, err := runtimeLoaderPath(dataDir)
	if err != nil {
		return err
	}
	lib, err := syscall.LoadLibrary(dllPath)
	if err != nil {
		return fmt.Errorf("WebView2Loader: %w", err)
	}
	proc, err := syscall.GetProcAddress(lib, "CreateCoreWebView2EnvironmentWithOptions")
	if err != nil {
		return fmt.Errorf("WebView2 loader export: %w", err)
	}
	s := &setup{hwnd: hwnd_, url: startURL, onDone: onDone}
	s.envH = &envHandler{vtbl: &envVtbl, owner: s}
	liveHandlers = append(liveHandlers, s.envH)

	userData := filepath.Join(dataDir, "WebView2")
	_ = os.MkdirAll(userData, 0o755)

	hr, _, _ := syscall.SyscallN(proc,
		0,
		uintptr(unsafe.Pointer(utf16p(userData))),
		0,
		uintptr(unsafe.Pointer(s.envH)))
	if int32(hr) < 0 {
		return fmt.Errorf("WebView2 environment: 0x%08x", uint32(hr))
	}
	return nil
}
