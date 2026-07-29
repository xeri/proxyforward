//go:build windows

package svc

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestFirewallRulePresent exercises the netsh plumbing (hidden window,
// exit-code detection) without needing elevation — querying rules is free.
func TestFirewallRulePresent(t *testing.T) {
	present, err := FirewallRulePresent()
	if err != nil {
		t.Fatalf("FirewallRulePresent: %v", err)
	}
	t.Logf("rule %q present: %v (elevated: %v)", FirewallRuleName, present, IsElevated())
}

// TestAddRemoveFirewallRule needs elevation; it runs in elevated dev shells
// and is skipped otherwise.
func TestAddRemoveFirewallRule(t *testing.T) {
	if !IsElevated() {
		t.Skip("not elevated")
	}
	// GitHub runners ARE elevated, so the guard above doesn't cover them. There
	// the test binary lives in a temp build dir, and netsh rejects it with "The
	// application name could not be resolved" — AddFirewallRule registers
	// os.Executable(), which in production is an installed exe, never a
	// throwaway. The netsh plumbing itself is covered by TestFirewallRulePresent.
	if os.Getenv("CI") != "" {
		t.Skip("CI: netsh will not accept a temp-dir test binary as program=")
	}
	before, err := FirewallRulePresent()
	if err != nil {
		t.Fatal(err)
	}
	if err := AddFirewallRule(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if present, _ := FirewallRulePresent(); !present {
		t.Fatal("rule missing after add")
	}
	if !before {
		// Leave the machine as we found it.
		if err := RemoveFirewallRule(); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if present, _ := FirewallRulePresent(); present {
			t.Fatal("rule still present after remove")
		}
	}
}

// useScratchRunKey points the autostart reconcile at a throwaway HKCU subkey
// for the duration of the test, and removes it afterwards. The real Run key is
// the developer's machine state: a test that flipped it would leave
// proxyforward launching at every sign-in on a machine that only ran `go test`.
// The pid keeps parallel test binaries out of each other's key.
func useScratchRunKey(t *testing.T) {
	t.Helper()
	base := fmt.Sprintf(`Software\proxyforward-test\%d`, os.Getpid())
	path := base + `\Run`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("create scratch run key: %v", err)
	}
	_ = k.Close()
	prev := autostartKey
	autostartKey = path
	t.Cleanup(func() {
		autostartKey = prev
		// DeleteKey only removes an empty key, so unwind leaf-first. Both are
		// best effort: a leftover empty key under Software is harmless, and
		// failing the run over cleanup would hide the real result.
		_ = registry.DeleteKey(registry.CURRENT_USER, path)
		_ = registry.DeleteKey(registry.CURRENT_USER, base)
	})
}

// scratchValue reads the raw contents of the scratch Run value.
func scratchValue(t *testing.T) (string, bool) {
	t.Helper()
	v, present, err := autostartValue()
	if err != nil {
		t.Fatalf("read scratch value: %v", err)
	}
	return v, present
}

// writeScratch plants a raw value under the scratch Run key, standing in for
// whatever an uninstaller, another install, or a person left behind.
func writeScratch(t *testing.T, v string) {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("open scratch run key: %v", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.SetStringValue(AutostartValueName, v); err != nil {
		t.Fatalf("plant scratch value: %v", err)
	}
}

// TestSetAutostartReconciles walks the states the Run key is actually found in:
// absent, ours, ours-but-unquoted, an older install's path, and removed behind
// our back. The contract is that only a real difference writes, and that the
// returned bool says which of the two happened.
func TestSetAutostartReconciles(t *testing.T) {
	useScratchRunKey(t)

	want, err := autostartCommand()
	if err != nil {
		t.Fatalf("autostartCommand: %v", err)
	}
	if !strings.HasPrefix(want, `"`) || !strings.HasSuffix(want, `"`) {
		t.Fatalf("command must be quoted so a path with spaces launches: %q", want)
	}

	// Absent → enable writes it.
	changed, err := SetAutostart(true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !changed {
		t.Fatal("enabling an absent entry reported no change")
	}
	if got, present := scratchValue(t); !present || got != want {
		t.Fatalf("value after enable = %q (present %v), want %q", got, present, want)
	}
	if on, err := AutostartEnabled(); err != nil || !on {
		t.Fatalf("AutostartEnabled = %v, %v; want true, nil", on, err)
	}

	// Idempotent: a second enable must not write.
	if changed, err := SetAutostart(true); err != nil || changed {
		t.Fatalf("re-enable = %v, %v; want no change", changed, err)
	}

	// A hand-added, unquoted entry names the same executable, so it is not a
	// difference — rewriting it would churn the registry on every save.
	writeScratch(t, unquotePath(want))
	if changed, err := SetAutostart(true); err != nil || changed {
		t.Fatalf("enable over an equivalent unquoted entry = %v, %v; want no change", changed, err)
	}

	// A stale entry (an older install's path) is repaired, not left alone.
	writeScratch(t, `"C:\gone\proxyforward.exe"`)
	changed, err = SetAutostart(true)
	if err != nil {
		t.Fatalf("enable over stale: %v", err)
	}
	if !changed {
		t.Fatal("a stale command was left in place")
	}
	if got, _ := scratchValue(t); got != want {
		t.Fatalf("value after repair = %q, want %q", got, want)
	}

	// Disable removes it; disabling again is success with no write.
	if changed, err := SetAutostart(false); err != nil || !changed {
		t.Fatalf("disable = %v, %v; want changed, nil", changed, err)
	}
	if on, err := AutostartEnabled(); err != nil || on {
		t.Fatalf("AutostartEnabled after disable = %v, %v; want false, nil", on, err)
	}
	if changed, err := SetAutostart(false); err != nil || changed {
		t.Fatalf("re-disable = %v, %v; want no change", changed, err)
	}

	// Removed behind our back (an uninstaller, a cleanup tool): asking for it
	// again restores it rather than trusting a remembered "already on".
	if changed, err := SetAutostart(true); err != nil || !changed {
		t.Fatalf("re-enable after external removal = %v, %v; want changed, nil", changed, err)
	}
	if changed, err := SetAutostart(false); err != nil || !changed {
		t.Fatalf("final disable = %v, %v; want changed, nil", changed, err)
	}
}

// TestAutostartToleratesAMissingRunKey covers the profile with no Run key at
// all: reading must answer "off" rather than fail, and enabling must create the
// key instead of refusing.
func TestAutostartToleratesAMissingRunKey(t *testing.T) {
	useScratchRunKey(t)
	if err := registry.DeleteKey(registry.CURRENT_USER, autostartKey); err != nil {
		t.Fatalf("remove scratch run key: %v", err)
	}
	if on, err := AutostartEnabled(); err != nil || on {
		t.Fatalf("AutostartEnabled with no Run key = %v, %v; want false, nil", on, err)
	}
	if changed, err := SetAutostart(false); err != nil || changed {
		t.Fatalf("disable with no Run key = %v, %v; want no change", changed, err)
	}
	if changed, err := SetAutostart(true); err != nil || !changed {
		t.Fatalf("enable with no Run key = %v, %v; want changed, nil", changed, err)
	}
	if on, err := AutostartEnabled(); err != nil || !on {
		t.Fatalf("AutostartEnabled after enable = %v, %v; want true, nil", on, err)
	}
	if _, err := SetAutostart(false); err != nil {
		t.Fatalf("cleanup disable: %v", err)
	}
}
