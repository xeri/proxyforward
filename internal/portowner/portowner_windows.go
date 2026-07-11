//go:build windows

package portowner

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcp  = iphlpapi.NewProc("GetExtendedTcpTable")
	errInsufficientBuf  = syscall.Errno(122) // ERROR_INSUFFICIENT_BUFFER
	wsaEADDRINUSE       = syscall.Errno(10048)
	tableOwnerPidListen = uint32(3) // TCP_TABLE_OWNER_PID_LISTENER
)

func isAddrInUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == wsaEADDRINUSE
}

// mibTCPRowOwnerPID is MIB_TCPROW_OWNER_PID (IPv4).
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32 // network byte order in the low 16 bits
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// mibTCP6RowOwnerPID is MIB_TCP6ROW_OWNER_PID.
type mibTCP6RowOwnerPID struct {
	LocalAddr    [16]byte
	LocalScopeID uint32
	LocalPort    uint32
	RemoteAddr   [16]byte
	RemoteScope  uint32
	RemotePort   uint32
	State        uint32
	OwningPID    uint32
}

func lookup(port int) (Owner, bool) {
	if pid, ok := findPID(windows.AF_INET, port); ok {
		return ownerFromPID(pid), true
	}
	if pid, ok := findPID(windows.AF_INET6, port); ok {
		return ownerFromPID(pid), true
	}
	return Owner{}, false
}

// findPID scans one address family's listener table for a matching local
// port.
func findPID(family uint32, port int) (uint32, bool) {
	var size uint32
	// First call sizes the buffer; loop in case the table grows in between.
	for range 4 {
		var buf []byte
		var ptr unsafe.Pointer
		if size > 0 {
			buf = make([]byte, size)
			ptr = unsafe.Pointer(&buf[0])
		}
		r, _, _ := procGetExtendedTcp.Call(
			uintptr(ptr),
			uintptr(unsafe.Pointer(&size)),
			0, // unsorted
			uintptr(family),
			uintptr(tableOwnerPidListen),
			0,
		)
		switch syscall.Errno(r) {
		case 0:
			if len(buf) < 4 {
				return 0, false
			}
			return scanTable(family, buf, port)
		case errInsufficientBuf:
			continue // size was updated; retry with a bigger buffer
		default:
			return 0, false
		}
	}
	return 0, false
}

func scanTable(family uint32, buf []byte, port int) (uint32, bool) {
	n := int(binary.LittleEndian.Uint32(buf))
	rows := buf[4:]
	var rowSize int
	if family == windows.AF_INET {
		rowSize = int(unsafe.Sizeof(mibTCPRowOwnerPID{}))
	} else {
		rowSize = int(unsafe.Sizeof(mibTCP6RowOwnerPID{}))
	}
	for i := 0; i < n && (i+1)*rowSize <= len(rows); i++ {
		row := rows[i*rowSize:]
		var localPort uint32
		var pid uint32
		if family == windows.AF_INET {
			r := (*mibTCPRowOwnerPID)(unsafe.Pointer(&row[0]))
			localPort, pid = r.LocalPort, r.OwningPID
		} else {
			r := (*mibTCP6RowOwnerPID)(unsafe.Pointer(&row[0]))
			localPort, pid = r.LocalPort, r.OwningPID
		}
		// dwLocalPort holds the port in network byte order in its low word.
		p := int(localPort&0xff)<<8 | int(localPort>>8&0xff)
		if p == port {
			return pid, true
		}
	}
	return 0, false
}

func ownerFromPID(pid uint32) Owner {
	o := Owner{PID: pid}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return o // access denied (system process) — PID alone still helps
	}
	defer windows.CloseHandle(h)
	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err == nil {
		o.Name = filepath.Base(windows.UTF16ToString(buf[:size]))
	}
	return o
}
