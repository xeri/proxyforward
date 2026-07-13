//go:build windows

package tcpinfo

import (
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SIO_TCP_INFO = _WSAIORW(IOC_VENDOR, 39): read the kernel's TCP_INFO for a
// socket. The input is a DWORD version selector; version 0 (TCP_INFO_v0) is
// available on Windows 10 1703+ and carries the fields we need.
const sioTCPInfo = 0xD8000027

// tcpInfoV0 mirrors the C TCP_INFO_v0 struct byte-for-byte, including the
// alignment padding after the two single-byte fields. We only read RttUs, but
// WSAIoctl requires the output buffer to be at least the full struct size, so
// every field is laid out explicitly.
type tcpInfoV0 struct {
	State             uint32
	Mss               uint32
	ConnectionTimeMs  uint64
	TimestampsEnabled uint8
	_                 [3]uint8
	RttUs             uint32
	MinRttUs          uint32
	BytesInFlight     uint32
	Cwnd              uint32
	SndWnd            uint32
	RcvWnd            uint32
	RcvBuf            uint32
	BytesOut          uint64
	BytesIn           uint64
	BytesReordered    uint32
	BytesRetrans      uint32
	FastRetrans       uint32
	DupAcksIn         uint32
	TimeoutEpisodes   uint32
	SynRetrans        uint8
	_                 [3]uint8
	SndLimTransRwin   uint32
	SndLimTimeRwin    uint32
	SndLimBytesRwin   uint64
	SndLimTransCwnd   uint32
	SndLimTimeCwnd    uint32
	SndLimBytesCwnd   uint64
	SndLimTransSnd    uint32
	SndLimTimeSnd     uint32
	SndLimBytesSnd    uint64
}

// RTT reads the kernel's smoothed RTT for conn via SIO_TCP_INFO. ok is false
// when the ioctl fails or the kernel has no sample yet (RttUs == 0).
func RTT(conn *net.TCPConn) (time.Duration, bool) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	var (
		info     tcpInfoV0
		version  uint32
		bytesRet uint32
		ioErr    error
	)
	ctrlErr := raw.Control(func(fd uintptr) {
		ioErr = windows.WSAIoctl(
			windows.Handle(fd), sioTCPInfo,
			(*byte)(unsafe.Pointer(&version)), uint32(unsafe.Sizeof(version)),
			(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
			&bytesRet, nil, 0,
		)
	})
	if ctrlErr != nil || ioErr != nil || info.RttUs == 0 {
		return 0, false
	}
	return time.Duration(info.RttUs) * time.Microsecond, true
}
