//go:build !windows

package svc

// Non-Windows stubs keep the rest of the codebase building on other
// platforms (useful for running unit tests on CI runners); every call
// reports ErrUnsupported or a safe zero value.

func IsElevated() bool { return false }

func RunElevatedTask(task string, args ...string) error { return ErrUnsupported }

func FirewallRulePresent() (bool, error) { return false, ErrUnsupported }

func AddFirewallRule() error { return ErrUnsupported }

func RemoveFirewallRule() error { return ErrUnsupported }
