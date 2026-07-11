//go:build windows

package svc

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// netsh runs one netsh invocation without flashing a console window.
func netsh(args ...string) (string, int, error) {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	if err != nil && code < 0 {
		return string(out), code, fmt.Errorf("run netsh: %w", err)
	}
	return string(out), code, nil
}

// FirewallRulePresent reports whether the proxyforward rule exists. It needs
// no elevation. Detection uses netsh's exit code (1 = no matching rules), not
// output text, so it works on localized Windows.
func FirewallRulePresent() (bool, error) {
	_, code, err := netsh("advfirewall", "firewall", "show", "rule", "name="+FirewallRuleName)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

// AddFirewallRule creates (idempotently: delete-then-add, since netsh happily
// duplicates) the inbound allow rule for this executable. Requires elevation.
func AddFirewallRule() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own executable: %w", err)
	}
	// Best-effort delete keeps re-runs from stacking duplicate rules.
	netsh("advfirewall", "firewall", "delete", "rule", "name="+FirewallRuleName)
	out, code, err := netsh("advfirewall", "firewall", "add", "rule",
		"name="+FirewallRuleName,
		"dir=in", "action=allow", "enable=yes",
		"program="+exe,
		"description=Allow inbound connections to proxyforward (tunnel gateway/agent).",
	)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("netsh add rule failed (exit %d): %s", code, strings.TrimSpace(out))
	}
	return nil
}

// RemoveFirewallRule deletes the rule. Requires elevation. Removing an
// already-absent rule is not an error.
func RemoveFirewallRule() error {
	out, code, err := netsh("advfirewall", "firewall", "delete", "rule", "name="+FirewallRuleName)
	if err != nil {
		return err
	}
	if code != 0 {
		if present, perr := FirewallRulePresent(); perr == nil && !present {
			return nil // nothing to delete — the desired state holds
		}
		return fmt.Errorf("netsh delete rule failed (exit %d): %s", code, strings.TrimSpace(out))
	}
	return nil
}
