package gateway

import (
	"crypto/subtle"
	"errors"

	"proxyforward/internal/control"
)

// Identity is what a Validator resolves a hello to. Today it carries only the
// self-asserted agentID; a later per-agent-credential validator will bind it to
// a real principal without changing the accept paths or the wire.
type Identity struct {
	AgentID string
}

// Validator authenticates a hello on both the control and data accept paths.
// The credential lives in Hello.Token (an opaque string); v1 checks a shared
// token, so per-agent identity + revocation later is a validator swap plus an
// allowlist, not another handshake rewrite.
type Validator interface {
	Validate(hello *control.Hello) (Identity, error)
}

// Sentinel auth failures. The accept paths map these to HelloErr codes.
var (
	ErrBadToken       = errors.New("bad token")
	ErrMissingAgentID = errors.New("missing agentId")
)

// sharedTokenValidator is the v1 authenticator: one token admits every agent,
// told apart only by the self-asserted agentID.
type sharedTokenValidator struct {
	token string
}

func (v sharedTokenValidator) Validate(hello *control.Hello) (Identity, error) {
	if subtle.ConstantTimeCompare([]byte(hello.Token), []byte(v.token)) != 1 {
		return Identity{}, ErrBadToken
	}
	if hello.AgentID == "" {
		return Identity{}, ErrMissingAgentID
	}
	return Identity{AgentID: hello.AgentID}, nil
}
