package link

import (
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPairingRoundTrip(t *testing.T) {
	cases := []PairingCode{
		{Host: "gw.example.com", Port: 8474, Token: "abc123", Fingerprint: "sha256:" + strings.Repeat("ab", 32)},
		{Host: "203.0.113.7", Port: 443, Token: "t0k", Fingerprint: "sha256:" + strings.Repeat("0f", 32)},
		{Host: "2001:db8::1", Port: 8474, Token: "v6token", Fingerprint: "sha256:" + strings.Repeat("9c", 32)},
	}
	for _, in := range cases {
		t.Run(in.Host, func(t *testing.T) {
			s := in.String()
			got, err := ParsePairingCode(s)
			if err != nil {
				t.Fatalf("parse %q: %v", s, err)
			}
			if got != in {
				t.Fatalf("round trip: got %+v want %+v", got, in)
			}
		})
	}
}

func TestPairingParseWhitespaceTolerant(t *testing.T) {
	in := PairingCode{Host: "gw", Port: 1, Token: "t", Fingerprint: "sha256:" + strings.Repeat("aa", 32)}
	if _, err := ParsePairingCode("  " + in.String() + "\r\n"); err != nil {
		t.Fatalf("should tolerate copy-paste whitespace: %v", err)
	}
}

// TestPairingEmitsPxfV1 pins the current wire shape: the code an agent pastes is a
// pxf:// URL carrying the format version and a "pair" role marker, so the frontend
// and the OS deep-link handler can tell a pairing invite from any other pxf:// link.
func TestPairingEmitsPxfV1(t *testing.T) {
	s := PairingCode{Host: "gw.example.com", Port: 8474, Token: "tok123", Fingerprint: "sha256:" + strings.Repeat("ab", 32)}.String()
	if !strings.HasPrefix(s, "pxf://") {
		t.Errorf("pairing code must use the pxf:// scheme, got %q", s)
	}
	if !strings.Contains(s, "/v1/pair/") {
		t.Errorf("pairing code must carry the /v1/pair/ version+role marker, got %q", s)
	}
}

// TestPairingParsesPxfV1 is the RED driver: a valid v1 code parses to its parts.
func TestPairingParsesPxfV1(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	got, err := ParsePairingCode("pxf://gw.example.com:8474/v1/pair/tok123#" + fp)
	if err != nil {
		t.Fatalf("valid v1 code rejected: %v", err)
	}
	want := PairingCode{Host: "gw.example.com", Port: 8474, Token: "tok123", Fingerprint: fp}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

// TestPairingRejectsPxfShape rejects pxf:// codes whose version or role marker the
// parser does not understand, so a future kind can never be mistaken for a pairing
// invite and a stale-format code fails loudly instead of half-parsing.
func TestPairingRejectsPxfShape(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	bad := map[string]string{
		"unknown version": "pxf://gw:8474/v9/pair/tok#" + fp,
		"unknown kind":    "pxf://gw:8474/v1/join/tok#" + fp,
		"missing kind":    "pxf://gw:8474/v1/tok#" + fp,
		"missing token":   "pxf://gw:8474/v1/pair/#" + fp,
		"no path":         "pxf://gw:8474#" + fp,
		"extra segment":   "pxf://gw:8474/v1/pair/tok/extra#" + fp,
	}
	for name, s := range bad {
		if _, err := ParsePairingCode(s); err == nil {
			t.Errorf("%s: expected error for %q", name, s)
		}
	}
}

// TestPairingRejectsOverlong caps the input before any real parsing — a pxf:// deep
// link is attacker-reachable (a web page can fire one), so a pathological string
// must be refused cheaply, not parsed.
func TestPairingRejectsOverlong(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	huge := "pxf://gw:8474/v1/pair/" + strings.Repeat("a", 4096) + "#" + fp
	if _, err := ParsePairingCode(huge); err == nil {
		t.Errorf("expected an over-length pairing code to be rejected")
	}
}

// A pairing code is a value that must survive being re-emitted: the app stores
// it, shows it, and hands it on. url.Parse is lenient about the authority — it
// splits an unbracketed IPv6 literal at the last colon, so "pxf://::1/..."
// yielded host ":" and port 1 — and a host like that is one String() cannot put
// back (JoinHostPort brackets anything containing a colon, and a bracketed
// non-IP is not a URL). So the parser must refuse a host that is neither an IP
// nor a name, rather than hand back a code that decays on the next hop.
// Found by FuzzParsePairingCode.
func TestPairingRejectsAHostItCannotReEmit(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	bad := map[string]string{
		"unbracketed v6 loopback": "pxf://::1/v1/pair/tok#" + fp,
		"unbracketed v6 literal":  "pxf://2001:db8::1/v1/pair/tok#" + fp,
		"colon in a name":         "pxf://a:b:8474/v1/pair/tok#" + fp,
		"space in a name":         "pxf://a%20b:8474/v1/pair/tok#" + fp,
		"bracket in a name":       "pxf://a%5Db:8474/v1/pair/tok#" + fp,
		"zoned v6 literal":        "pxf://[fe80::1%25eth0]:8474/v1/pair/tok#" + fp,
	}
	for name, s := range bad {
		t.Run(name, func(t *testing.T) {
			got, err := ParsePairingCode(s)
			if err == nil {
				t.Fatalf("accepted %q as host %q; String() re-emits it as %q", s, got.Host, got.String())
			}
		})
	}
}

// The hosts a pairing code legitimately carries must still parse, and must be
// the fixed point the type promises. Bracketed IPv6 is the case the rejection
// above must not take with it.
func TestPairingAcceptsRealHosts(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	for _, host := range []string{
		"gw.example.com", "gw", "GW.Example.COM", "203.0.113.7", "2001:db8::1",
		"::1", "host_1.internal", "gw-2.example.com.", "xn--mnchen-3ya.example",
	} {
		t.Run(host, func(t *testing.T) {
			in := PairingCode{Host: host, Port: 8474, Token: "tok", Fingerprint: fp}
			got, err := ParsePairingCode(in.String())
			if err != nil {
				t.Fatalf("rejected a real host: %v (code %q)", err, in.String())
			}
			if got != in {
				t.Fatalf("round trip: got %+v want %+v", got, in)
			}
		})
	}
}

func TestPairingParseRejects(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	bad := map[string]string{
		"wrong scheme":    "https://gw:8474/v1/pair/tok#" + fp,
		"no host":         "pxf://:8474/v1/pair/tok#" + fp,
		"no port":         "pxf://gw/v1/pair/tok#" + fp,
		"bad port":        "pxf://gw:99999/v1/pair/tok#" + fp,
		"no fingerprint":  "pxf://gw:8474/v1/pair/tok",
		"short fp":        "pxf://gw:8474/v1/pair/tok#sha256:abcd",
		"non-hex fp":      "pxf://gw:8474/v1/pair/tok#sha256:" + strings.Repeat("zz", 32),
		"md5 fingerprint": "pxf://gw:8474/v1/pair/tok#md5:" + strings.Repeat("ab", 32),
	}
	for name, s := range bad {
		if _, err := ParsePairingCode(s); err == nil {
			t.Errorf("%s: expected error for %q", name, s)
		}
	}
}

// v2fixture is a code whose parts are exactly the widths v2 packs, so String emits
// the v2 shape. The bytes are arbitrary but fixed: the checksum sweep below depends
// on this exact code.
func v2fixture(host string) PairingCode {
	return PairingCode{
		Host:        host,
		Port:        8474,
		Fingerprint: "sha256:" + strings.Repeat("1c", v2PinBytes),
		Token:       "tkt_" + strings.Repeat("a5", v2TicketBytes),
	}
}

// TestPairingV2RoundTrip is the shape contract: one grouped payload, no fragment,
// six groups of five, and a value that survives being re-emitted.
func TestPairingV2RoundTrip(t *testing.T) {
	for _, host := range []string{"gw.example.com", "203.0.113.7", "2001:db8::1", "gw"} {
		t.Run(host, func(t *testing.T) {
			in := v2fixture(host)
			s := in.String()
			if !strings.Contains(s, "/v2/pair/") {
				t.Errorf("v2 parts must emit the v2 shape, got %q", s)
			}
			if strings.Contains(s, "#") {
				t.Errorf("v2 carries the pin in the payload, not a fragment: %q", s)
			}
			payload := s[strings.Index(s, "/v2/pair/")+len("/v2/pair/"):]
			groups := strings.Split(payload, "-")
			if len(groups) != v2Symbols/v2GroupLen {
				t.Errorf("payload %q has %d groups, want %d", payload, len(groups), v2Symbols/v2GroupLen)
			}
			for _, g := range groups {
				if len(g) != v2GroupLen {
					t.Errorf("group %q is %d characters, want %d", g, len(g), v2GroupLen)
				}
			}
			got, err := ParsePairingCode(s)
			if err != nil {
				t.Fatalf("parse %q: %v", s, err)
			}
			if got != in {
				t.Fatalf("round trip: got %+v want %+v", got, in)
			}
		})
	}
}

// The checksum exists for exactly one reason: a human mistyped one character, and
// should be told so here rather than by a connection that fails somewhere else a
// minute later. So every single-character change to a real code must be caught —
// including changes to the address, which is why the checksum covers host and port.
//
// A failure here is not necessarily a bug in the packing: 14 bits let roughly one
// damaged code in 16384 through by construction. If one escapes, this fixture hit
// that blind spot — change v2fixture's bytes, never this assertion.
func TestPairingV2CatchesASingleCharacterTypo(t *testing.T) {
	in := v2fixture("gw.example.com")
	s := in.String()
	at := strings.Index(s, "/v2/pair/") + len("/v2/pair/")
	payload := s[at:]

	for i, orig := range []byte(payload) {
		if orig == '-' {
			continue
		}
		for _, sub := range []byte(crockfordLower) {
			if sub == orig {
				continue
			}
			damaged := []byte(payload)
			damaged[i] = sub
			if _, err := ParsePairingCode(s[:at] + string(damaged)); err == nil {
				t.Errorf("accepted a code with %q at position %d changed to %q", string(orig), i, string(sub))
			}
		}
	}

	// The address is part of what the checksum protects: a code pasted against a
	// hostname someone edited names a machine the gateway never vouched for.
	for _, host := range []string{"gw.exarnple.com", "gw.example.co", "gw.example.com."} {
		if _, err := ParsePairingCode(strings.Replace(s, "gw.example.com", host, 1)); err == nil {
			t.Errorf("accepted a code whose host was changed to %q", host)
		}
	}
	if _, err := ParsePairingCode(strings.Replace(s, ":8474", ":8475", 1)); err == nil {
		t.Errorf("accepted a code whose port was changed")
	}
}

// A code is transcribed by humans over voice, chat and paper, so the parser folds
// the ways it comes back: any case, any grouping, and Crockford's confusable
// letters. The fold is lossless — the alphabet has no i, l, o or u to overwrite.
func TestPairingV2NormalizesHowItWasTyped(t *testing.T) {
	in := v2fixture("gw.example.com")
	s := in.String()
	at := strings.Index(s, "/v2/pair/") + len("/v2/pair/")
	head, payload := s[:at], s[at:]

	// The fixture's own payload must exercise the confusable fold, or the "typed
	// an I for a 1" cases below prove nothing.
	if !strings.ContainsAny(payload, "01") {
		t.Fatalf("fixture payload %q has no 0 or 1 — pick fixture bytes that do", payload)
	}
	confusable := strings.NewReplacer("1", "I", "0", "O").Replace(payload)

	for name, typed := range map[string]string{
		"upper case":     strings.ToUpper(payload),
		"no groups":      strings.ReplaceAll(payload, "-", ""),
		"spaced groups":  strings.ReplaceAll(payload, "-", " "),
		"confusables":    confusable,
		"trailing space": payload + " ",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParsePairingCode(head + typed)
			if err != nil {
				t.Fatalf("rejected %q: %v", typed, err)
			}
			if got != in {
				t.Fatalf("got %+v want %+v", got, in)
			}
		})
	}
}

// A payload of the wrong length is a truncated or doubled paste, not a typo, and it
// is refused before any bit is unpacked.
func TestPairingV2RejectsWrongLength(t *testing.T) {
	in := v2fixture("gw.example.com")
	s := in.String()
	at := strings.Index(s, "/v2/pair/") + len("/v2/pair/")
	payload := strings.ReplaceAll(s[at:], "-", "")

	for name, typed := range map[string]string{
		"one short": payload[:len(payload)-1],
		"one long":  payload + "7",
		"doubled":   payload + payload,
		"empty":     "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePairingCode(s[:at] + typed); err == nil {
				t.Errorf("accepted a %s payload %q", name, typed)
			}
		})
	}
}

// TestIsPairingURL covers the cheap scheme sniff the OS deep-link router uses: it
// matches the pxf:// scheme (tolerating copy-paste whitespace) without fully
// validating, so a malformed link still routes to the pairing UI to show its error.
func TestIsPairingURL(t *testing.T) {
	fp := "sha256:" + strings.Repeat("ab", 32)
	cases := map[string]bool{
		"pxf://gw:8474/v1/pair/tok#" + fp:         true,
		"  pxf://gw:8474/v1/pair/tok#" + fp + " ": true,
		"pxf://malformed":                         true, // scheme match, not full validation
		"https://gw:8474/v1/pair/tok#" + fp:       false,
		"pf1://gw:8474/tok#" + fp:                 false, // legacy scheme is no longer ours
		"pxfnope":                                 false,
		"":                                        false,
	}
	for in, want := range cases {
		if got := IsPairingURL(in); got != want {
			t.Errorf("IsPairingURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoadOrCreateCertPersists(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fp1, "sha256:") {
		t.Fatalf("fingerprint format: %q", fp1)
	}
	_, fp2, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("cert must persist across loads: %q != %q", fp1, fp2)
	}
	_, fp3, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if fp3 == fp1 {
		t.Fatal("different dirs must generate different certs")
	}
}

// TestPinnedTLSHandshake proves an agent connects when the pin matches and
// refuses when it doesn't.
func TestPinnedTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	cert, fp, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", GatewayTLSConfig(cert))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				c.Read(make([]byte, 1)) // drive the handshake
				c.Close()
			}(c)
		}
	}()

	dial := func(pin string) error {
		d := net.Dialer{Timeout: 5 * time.Second}
		raw, err := d.Dial("tcp", ln.Addr().String())
		if err != nil {
			return err
		}
		defer raw.Close()
		tc := tls.Client(raw, AgentTLSConfig(pin))
		defer tc.Close()
		tc.SetDeadline(time.Now().Add(5 * time.Second))
		return tc.Handshake()
	}

	if err := dial(fp); err != nil {
		t.Fatalf("handshake with correct pin failed: %v", err)
	}
	wrong := "sha256:" + strings.Repeat("00", 32)
	if err := dial(wrong); err == nil {
		t.Fatal("handshake with wrong pin must fail")
	} else if !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("expected fingerprint mismatch error, got: %v", err)
	}
}

func TestBackoffGrowsAndResets(t *testing.T) {
	b := &Backoff{Base: time.Second, Max: 30 * time.Second, StableAfter: time.Minute}
	seen := make([]time.Duration, FastRetries+6)
	for i := range seen {
		seen[i] = b.Next()
	}
	// The fast tier prepends FastRetries attempts; the curve then starts at Base
	// (backoff_test.go covers the tier itself and its window).
	for i, d := range seen {
		ceil := 3 * FastDelay / 2
		if i >= FastRetries {
			ceil = min(time.Second<<(i-FastRetries), 30*time.Second)
		}
		if d < 1 || d > ceil {
			t.Errorf("attempt %d: delay %v outside (0, %v]", i, d, ceil)
		}
	}
	// A short-lived connection must not reset the sequence.
	b.ConnectionEnded(2 * time.Second)
	if d := b.Next(); d > 30*time.Second {
		t.Errorf("delay after flap: %v", d)
	}
	// A stable connection resets to base.
	b.ConnectionEnded(2 * time.Minute)
	for i := 0; i < 20; i++ {
		b2 := *b
		if d := b2.Next(); d > time.Second {
			t.Fatalf("after stable reset, first delay should be <= base: %v", d)
		}
	}
}
