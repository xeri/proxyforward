package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
)

// enrollHello builds a per-identity hello: pubkey + proof-of-possession signature
// bound to fp, optionally carrying an enrollment ticket.
func enrollHello(t *testing.T, priv ed25519.PrivateKey, pub ed25519.PublicKey, fp, ticket string) control.Hello {
	t.Helper()
	return control.Hello{
		AgentPubKey:  pub,
		AgentSig:     link.SignAgentAuth(priv, fp),
		EnrollTicket: ticket,
	}
}

// TestIdentityValidator: a signed hello with a valid ticket enrolls; afterwards the
// keyed hello authenticates by allowlist; a revoked key is rejected with ErrRevoked;
// a bad signature or unknown key is ErrBadToken.
func TestIdentityValidator(t *testing.T) {
	const fp = "sha256:deadbeef"
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	store, err := LoadAgentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := identityValidator{store: store, now: time.Now}

	// Unknown key, no ticket → rejected.
	if _, err := v.Validate(ptr(enrollHello(t, priv, pub, fp, "")), fp); !errors.Is(err, ErrBadToken) {
		t.Fatalf("unknown key: want ErrBadToken, got %v", err)
	}

	// Enroll with a ticket.
	ticket, _ := store.IssueEnrollment(false, time.Time{}, Scope{})
	id, err := v.Validate(ptr(enrollHello(t, priv, pub, fp, ticket)), fp)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if id.AgentID != link.AgentID(pub) || id.PubKey == nil {
		t.Fatalf("enrolled identity wrong: %+v", id)
	}

	// Steady state: keyed hello, no ticket, authenticates by allowlist.
	if _, err := v.Validate(ptr(enrollHello(t, priv, pub, fp, "")), fp); err != nil {
		t.Fatalf("steady-state auth: %v", err)
	}

	// Wrong fingerprint invalidates the proof of possession.
	if _, err := v.Validate(ptr(enrollHello(t, priv, pub, fp, "")), "sha256:other"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("wrong fingerprint: want ErrBadToken, got %v", err)
	}

	// Revoke → ErrRevoked.
	if !store.Revoke(link.AgentID(pub)) {
		t.Fatal("revoke failed")
	}
	if _, err := v.Validate(ptr(enrollHello(t, priv, pub, fp, "")), fp); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: want ErrRevoked, got %v", err)
	}
}

func ptr(h control.Hello) *control.Hello { return &h }
