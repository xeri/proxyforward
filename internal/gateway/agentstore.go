package gateway

// AgentStore is the gateway's per-agent identity allowlist plus the outstanding
// enrollment tickets. It is the authority that replaces the shared token: the
// canonical identity of an agent is its Ed25519 public key (the map key), and the
// agt_ agentID is a derived display label. Revoking one agent removes its entry
// without touching the rest of the fleet.
//
// Concurrency: a single mutex guards both maps; every mutation persists the whole
// (small) store atomically before returning, so a crash never leaves a half-written
// allowlist. Writes happen only on pair / enroll / revoke / rename — never on the
// data path — so the coarse lock is fine.

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"proxyforward/internal/control"
	"proxyforward/internal/link"
)

const agentStoreFile = "gateway_agents.json"

// Ticket-redemption failures, surfaced to the enrolling agent as a hello error.
var (
	ErrTicketUnknown  = errors.New("pairing code not recognized (it may have been rotated)")
	ErrTicketConsumed = errors.New("this pairing code was already used — ask for a fresh one")
	ErrTicketExpired  = errors.New("this pairing code has expired — ask for a fresh one")
)

// Scope restricts which public ports and tunnel IDs an agent may bind. An empty
// list means "any" — the permissive default that preserves pre-scope behavior.
type Scope struct {
	Ports     []int    `json:"ports,omitempty"`
	TunnelIDs []string `json:"tunnelIds,omitempty"`
}

// AllowsPort reports whether p is within scope (empty Ports = unrestricted).
func (s Scope) AllowsPort(p int) bool {
	if len(s.Ports) == 0 {
		return true
	}
	for _, x := range s.Ports {
		if x == p {
			return true
		}
	}
	return false
}

// AllowsTunnel reports whether id is within scope (empty TunnelIDs = unrestricted).
func (s Scope) AllowsTunnel(id string) bool {
	if len(s.TunnelIDs) == 0 {
		return true
	}
	for _, x := range s.TunnelIDs {
		if x == id {
			return true
		}
	}
	return false
}

// AgentRecord is one enrolled agent's persisted identity plus the
// gateway-authoritative tunnel config the gateway holds for it (CapGatewayConfig).
// DesiredTunnels is the resolved desired set (concrete ports); ConfigGen is a
// per-agent monotonic generation bumped on every adopt so drift is detectable on
// each reconnect. Both zero for an agent that has never synced a config.
type AgentRecord struct {
	AgentID        string               `json:"agentId"`
	PubKey         []byte               `json:"pubKey"`
	Nickname       string               `json:"nickname,omitempty"`
	Scope          Scope                `json:"scope,omitempty"`
	IssuedAt       time.Time            `json:"issuedAt"`
	Revoked        bool                 `json:"revoked,omitempty"`
	DesiredTunnels []control.TunnelSpec `json:"desiredTunnels,omitempty"`
	ConfigGen      uint64               `json:"configGen,omitempty"`
}

// enrollNonce is one outstanding pairing ticket.
type enrollNonce struct {
	Exp      time.Time `json:"exp,omitempty"`      // zero = never expires
	Reusable bool      `json:"reusable,omitempty"` // false = single-use (the safe default)
	Scope    Scope     `json:"scope,omitempty"`
	Consumed bool      `json:"consumed,omitempty"`
}

type storeFile struct {
	Agents []AgentRecord          `json:"agents"`
	Nonces map[string]enrollNonce `json:"nonces"`
}

// AgentStore is safe for concurrent use.
type AgentStore struct {
	path   string
	mu     sync.Mutex
	agents map[string]AgentRecord // key: hex(pubkey) — the canonical identity
	nonces map[string]enrollNonce // key: ticket nonce
}

// LoadAgentStore reads the allowlist under dir, returning an empty store if none
// exists yet.
func LoadAgentStore(dir string) (*AgentStore, error) {
	s := &AgentStore{
		path:   filepath.Join(dir, agentStoreFile),
		agents: map[string]AgentRecord{},
		nonces: map[string]enrollNonce{},
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent store: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse agent store %s: %w", s.path, err)
	}
	for _, r := range f.Agents {
		s.agents[hex.EncodeToString(r.PubKey)] = r
	}
	if f.Nonces != nil {
		s.nonces = f.Nonces
	}
	return s, nil
}

// save persists the store atomically. The caller must hold s.mu.
func (s *AgentStore) save() error {
	f := storeFile{Nonces: s.nonces}
	for _, r := range s.agents {
		f.Agents = append(f.Agents, r)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent store: %w", err)
	}
	return atomicWriteFile(s.path, data, 0o600)
}

// IssueEnrollment mints a fresh pairing ticket and records it. reusable=false is
// single-use (the safe default); a zero exp never expires. Returns the opaque
// ticket nonce for embedding in the pairing code.
func (s *AgentStore) IssueEnrollment(reusable bool, exp time.Time, scope Scope) (string, error) {
	ticket, err := link.NewEnrollTicket()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces[ticket] = enrollNonce{Exp: exp, Reusable: reusable, Scope: scope}
	if err := s.save(); err != nil {
		return "", err
	}
	return ticket, nil
}

// Enroll validates ticket and binds pubKey to the allowlist under the derived
// agentID, consuming a single-use ticket. Re-enrolling an existing pubkey keeps its
// nickname. now is injected for deterministic expiry checks.
func (s *AgentStore) Enroll(pubKey []byte, agentID, ticket string, now time.Time) (AgentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nonces[ticket]
	if !ok {
		return AgentRecord{}, ErrTicketUnknown
	}
	if n.Consumed {
		return AgentRecord{}, ErrTicketConsumed
	}
	if !n.Exp.IsZero() && now.After(n.Exp) {
		return AgentRecord{}, ErrTicketExpired
	}

	key := hex.EncodeToString(pubKey)
	rec := AgentRecord{
		AgentID:  agentID,
		PubKey:   append([]byte(nil), pubKey...),
		Scope:    n.Scope,
		IssuedAt: now,
	}
	if existing, ok := s.agents[key]; ok {
		rec.Nickname = existing.Nickname // preserve a rename across re-enrollment
	}
	s.agents[key] = rec
	if !n.Reusable {
		n.Consumed = true
		s.nonces[ticket] = n
	}
	if err := s.save(); err != nil {
		return AgentRecord{}, err
	}
	return rec, nil
}

// Lookup returns the record for a public key, if enrolled.
func (s *AgentStore) Lookup(pubKey []byte) (AgentRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.agents[hex.EncodeToString(pubKey)]
	return rec, ok
}

// Revoke marks an agent revoked by agentID, reporting whether it was found. The
// record is kept (not deleted) so a revoked agent's next connect gets a clear
// "revoked" answer rather than an "unknown identity" one.
func (s *AgentStore) Revoke(agentID string) bool {
	return s.mutate(agentID, func(r *AgentRecord) { r.Revoked = true })
}

// Rename sets an agent's display nickname, reporting whether it was found.
func (s *AgentStore) Rename(agentID, nickname string) bool {
	return s.mutate(agentID, func(r *AgentRecord) { r.Nickname = nickname })
}

// SetScope replaces an agent's bind scope, reporting whether it was found.
func (s *AgentStore) SetScope(agentID string, scope Scope) bool {
	return s.mutate(agentID, func(r *AgentRecord) { r.Scope = scope })
}

// mutate applies fn to the record with the given agentID and persists.
func (s *AgentStore) mutate(agentID string, fn func(*AgentRecord)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, r := range s.agents {
		if r.AgentID == agentID {
			fn(&r)
			s.agents[key] = r
			_ = s.save()
			return true
		}
	}
	return false
}

// DesiredConfig returns the gateway-authoritative tunnel set and its generation for
// an agent. ok is false only when the agent is unknown; a known agent with no config
// yet returns (nil, 0, true) — the caller distinguishes "no config" (bootstrap) from
// "unknown agent" (reject).
func (s *AgentStore) DesiredConfig(agentID string) ([]control.TunnelSpec, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.agents {
		if r.AgentID == agentID {
			return append([]control.TunnelSpec(nil), r.DesiredTunnels...), r.ConfigGen, true
		}
	}
	return nil, 0, false
}

// AdoptConfig replaces an agent's authoritative tunnel set, bumps its generation,
// and persists. It reports false only for an unknown agent. The specs are stored
// verbatim (already resolved to concrete ports by the caller).
func (s *AgentStore) AdoptConfig(agentID string, specs []control.TunnelSpec) (uint64, bool) {
	var gen uint64
	ok := s.mutate(agentID, func(r *AgentRecord) {
		r.ConfigGen++
		r.DesiredTunnels = append([]control.TunnelSpec(nil), specs...)
		gen = r.ConfigGen
	})
	return gen, ok
}

// List returns a snapshot of all enrolled agents.
func (s *AgentStore) List() []AgentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentRecord, 0, len(s.agents))
	for _, r := range s.agents {
		out = append(out, r)
	}
	return out
}

// atomicWriteFile writes data to path via a temp file + rename, retrying the rename
// once for Windows AV scanners that briefly hold fresh files (mirrors
// setup.atomicWrite, which is unexported).
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".agents-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		time.Sleep(100 * time.Millisecond)
		if err = os.Rename(tmpName, path); err != nil {
			return err
		}
	}
	return nil
}
