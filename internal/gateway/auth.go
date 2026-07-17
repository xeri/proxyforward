package gateway

import (
	"crypto/ed25519"
	"errors"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
)

// Identity is what a Validator resolves a hello to: the key-derived agt_ display
// label, the Ed25519 public key that is the agent's canonical identity, and the
// Scope limiting which ports and tunnels it may bind.
type Identity struct {
	AgentID string
	PubKey  ed25519.PublicKey
	Scope   Scope
	// JustEnrolled marks the hello that consumed a ticket and admitted a new
	// key; EnrolledVia is that invite's label/handle. Admission records the
	// enrollment event from these.
	JustEnrolled bool
	EnrolledVia  string
}

// Validator authenticates a hello on both the control and data accept paths.
// gatewayCertFP is this gateway's pinned certificate fingerprint; the validator
// checks the agent's proof-of-possession signature against it.
type Validator interface {
	Validate(hello *control.Hello, gatewayCertFP string) (Identity, error)
}

// Auth failures. The accept paths map these to HelloErr codes.
var (
	ErrBadToken = errors.New("bad token")
	ErrRevoked  = errors.New("agent identity revoked")
)

// identityValidator is the only authenticator: an agent is its Ed25519 public
// key. It first verifies the proof-of-possession signature against the gateway
// fingerprint, then either enrolls the key (first contact, carrying a valid
// ticket) or checks it against the allowlist. A revoked key returns ErrRevoked so
// the agent stops rather than retry-hammering.
type identityValidator struct {
	store *AgentStore
	now   func() time.Time
}

func (v identityValidator) Validate(hello *control.Hello, fp string) (Identity, error) {
	pub := ed25519.PublicKey(hello.AgentPubKey)
	if !link.VerifyAgentAuth(pub, fp, hello.AgentSig) {
		return Identity{}, ErrBadToken
	}
	// Already enrolled: authenticate by the allowlist and ignore any (possibly
	// spent) ticket the agent still carries, so a reconnect never re-consumes it.
	if rec, ok := v.store.Lookup(pub); ok {
		if rec.Revoked {
			return Identity{}, ErrRevoked
		}
		return Identity{AgentID: rec.AgentID, PubKey: pub, Scope: rec.Scope}, nil
	}
	// Not enrolled: a valid ticket is required to join the allowlist.
	if hello.EnrollTicket == "" {
		return Identity{}, ErrBadToken
	}
	rec, err := v.store.Enroll(pub, link.AgentID(pub), hello.EnrollTicket, v.now())
	if err != nil {
		return Identity{}, err // ErrTicket* — surfaced with its own message
	}
	return Identity{AgentID: rec.AgentID, PubKey: pub, Scope: rec.Scope, JustEnrolled: true, EnrolledVia: rec.EnrolledVia}, nil
}
