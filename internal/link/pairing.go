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
//	pxf://host:8474/v1/pair/<token>#sha256:<hex>
//
// The /v1/ segment versions the code's shape independently of the wire protocol
// (bumped only when the layout itself changes), and the /pair/ segment is a role
// marker so a pasted code — or a pxf:// deep link the OS hands the app — is
// recognizably a gateway pairing invite; a wrong-role paste or an unknown pxf://
// link kind fails with a clear message instead of half-parsing. Host may be a DNS
// name (preferred — survives gateway IP changes) or an IP; IPv6 literals are
// bracketed. The fragment pins the gateway's TLS cert.
type PairingCode struct {
	Host        string
	Port        int
	Token       string
	Fingerprint string // "sha256:<hex>"
}

const (
	// pairingScheme is the URI scheme; it doubles as the OS deep-link protocol, so
	// a clicked pxf:// link opens the app straight into pairing.
	pairingScheme = "pxf"
	// pairingFormat versions the pxf:// path shape, independent of the wire
	// ProtocolVersion — see the version-axes table in the identity design.
	pairingFormat = "v1"
	// pairingKindPair marks a pxf:// link as a gateway→agent pairing invite, told
	// apart from any future link kind by the path's role segment.
	pairingKindPair = "pair"
	// maxPairingCodeLen bounds the input before any parsing. A pxf:// deep link is
	// attacker-reachable (a web page can fire one); a real code is well under this.
	maxPairingCodeLen = 512
)

// IsPairingURL reports whether s carries the pxf:// pairing scheme, a cheap sniff
// (no full validation) used to route an OS deep-link launch into the pairing UI —
// which then surfaces any parse error itself, rather than the link being silently
// dropped.
func IsPairingURL(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), pairingScheme+"://")
}

func (p PairingCode) String() string {
	u := url.URL{
		Scheme:   pairingScheme,
		Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
		Path:     "/" + pairingFormat + "/" + pairingKindPair + "/" + p.Token,
		Fragment: p.Fingerprint,
	}
	return u.String()
}

// ParsePairingCode parses and fully validates a pxf:// v1 pairing code.
func ParsePairingCode(s string) (PairingCode, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxPairingCodeLen {
		return PairingCode{}, fmt.Errorf("pairing code is implausibly long (%d bytes) — re-copy it from the gateway", len(s))
	}
	u, err := url.Parse(s)
	if err != nil {
		return PairingCode{}, fmt.Errorf("not a valid pairing code: %w", err)
	}
	if u.Scheme != pairingScheme {
		return PairingCode{}, fmt.Errorf("not a proxyforward pairing code (expected %s://, got %q)", pairingScheme, u.Scheme)
	}
	token, err := tokenFromV1Path(u.Path)
	if err != nil {
		return PairingCode{}, err
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

// tokenFromV1Path validates the /<version>/<kind>/<token> path of a pxf:// code and
// returns the token. A wrong version or role marker fails loudly rather than
// silently accepting a stale-format or wrong-kind link.
func tokenFromV1Path(path string) (string, error) {
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segs) != 3 {
		return "", fmt.Errorf("pairing code path must be /%s/%s/<token>", pairingFormat, pairingKindPair)
	}
	if segs[0] != pairingFormat {
		return "", fmt.Errorf("unsupported pairing code version %q (this app understands %q) — update the app or re-copy the code", segs[0], pairingFormat)
	}
	if segs[1] != pairingKindPair {
		return "", fmt.Errorf("not a pairing invite (expected a %q link, got %q)", pairingKindPair, segs[1])
	}
	if segs[2] == "" {
		return "", fmt.Errorf("pairing code is missing the token")
	}
	return segs[2], nil
}
