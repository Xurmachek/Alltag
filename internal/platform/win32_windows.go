//go:build windows

package platform

import (
	"errors"
	"syscall"
	"unsafe"
)

// Minimal Win32 bindings (kept tiny and dependency free).

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procLoadImageW                    = user32.NewProc("LoadImageW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	procFindWindowW                   = user32.NewProc("FindWindowW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procIsIconic                      = user32.NewProc("IsIconic")
	procShowWindowAsync               = user32.NewProc("ShowWindowAsync")
	procGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW             = user32.NewProc("SetWindowLongPtrW")
	procAdjustWindowRect              = user32.NewProc("AdjustWindowRect")

	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	procCreateMutexW       = kernel32.NewProc("CreateMutexW")
	procGetModuleFileNameW = kernel32.NewProc("GetModuleFileNameW")

	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	procRegCreateKeyExW  = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	procRegDeleteTreeW   = advapi32.NewProc("RegDeleteTreeW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject     = gdi32.NewProc("DeleteObject")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	swShow             = 5
	swHide             = 0
	colorWindow        = 5
	cwUseDefault       = 0x80000000

	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmClose         = 0x0010
	wmGetMinMaxInfo = 0x0024
	wmCopyData      = 0x004A
	wmApp           = 0x8000

	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040
	bifEditBox          = 0x0010

	regSZ = 1
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

type msg struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type copyDataStruct struct {
	DwData uintptr
	CbData uint32
	LpData uintptr
}

type browseInfoW struct {
	HwndOwner      syscall.Handle
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

func hwnd(h syscall.Handle) uintptr { return uintptr(h) }

func utf16p(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// SetDarkModeForProcess enables per-monitor DPI awareness.
func SetDarkModeForProcess() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == -4
	procSetProcessDpiAwarenessContext.Call(uintptr(0xFFFFFFFFFFFFFFFC))
}

func setProcessDPIAware() { SetDarkModeForProcess() }

func moduleHandle() syscall.Handle {
	h, _, _ := procGetModuleHandleW.Call(0)
	return syscall.Handle(h)
}

func messageBox(title, text string, flags uint32) int {
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16p(text))),
		uintptr(unsafe.Pointer(utf16p(title))), uintptr(flags))
	return int(r)
}

func shellExec(op, file, params string) error {
	r, _, err := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(utf16p(op))),
		uintptr(unsafe.Pointer(utf16p(file))),
		uintptr(unsafe.Pointer(utf16p(params))),
		0, 1 /* SW_SHOWNORMAL */)
	if r <= 32 {
		return errors.New("ShellExecute failed: " + err.Error())
	}
	return nil
}
