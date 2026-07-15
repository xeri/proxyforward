package gateway

import (
	"errors"
	"testing"

	"proxyforward/internal/control"
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
			id, err := v.Validate(&tc.hello)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if id.AgentID != tc.wantID {
				t.Fatalf("identity agentID = %q, want %q", id.AgentID, tc.wantID)
			}
		})
	}
}
