// Package secureio creates the directories that hold proxyforward's secrets with
// permissions the host OS actually enforces.
//
// The rest of the tree writes its secrets with Unix modes — `os.WriteFile(key,
// pem, 0o600)` in link/cred.go and link/cert.go, `0o600` in the agent store's
// atomicWriteFile. On the platform proxyforward actually ships to those modes are
// a comment: Go maps a Windows file's mode to nothing but the read-only bit, so
// the bits that matter come from the parent directory's inherited ACL. In a
// per-user %APPDATA% that inheritance happens to be user-only and the intent
// survives by luck; under %ProgramData% (config.DefaultDir(true) — where the
// service keeps its config) it grants BUILTIN\Users read, and the gateway's TLS
// private key, the shared token, the agent store's live enrollment nonces, and the
// agent's Ed25519 identity are all readable by any local account.
//
// So the directory is the enforcement point, not the file: a protected DACL set
// once on the directory covers every secret written into it — including the
// temp-file-plus-rename paths, which never touch a mode of their own — and keeps
// covering them as new files are added. Hardening each file instead would be one
// more thing for the next writer to forget.
package secureio

// MkdirAll creates dir and any missing parents, then restricts dir to the
// accounts that need it: on Windows, SYSTEM, Administrators, and the account this
// process runs as, with inheritance from the parent severed so a permissive
// %ProgramData% grant cannot leak in. Elsewhere it is os.MkdirAll(dir, 0o700),
// where the mode is enforced natively.
//
// It is idempotent and re-applies the DACL every call, so an install created
// before this existed — or one an operator loosened by hand — is repaired the next
// time the engine starts rather than staying wide open until reinstall.
func MkdirAll(dir string) error { return mkdirAll(dir) }
