//go:build windows

package svc

// Autostart is the "start when I sign in" half of proxyforward's two answers to
// "what brings this up without a person launching it". The other is the Windows
// service (service.go), which is the better answer for a gateway: it starts at
// boot, needs no sign-in, and survives logout. Autostart is the answer for a
// desktop the owner actually logs into — and, unlike the service, it costs no
// elevation, because HKCU is the user's own hive.
//
// Both can be on at once, and nothing here stops that: the GUI is where that
// overlap is explained (app/../Settings.tsx System section), because refusing it
// in the registry layer would be a policy the user cannot override.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// autostartKey is the per-user Run key: Explorer runs each value under it once,
// at sign-in, for this user only. HKCU rather than HKLM because autostart is a
// preference of the person who set it, not machine policy.
//
// It is a variable, not a const, so the test can point the reconcile at a
// throwaway subkey — a test that flipped the developer's real Run key would
// leave the machine changed after it passed.
var autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// AutostartEnabled reports whether signing in launches proxyforward — that is,
// whether our value exists under the Run key at all.
//
// A value naming a moved or deleted executable still counts as enabled: Windows
// will still try, so the honest answer to "does this start with me?" is yes.
// SetAutostart is what repairs the command it holds.
func AutostartEnabled() (bool, error) {
	_, present, err := autostartValue()
	return present, err
}

// SetAutostart reconciles the Run key with want, and reports whether the
// registry actually changed.
//
// Reconcile, never blind-write: the Run key is shared state. An uninstaller may
// have removed our value, a user may have edited it by hand, and this build may
// sit at a different path than the one that wrote it. So enabling an entry that
// is already correct writes nothing, enabling a stale or absent one (re)writes
// it, and disabling one that is already gone is success rather than an error.
// The bool is what a caller can honestly report: "already true" and "I fixed it"
// are different answers, and the test asserts they stay different.
func SetAutostart(want bool) (changed bool, err error) {
	current, present, err := autostartValue()
	if err != nil {
		return false, err
	}

	if !want {
		if !present {
			return false, nil // desired state already holds
		}
		k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
		if err != nil {
			return false, fmt.Errorf("open startup key for writing: %w", err)
		}
		defer func() { _ = k.Close() }()
		if err := k.DeleteValue(AutostartValueName); err != nil {
			if errors.Is(err, registry.ErrNotExist) {
				return false, nil // removed between the read and the write
			}
			return false, fmt.Errorf("remove startup entry: %w", err)
		}
		return true, nil
	}

	cmd, err := autostartCommand()
	if err != nil {
		return false, err
	}
	if present && sameCommand(current, cmd) {
		return false, nil
	}
	// CreateKey rather than OpenKey: HKCU's Run key exists on every normal
	// install but is not guaranteed to, and a profile missing it should get the
	// entry rather than an error. On an existing key CreateKey just opens it.
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("open startup key for writing: %w", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.SetStringValue(AutostartValueName, cmd); err != nil {
		return false, fmt.Errorf("write startup entry: %w", err)
	}
	return true, nil
}

// autostartValue reads our Run value: its contents, and whether it is there at
// all. A missing Run key, a missing value, and a value of the wrong type all
// answer the same question — the last as "present, but not something we wrote",
// which is exactly the case SetAutostart repairs.
func autostartValue() (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open startup key: %w", err)
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(AutostartValueName)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		if errors.Is(err, registry.ErrUnexpectedType) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("read startup entry: %w", err)
	}
	return v, true, nil
}

// autostartCommand is what the value should hold: this executable, symlinks
// resolved, quoted.
//
// The quotes are not cosmetic. CreateProcess splits an unquoted
// C:\Program Files\proxyforward\proxyforward.exe at the space and looks for
// C:\Program — which is how a startup entry silently does nothing on exactly
// the installs most people have.
func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate own executable: %w", err)
	}
	// Register the real path: a value naming a link stops resolving the moment
	// the link is replaced. Best effort only — EvalSymlinks fails on paths that
	// are perfectly launchable, and a launchable path beats no entry.
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return `"` + exe + `"`, nil
}

// sameCommand reports whether a stored value already launches what we would
// write. Quoting and case are not differences worth a rewrite: Windows paths are
// case-insensitive, and an entry added by hand is usually unquoted. Anything
// else — a different path, extra arguments — is.
func sameCommand(stored, want string) bool {
	return strings.EqualFold(unquotePath(stored), unquotePath(want))
}

// unquotePath strips one surrounding pair of double quotes, the only quoting a
// bare-path Run value can carry.
func unquotePath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = s[1 : len(s)-1]
	}
	return s
}
