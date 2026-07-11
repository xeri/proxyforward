// Package netid resolves this machine's local network identity for the GUI's
// identity badges and diagnostics. It reports LAN IPv4 addresses only —
// non-loopback, non-link-local — never anything that leaves the host.
package netid

import (
	"net"
	"sort"
)

// LocalIPv4s returns the machine's non-loopback, non-link-local IPv4 addresses
// as dotted strings, sorted for stable display. Returns nil on error or when
// the host has no routable IPv4 interface.
func LocalIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		v4 := ip.To4()
		if v4 == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, v4.String())
	}
	sort.Strings(out)
	return out
}
