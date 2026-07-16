package link

// This file owns the agent's cryptographic identity — a long-term Ed25519 keypair
// generated once and persisted on the machine — and the human-facing IDs derived
// from it (agt_/gw_/tnl_). Because an agent's ID is derived from its public key it
// is stable across re-pairs and cannot be forged by a mere token holder; the
// gateway allowlists the raw public key, and these rendered strings are display
// labels over that canonical identity.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnrollTicketPrefix marks a pairing code's token as a single-use enrollment
// ticket, as opposed to the legacy shared gateway token (bare hex). The token is
// thus self-describing — the agent routes a pasted code to per-identity enrollment
// or shared-token auth without a second field in the code — mirroring the typed
// agt_/gw_/tnl_ ids above. It must stay distinct from a 32-hex shared token.
const EnrollTicketPrefix = "tkt_"

// NewEnrollTicket mints a random single-use enrollment ticket (128-bit nonce)
// carrying the tkt_ prefix. The gateway stores it and embeds it in a pairing code;
// the agent replays it once to join the allowlist.
func NewEnrollTicket() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate enrollment ticket: %w", err)
	}
	return EnrollTicketPrefix + hex.EncodeToString(raw[:]), nil
}

// IsEnrollTicket reports whether a pairing-code token is an enrollment ticket
// rather than a legacy shared token, so a pasted code routes to the right auth.
func IsEnrollTicket(token string) bool {
	return strings.HasPrefix(token, EnrollTicketPrefix)
}

// crockfordLower is Crockford base32 (no confusable i/l/o/u) lowercased, so derived
// IDs read like modern API keys. It is frozen: changing the alphabet would silently
// rename every already-issued ID.
const crockfordLower = "0123456789abcdefghjkmnpqrstvwxyz"

var idEncoding = base32.NewEncoding(crockfordLower).WithPadding(base32.NoPadding)

// fingerprint renders the first 40 bits of sha256(seed) as an 8-char base32 tag.
// 40 bits is a *display* budget, sized to tell a home fleet's surfaces apart at a
// glance — it is deliberately not used where a label carries authority. GatewayID
// is its only caller: the gateway's trust rests on the full sha256 pin carried in
// the pairing code (cert.go Fingerprint), so gw_ is a name, never a credential.
// Contrast AgentID, which the gateway keys authorization on and which therefore
// takes agentFingerprintBytes.
func fingerprint(seed []byte) string {
	sum := sha256.Sum256(seed)
	return idEncoding.EncodeToString(sum[:5])
}

// agentFingerprintBytes is how much of sha256(pubkey) an agentID carries. It is a
// security parameter, not a display choice: the gateway keys supersede, revocation,
// scope, and gateway-authoritative config on this label, so anyone who can find a
// second key hashing to the same one inherits the victim's authority. At the
// original 5 bytes (40 bits) that search cost ~2^40 keygens — hours on one rented
// machine, i.e. forgeable. 10 bytes puts it at 2^80, which no amount of money buys,
// and still renders as a 16-char tag. AgentStore.Enroll enforces uniqueness besides,
// so even a found collision fails closed instead of silently sharing an identity.
const agentFingerprintBytes = 10

// AgentID derives an agent's stable public identity from its Ed25519 public key:
// agt_<base32 fingerprint>. Derived rather than stored, so the same machine always
// re-derives the same ID and no two keys render alike.
func AgentID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "agt_" + idEncoding.EncodeToString(sum[:agentFingerprintBytes])
}

// GatewayID derives the gateway's display identity from its (pinned) certificate
// DER, giving the UI a stable gw_ name without a second gateway key.
func GatewayID(certDER []byte) string {
	return "gw_" + fingerprint(certDER)
}

// TunnelID renders a tunnel identity from its human name (tnl_survival-smp),
// suffixing -2, -3… until taken reports the candidate free. An empty name falls
// back to tnl_tunnel.
func TunnelID(name string, taken func(string) bool) string {
	s := slug(name)
	if s == "" {
		s = "tunnel"
	}
	base := "tnl_" + s
	cand := base
	for n := 2; taken(cand); n++ {
		cand = fmt.Sprintf("%s-%d", base, n)
	}
	return cand
}

// slug lowercases name and keeps only [a-z0-9], collapsing every other run into a
// single hyphen and trimming hyphens at the ends.
func slug(name string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// LoadOrCreateIdentity returns the agent's long-term Ed25519 identity, generating
// one on first run and persisting the PKCS#8 private key under dir (0600). The
// public half is what the gateway allowlists; the private key never leaves the
// machine. A corrupt or non-Ed25519 key file is a fatal, actionable error rather
// than a silent regeneration (which would orphan the agent's allowlist entry).
func LoadOrCreateIdentity(dir string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	keyPath := filepath.Join(dir, "agent_identity.key")

	pemBytes, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		block, _ := pem.Decode(pemBytes)
		if block == nil {
			return nil, nil, fmt.Errorf("agent identity key at %s is not valid PEM (delete it to regenerate, then re-pair)", keyPath)
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("parse agent identity key: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("agent identity key at %s is not Ed25519 (delete it to regenerate, then re-pair)", keyPath)
		}
		return priv, priv.Public().(ed25519.PublicKey), nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("read agent identity key: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate agent identity: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal agent identity: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create identity dir: %w", err)
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(keyPath, out, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write agent identity key: %w", err)
	}
	return priv, pub, nil
}

// AgentAuthMessage is the exact byte string an agent signs with its identity key to
// prove possession to a specific gateway. Binding to the gateway's pinned cert
// fingerprint means a signature made for one gateway cannot be replayed to another;
// the pinned TLS 1.3 channel already rules out capture by anyone but a compromised
// endpoint, which would hold the private key regardless — so no per-connection
// nonce is needed, and the identical message works over both TCP and QUIC.
func AgentAuthMessage(gatewayCertFP string) []byte {
	return append([]byte("proxyforward-agent-auth-v1\x00"), gatewayCertFP...)
}

// SignAgentAuth signs AgentAuthMessage with the agent's identity key.
func SignAgentAuth(priv ed25519.PrivateKey, gatewayCertFP string) []byte {
	return ed25519.Sign(priv, AgentAuthMessage(gatewayCertFP))
}

// VerifyAgentAuth reports whether sig proves possession of pub's private key, bound
// to gatewayCertFP. Safe on a malformed key or signature (returns false, no panic).
func VerifyAgentAuth(pub ed25519.PublicKey, gatewayCertFP string, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, AgentAuthMessage(gatewayCertFP), sig)
}
