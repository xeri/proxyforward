//go:build windows

package svc

import (
	"os"
	"testing"
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
