package link

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// PairingCode is the single line a gateway shows and an agent pastes:
//
//	pf1://host:8474/<token>#sha256:<hex>
//
// Host may be a DNS name (preferred — survives gateway IP changes) or an IP;
// IPv6 literals are bracketed. The fragment pins the gateway's TLS cert.
type PairingCode struct {
	Host        string
	Port        int
	Token       string
	Fingerprint string // "sha256:<hex>"
}

const pairingScheme = "pf1"

func (p PairingCode) String() string {
	u := url.URL{
		Scheme:   pairingScheme,
		Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
		Path:     "/" + p.Token,
		Fragment: p.Fingerprint,
	}
	return u.String()
}

// ParsePairingCode parses and fully validates a pairing code.
func ParsePairingCode(s string) (PairingCode, error) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil {
		return PairingCode{}, fmt.Errorf("not a valid pairing code: %w", err)
	}
	if u.Scheme != pairingScheme {
		return PairingCode{}, fmt.Errorf("not a proxyforward pairing code (expected %s://, got %q)", pairingScheme, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return PairingCode{}, fmt.Errorf("pairing code is missing the gateway host")
	}
	portStr := u.Port()
	if portStr == "" {
		return PairingCode{}, fmt.Errorf("pairing code is missing the gateway control port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return PairingCode{}, fmt.Errorf("pairing code has an invalid port %q", portStr)
	}
	token := strings.TrimPrefix(u.Path, "/")
	if token == "" {
		return PairingCode{}, fmt.Errorf("pairing code is missing the token")
	}
	if strings.Contains(token, "/") {
		return PairingCode{}, fmt.Errorf("pairing code token contains unexpected %q", "/")
	}
	fp := u.Fragment
	if !strings.HasPrefix(fp, "sha256:") || len(fp) != len("sha256:")+64 {
		return PairingCode{}, fmt.Errorf("pairing code is missing a valid sha256 certificate fingerprint")
	}
	for _, c := range fp[len("sha256:"):] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return PairingCode{}, fmt.Errorf("pairing code fingerprint contains non-hex characters")
		}
	}
	return PairingCode{Host: host, Port: port, Token: token, Fingerprint: strings.ToLower(fp)}, nil
}
