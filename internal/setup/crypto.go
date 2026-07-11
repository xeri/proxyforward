package setup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
)

// KDFParams pins the argon2id parameters into the file, so future default
// changes never break old exports.
type KDFParams struct {
	Salt      []byte `json:"salt"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memoryKiB"`
	Threads   uint8  `json:"threads"`
}

// Current argon2id defaults (RFC 9106 second recommendation: 64 MiB, t=3
// would be first; t=1/64MiB is its low-memory profile — interactive-use
// appropriate here).
const (
	kdfTime      = 1
	kdfMemoryKiB = 64 * 1024
	kdfThreads   = 4
)

// Decode-side caps: KDF params come from an untrusted file, and argon2
// allocates MemoryKiB up front — without a ceiling a crafted file is an
// instant multi-GiB allocation.
const (
	kdfMaxTime      = 16
	kdfMaxMemoryKiB = 512 * 1024
	kdfMaxThreads   = 32
)

// aadFields binds the plaintext header to the ciphertext: swapping role,
// date, or version on an encrypted file makes GCM open fail.
type aadFields struct {
	App           string    `json:"app"`
	FormatVersion int       `json:"formatVersion"`
	Role          string    `json:"role"`
	AppVersion    string    `json:"appVersion"`
	ExportedAt    time.Time `json:"exportedAt"`
}

func aad(m *Manifest) []byte {
	data, err := json.Marshal(aadFields{
		App: m.App, FormatVersion: m.FormatVersion, Role: m.Role,
		AppVersion: m.AppVersion, ExportedAt: m.ExportedAt,
	})
	if err != nil {
		panic(fmt.Sprintf("setup: marshal aad: %v", err)) // struct of scalars cannot fail
	}
	return data
}

func gcm(passphrase string, k *KDFParams) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), k.Salt, k.Time, k.MemoryKiB, k.Threads, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts files into m (encrypted mode fields) using passphrase.
func seal(m *Manifest, files map[string][]byte, passphrase string) error {
	plaintext, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("marshal files: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	m.KDF = &KDFParams{Salt: salt, Time: kdfTime, MemoryKiB: kdfMemoryKiB, Threads: kdfThreads}
	aead, err := gcm(passphrase, m.KDF)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	m.Nonce = nonce
	m.Ciphertext = aead.Seal(nil, nonce, plaintext, aad(m))
	return nil
}

// open decrypts an encrypted manifest's files map.
func open(m *Manifest, passphrase string) (map[string][]byte, error) {
	k := m.KDF
	if k == nil || len(k.Salt) == 0 || len(m.Nonce) == 0 || len(m.Ciphertext) == 0 {
		return nil, ErrBadPassphrase
	}
	if k.Time < 1 || k.Time > kdfMaxTime || k.MemoryKiB < 8 || k.MemoryKiB > kdfMaxMemoryKiB || k.Threads < 1 || k.Threads > kdfMaxThreads {
		return nil, fmt.Errorf("setup file has out-of-range key-derivation parameters")
	}
	aead, err := gcm(passphrase, k)
	if err != nil {
		return nil, err
	}
	if len(m.Nonce) != aead.NonceSize() {
		return nil, ErrBadPassphrase
	}
	plaintext, err := aead.Open(nil, m.Nonce, m.Ciphertext, aad(m))
	if err != nil {
		return nil, ErrBadPassphrase
	}
	var files map[string][]byte
	if err := json.Unmarshal(plaintext, &files); err != nil {
		return nil, fmt.Errorf("setup file payload: %w", err)
	}
	return files, nil
}
