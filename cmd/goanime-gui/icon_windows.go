//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Wails sets only the small window icon: winc's Form calls
// SetIcon(ICON_SMALL, …) and never sets ICON_BIG. With ICON_BIG unset,
// Windows falls back to the window-class icon for the Alt+Tab switcher and
// the large taskbar preview — and the class icon is never populated either,
// so the app shows the generic executable icon there.
//
// This file fills that gap: it finds our own top-level window and sends it
// the large icon loaded from the same resource Wails uses for the small one.
const (
	wmSetIcon = 0x0080
	iconBig   = 1
	iconSmall = 0

	imageIcon      = 1
	lrDefaultSize  = 0x00000040
	lrSharedHandle = 0x00008000

	smCXIcon    = 11
	smCYIcon    = 12
	smCXSmIcon  = 49
	smCYSmIcon  = 50
	appIconResI = 3 // must match winc.AppIconID
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentProcessID = kernel32.NewProc("GetCurrentProcessId")
)

// applyWindowIcon sets the large window icon once the window exists. It is
// safe to call from any goroutine and gives up quietly: an app that runs
// with the wrong Alt+Tab icon is a blemish, never a reason to fail startup.
func applyWindowIcon() {
	// OnStartup can fire a moment before the window is on screen, so poll
	// briefly rather than racing it.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findOwnWindow(); hwnd != 0 {
			setIcons(hwnd)
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// setIcons loads the icon at the two sizes Windows asks for and assigns
// both, so the switcher and the title bar agree.
func setIcons(hwnd uintptr) {
	instance, _, _ := procGetModuleHandleW.Call(0)

	// LazyProc.Call always returns a non-nil error -- it is the thread's
	// errno, which is set whether or not the call succeeded -- so there is
	// nothing meaningful to check here, and the whole file is best-effort
	// anyway: WM_SETICON has no failure worth reporting to the user.
	if big := loadIcon(instance, systemMetric(smCXIcon), systemMetric(smCYIcon)); big != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
	}
	if small := loadIcon(instance, systemMetric(smCXSmIcon), systemMetric(smCYSmIcon)); small != 0 {
		_, _, _ = procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	}
}

// loadIcon pulls the app icon resource at a specific size. A zero size
// falls back to the system default via LR_DEFAULTSIZE.
func loadIcon(instance, cx, cy uintptr) uintptr {
	flags := uintptr(0)
	if cx == 0 || cy == 0 {
		flags = lrDefaultSize
	}
	h, _, _ := procLoadImageW.Call(
		instance,
		appIconResI, // the resource id, as MAKEINTRESOURCE would encode it
		imageIcon,
		cx,
		cy,
		flags,
	)
	return h
}

// systemMetric returns a GetSystemMetrics value, or 0 when the metric is
// unavailable -- which loadIcon reads as "use the system default size".
func systemMetric(index uintptr) uintptr {
	v, _, _ := procGetSystemMetrics.Call(index)
	return v
}

// findOwnWindow returns the handle of this process's first visible
// top-level window, or 0 if it does not exist yet.
func findOwnWindow() uintptr {
	pid, _, _ := procGetCurrentProcessID.Call()

	var found uintptr
	cb := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var wndPID uint32
		// #nosec G103 -- passing a pointer to a syscall is the only way to
		// call GetWindowThreadProcessId; the target is a local uint32 whose
		// lifetime spans the call.
		_, _, _ = procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&wndPID)))
		if uintptr(wndPID) != pid {
			return 1 // keep enumerating
		}
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}
		found = hwnd
		return 0 // stop
	})

	_, _, _ = procEnumWindows.Call(cb, 0)
	return found
}
