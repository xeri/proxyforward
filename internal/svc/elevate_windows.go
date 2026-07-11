//go:build windows

package svc

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW  = shell32.NewProc("ShellExecuteExW")
	seeMaskNoCloseProc   = uint32(0x00000040) // SEE_MASK_NOCLOSEPROCESS
	errCancelledByUser   = windows.ERROR_CANCELLED
	elevatedTaskDeadline = 5 * time.Minute
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

// IsElevated reports whether this process already runs with an elevated
// token.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RunElevatedTask relaunches this executable as
// `proxyforward elevated-task <task> [args…]` through the UAC prompt
// ("runas"), waits for it, and returns an error unless it exited 0. The main
// process never elevates itself — the consent covers exactly one task.
func RunElevatedTask(task string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}
	params := windows.ComposeCommandLine(append([]string{"elevated-task", task}, args...))

	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	paramPtr, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return fmt.Errorf("bad task arguments: %w", err)
	}

	info := &shellExecuteInfo{
		fMask:        seeMaskNoCloseProc,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: paramPtr,
		nShow:        windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(*info))

	r, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(info)))
	if r == 0 {
		if callErr == errCancelledByUser {
			return fmt.Errorf("elevation declined: the UAC prompt was cancelled")
		}
		return fmt.Errorf("launch elevated helper: %w", callErr)
	}
	if info.hProcess == 0 {
		return fmt.Errorf("elevated helper started but returned no process handle")
	}
	defer windows.CloseHandle(info.hProcess)

	s, err := windows.WaitForSingleObject(info.hProcess, uint32(elevatedTaskDeadline.Milliseconds()))
	if err != nil {
		return fmt.Errorf("wait for elevated helper: %w", err)
	}
	if s != uint32(windows.WAIT_OBJECT_0) {
		return fmt.Errorf("elevated helper did not finish within %s", elevatedTaskDeadline)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return fmt.Errorf("read elevated helper exit code: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("elevated task %s failed (exit %d) — check the log", task, code)
	}
	return nil
}
