//go:build windows

package netnotify

import (
	"context"
	"log/slog"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                 = windows.NewLazySystemDLL("iphlpapi.dll")
	procNotifyAddrChange     = iphlpapi.NewProc("NotifyAddrChange")
	procCancelIPChangeNotify = iphlpapi.NewProc("CancelIPChangeNotify")
)

// watchNetChange loops on NotifyAddrChange: each completion of the
// overlapped event means the local IPv4 address table changed (adapter
// connect/disconnect, DHCP renew onto a new net, VPN up/down). A second
// event, signalled on ctx cancellation, makes shutdown immediate.
//
// The OVERLAPPED must be heap-allocated and outlive any pending
// notification: the kernel writes through the pointer asynchronously, and Go
// stacks move. It is cancelled before the handles are closed.
func watchNetChange(ctx context.Context, logger *slog.Logger, ch chan<- struct{}) {
	event, err := windows.CreateEvent(nil, 0, 0, nil) // auto-reset
	if err != nil {
		logger.Warn("network-change notifications unavailable", "err", err)
		return
	}
	cancelEvent, err := windows.CreateEvent(nil, 1, 0, nil) // manual-reset
	if err != nil {
		windows.CloseHandle(event)
		logger.Warn("network-change notifications unavailable", "err", err)
		return
	}

	overlap := new(windows.Overlapped) // heap: pinned for the pending request
	handle := new(windows.Handle)
	overlap.HEvent = event
	pending := false

	// Trip cancelEvent when ctx dies; stopped guarantees the helper exits
	// before the teardown below runs.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-watchCtx.Done()
		windows.SetEvent(cancelEvent)
	}()

	defer func() {
		cancelWatch()
		<-stopped
		if pending {
			procCancelIPChangeNotify.Call(uintptr(unsafe.Pointer(overlap)))
		}
		windows.CloseHandle(event)
		windows.CloseHandle(cancelEvent)
		runtime.KeepAlive(overlap)
		runtime.KeepAlive(handle)
	}()

	for ctx.Err() == nil {
		r, _, callErr := procNotifyAddrChange.Call(
			uintptr(unsafe.Pointer(handle)),
			uintptr(unsafe.Pointer(overlap)),
		)
		if e := windows.Errno(r); e != 0 && e != windows.ERROR_IO_PENDING {
			logger.Warn("NotifyAddrChange failed; network-change notifications disabled", "err", callErr)
			return
		}
		pending = true
		s, err := windows.WaitForMultipleObjects([]windows.Handle{event, cancelEvent}, false, windows.INFINITE)
		if err != nil || s != uint32(windows.WAIT_OBJECT_0) {
			return // cancelled or wait failure; pending request cleaned up in defer
		}
		pending = false // this notification completed
		logger.Debug("network address change detected")
		tick(ch)
		// Change storms (adapter flaps fire several notifications) coalesce
		// into one tick via the buffered channel; a brief pause avoids a
		// tight re-arm spin.
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return
		}
	}
}
