//go:build windows

// Package wincon reattaches stdio to the parent console. Production builds
// use the windowsgui subsystem (no console is allocated), so headless
// subcommands run from a terminal would otherwise print nothing.
package wincon

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// AttachParent binds std handles to the invoking terminal's console if there
// is one — but only handles that aren't already valid, so redirection
// (`proxyforward agent > log.txt`, pipes, service host) is never clobbered.
// Safe to call when launched from Explorer or a service (does nothing).
func AttachParent() {
	const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	r, _, _ := k32.NewProc("AttachConsole").Call(uintptr(attachParentProcess))
	if r == 0 {
		return // no parent console (double-click launch, service, or already attached)
	}
	if !validStdHandle(windows.STD_OUTPUT_HANDLE) {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = f
			windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(f.Fd()))
			syscall.Stdout = syscall.Handle(f.Fd())
		}
	}
	if !validStdHandle(windows.STD_ERROR_HANDLE) {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stderr = f
			windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
			syscall.Stderr = syscall.Handle(f.Fd())
		}
	}
	if !validStdHandle(windows.STD_INPUT_HANDLE) {
		if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
			os.Stdin = f
			windows.SetStdHandle(windows.STD_INPUT_HANDLE, windows.Handle(f.Fd()))
			syscall.Stdin = syscall.Handle(f.Fd())
		}
	}
}

func validStdHandle(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	// A handle can be non-zero yet dead; GetFileType tells us if it points at
	// anything real (pipe, file, or console).
	t, err := windows.GetFileType(h)
	return err == nil && t != windows.FILE_TYPE_UNKNOWN
}
