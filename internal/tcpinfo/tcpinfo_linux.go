//go:build linux

package tcpinfo

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// RTT reads the kernel's smoothed RTT via getsockopt(TCP_INFO). tcpi_rtt is in
// microseconds; ok is false when the syscall fails or no sample exists yet.
func RTT(conn *net.TCPConn) (time.Duration, bool) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	var (
		info *unix.TCPInfo
		gerr error
	)
	ctrlErr := raw.Control(func(fd uintptr) {
		info, gerr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	})
	if ctrlErr != nil || gerr != nil || info == nil || info.Rtt == 0 {
		return 0, false
	}
	return time.Duration(info.Rtt) * time.Microsecond, true
}
