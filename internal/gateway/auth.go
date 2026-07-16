package gateway

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
)

// Identity is what a Validator resolves a hello to. AgentID is the display label;
// PubKey is the canonical cryptographic identity and is non-nil only when the agent
// authenticated per-identity (not via the legacy shared token). Scope limits which
// ports and tunnels the agent may bind.
type Identity struct {
	AgentID string
	PubKey  ed25519.PublicKey
	Scope   Scope
}

// Validator authenticates a hello on both the control and data accept paths.
// gatewayCertFP is this gateway's pinned certificate fingerprint; a per-agent
// validator checks the agent's proof-of-possession signature against it.
type Validator interface {
	Validate(hello *control.Hello, gatewayCertFP string) (Identity, error)
}

// Auth failures. The accept paths map these to HelloErr codes.
var (
	ErrBadToken       = errors.New("bad token")
	ErrMissingAgentID = errors.New("missing agentId")
	ErrRevoked        = errors.New("agent identity revoked")
)

// sharedTokenValidator is the legacy authenticator: one token admits every agent,
// told apart only by the self-asserted agentID. Retained as a migration fallback,
// gated by Gateway.AcceptSharedToken.
type sharedTokenValidator struct {
	token string
}

func (v sharedTokenValidator) Validate(hello *control.Hello, _ string) (Identity, error) {
	if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(v.token)) != 1 {
		return Identity{}, ErrBadToken
	}
	if hello.AgentID == "" {
		return Identity{}, ErrMissingAgentID
	}
	return Identity{AgentID: hello.AgentID}, nil
}

// identityValidator authenticates an agent by its Ed25519 public key. It first
// verifies the proof-of-possession signature against the gateway fingerprint, then
// either enrolls the key (first contact, carrying a valid ticket) or checks it
// against the allowlist. A revoked key returns ErrRevoked so the agent stops rather
// than retry-hammering.
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
	return Identity{AgentID: rec.AgentID, PubKey: pub, Scope: rec.Scope}, nil
}

// compositeValidator prefers per-agent identity and falls back to the shared token
// during migration. shared is nil once shared-token acceptance is turned off, at
// which point a keyless (legacy) hello is rejected.
type compositeValidator struct {
	identity identityValidator
	shared   *sharedTokenValidator
}

func (v compositeValidator) Validate(hello *control.Hello, fp string) (Identity, error) {
	if len(hello.AgentPubKey) > 0 {
		id, err := v.identity.Validate(hello, fp)
		switch {
		case err == nil, errors.Is(err, ErrRevoked):
			// Authenticated, or a definitive revocation — never mask either.
			return id, err
		case errors.Is(err, ErrTicketUnknown), errors.Is(err, ErrTicketConsumed), errors.Is(err, ErrTicketExpired):
			// An explicit enrollment attempt failed; surface it, don't fall back.
			return id, err
		}
		// Unknown key or bad signature and no ticket: during migration, fall back to
		// the shared token if the agent offered one and the gateway still accepts it.
		if v.shared != nil && hello.Token != "" {
			return v.shared.Validate(hello, fp)
		}
		return id, err
	}
	if v.shared != nil {
		return v.shared.Validate(hello, fp)
	}
	return Identity{}, ErrBadToken
}
