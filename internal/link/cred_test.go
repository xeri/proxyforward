package link

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

// TestEnrollTicket: a minted enrollment ticket is self-describing (tkt_ prefix)
// so a pasted pairing code routes to per-identity enrollment, while a bare-hex
// legacy shared token does not; two mints never collide. (identity, enroll)
func TestEnrollTicket(t *testing.T) {
	a, err := NewEnrollTicket()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, EnrollTicketPrefix) {
		t.Fatalf("ticket %q missing %q prefix", a, EnrollTicketPrefix)
	}
	if !IsEnrollTicket(a) {
		t.Fatalf("IsEnrollTicket(%q) = false, want true", a)
	}
	// A legacy shared token is bare hex — never mistaken for an enrollment ticket.
	if IsEnrollTicket("3f8a1c9e2b7d4056a1b2c3d4e5f60718") {
		t.Fatalf("a bare-hex shared token must not read as an enrollment ticket")
	}
	b, err := NewEnrollTicket()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two minted tickets collided: %q", a)
	}
}

// TestAgentIDDerivation: the agentID is deterministic for a given public key,
// carries the agt_ prefix, and two distinct keys never render the same ID.
// (identity)
func TestAgentIDDerivation(t *testing.T) {
	pub1, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	id1 := AgentID(pub1)
	if id1 != AgentID(pub1) {
		t.Fatal("agentID must be deterministic for the same key")
	}
	if !strings.HasPrefix(id1, "agt_") {
		t.Fatalf("agentID must be prefixed agt_: %q", id1)
	}
	if id1 == AgentID(pub2) {
		t.Fatalf("distinct keys must derive distinct agentIDs (both %q)", id1)
	}
}

// TestAgentIDAlphabet: the fingerprint is lowercase Crockford base32, so it never
// contains the confusable i/l/o/u. (identity)
func TestAgentIDAlphabet(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fp := strings.TrimPrefix(AgentID(pub), "agt_")
	const allowed = "0123456789abcdefghjkmnpqrstvwxyz"
	for _, r := range fp {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("fingerprint char %q is not lowercase Crockford base32", r)
		}
	}
}

// TestAgentIDIsWideEnoughToBeUnforgeable pins the agentID's width as the security
// parameter it is. The gateway keys supersede, revocation, scope, and config on this
// label, so its width *is* the cost of forging a second key that answers to a
// victim's name. At the original 40 bits that search was ~2^40 keygens — hours of
// rented CPU, which is not a threat model, it's a budget line. Anything below 80
// bits here puts that attack back on the table. Asserts the floor, not the exact
// encoding. (identity)
func TestAgentIDIsWideEnoughToBeUnforgeable(t *testing.T) {
	if bits := agentFingerprintBytes * 8; bits < 80 {
		t.Fatalf("agentID carries %d bits; below 80 a chosen-id collision is purchasable", bits)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// The rendered tag must actually carry those bits (base32 packs 5 bits/char).
	fp := strings.TrimPrefix(AgentID(pub), "agt_")
	if got := len(fp) * 5; got < agentFingerprintBytes*8 {
		t.Fatalf("rendered agentID carries only %d bits, want >= %d", got, agentFingerprintBytes*8)
	}
}

// TestGatewayIDDerivation: the gateway's display ID derives deterministically from
// its cert DER with the gw_ prefix. (identity)
func TestGatewayIDDerivation(t *testing.T) {
	der := []byte("some-certificate-der-bytes")
	id := GatewayID(der)
	if !strings.HasPrefix(id, "gw_") {
		t.Fatalf("gatewayID must be prefixed gw_: %q", id)
	}
	if id != GatewayID(der) {
		t.Fatal("gatewayID must be deterministic")
	}
}

// TestTunnelID: slugs the human name, suffixes -2/-3… past collisions, and falls
// back to tnl_tunnel for an empty name. (identity)
func TestTunnelID(t *testing.T) {
	never := func(string) bool { return false }
	cases := []struct {
		name  string
		taken map[string]bool
		want  string
	}{
		{"Survival SMP", nil, "tnl_survival-smp"},
		{"  Creative!!  Realm  ", nil, "tnl_creative-realm"},
		{"", nil, "tnl_tunnel"},
		{"Survival SMP", map[string]bool{"tnl_survival-smp": true, "tnl_survival-smp-2": true}, "tnl_survival-smp-3"},
	}
	for _, c := range cases {
		taken := never
		if c.taken != nil {
			taken = func(s string) bool { return c.taken[s] }
		}
		if got := TunnelID(c.name, taken); got != c.want {
			t.Errorf("TunnelID(%q): got %q want %q", c.name, got, c.want)
		}
	}
}

// TestAgentAuthSignVerify: a proof-of-possession signature verifies only for the
// signing key and the exact gateway fingerprint it was bound to; a different key,
// a different fingerprint, or a tampered signature all fail. (auth)
func TestAgentAuthSignVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const fp = "sha256:" + "deadbeef"

	sig := SignAgentAuth(priv, fp)
	if !VerifyAgentAuth(pub, fp, sig) {
		t.Fatal("valid signature must verify")
	}
	if VerifyAgentAuth(otherPub, fp, sig) {
		t.Fatal("signature must not verify under a different key")
	}
	if VerifyAgentAuth(pub, "sha256:other", sig) {
		t.Fatal("signature bound to one gateway must not verify for another")
	}
	bad := append([]byte(nil), sig...)
	bad[0] ^= 0xff
	if VerifyAgentAuth(pub, fp, bad) {
		t.Fatal("tampered signature must not verify")
	}
	if VerifyAgentAuth([]byte{1, 2, 3}, fp, sig) {
		t.Fatal("malformed public key must not panic or verify")
	}
}

// TestLoadOrCreateIdentityPersists: the same dir re-derives one identity; a fresh
// dir mints a different one. (identity)
func TestLoadOrCreateIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	_, pub1, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, pub2, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !pub1.Equal(pub2) {
		t.Fatal("agent identity must persist across loads")
	}
	_, pub3, err := LoadOrCreateIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if pub1.Equal(pub3) {
		t.Fatal("different dirs must generate different identities")
	}
}
