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

// TestSharedTokenValidator characterizes the v1 authenticator: a constant-time
// shared-token compare plus a non-empty agentID check, returning a typed
// Identity or a sentinel error the accept paths map to a HelloErr code.
func TestSharedTokenValidator(t *testing.T) {
	v := sharedTokenValidator{token: "s3cret"}

	cases := []struct {
		name    string
		hello   control.Hello
		wantID  string
		wantErr error
	}{
		{"good token and agentID", control.Hello{Token: "s3cret", AgentID: "agent-1"}, "agent-1", nil},
		{"wrong token", control.Hello{Token: "nope", AgentID: "agent-1"}, "", ErrBadToken},
		{"empty token", control.Hello{Token: "", AgentID: "agent-1"}, "", ErrBadToken},
		{"good token but empty agentID", control.Hello{Token: "s3cret", AgentID: ""}, "", ErrMissingAgentID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := v.Validate(&tc.hello, "")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if id.AgentID != tc.wantID {
				t.Fatalf("identity agentID = %q, want %q", id.AgentID, tc.wantID)
			}
		})
	}
}

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

// TestCompositeValidator: a keyed hello takes the identity path; a keyless hello
// falls back to the shared token when enabled, and is rejected when it is nil.
func TestCompositeValidator(t *testing.T) {
	const fp = "sha256:deadbeef"
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	store, _ := LoadAgentStore(t.TempDir())
	ticket, _ := store.IssueEnrollment(false, time.Time{}, Scope{})
	if _, err := store.Enroll(pub, link.AgentID(pub), ticket, time.Now()); err != nil {
		t.Fatal(err)
	}

	withShared := compositeValidator{
		identity: identityValidator{store: store, now: time.Now},
		shared:   &sharedTokenValidator{token: "s3cret"},
	}
	// Keyed hello → identity path.
	if id, err := withShared.Validate(ptr(enrollHello(t, priv, pub, fp, "")), fp); err != nil || id.PubKey == nil {
		t.Fatalf("keyed hello via composite: id=%+v err=%v", id, err)
	}
	// Keyless hello with the shared token → fallback path.
	if id, err := withShared.Validate(&control.Hello{Token: "s3cret", AgentID: "legacy"}, fp); err != nil || id.AgentID != "legacy" {
		t.Fatalf("shared fallback: id=%+v err=%v", id, err)
	}

	// A keyed hello for an UNenrolled key with no ticket falls back to the shared
	// token during migration (an existing agent now also sends its pubkey).
	upub, upriv, _ := ed25519.GenerateKey(rand.Reader)
	h := enrollHello(t, upriv, upub, fp, "")
	h.Token, h.AgentID = "s3cret", "legacy2"
	if id, err := withShared.Validate(&h, fp); err != nil || id.AgentID != "legacy2" || id.PubKey != nil {
		t.Fatalf("migration fallback for unenrolled key: id=%+v err=%v", id, err)
	}

	// With shared acceptance off, a keyless hello is rejected outright.
	noShared := compositeValidator{identity: identityValidator{store: store, now: time.Now}}
	if _, err := noShared.Validate(&control.Hello{Token: "s3cret", AgentID: "legacy"}, fp); !errors.Is(err, ErrBadToken) {
		t.Fatalf("shared off: want ErrBadToken, got %v", err)
	}
}

func ptr(h control.Hello) *control.Hello { return &h }
