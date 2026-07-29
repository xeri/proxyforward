// Package svc owns proxyforward's Windows integration surface: firewall
// rule management, the elevation helper that runs single privileged tasks in
// a scoped child process, sign-in autostart (see autostart_windows.go), and
// (see service.go) Windows service mode.
package svc

import "errors"

// FirewallRuleName is the single program-scoped inbound allow rule. Scoping
// by program (not port) means tunnel port changes never need another UAC
// prompt.
const FirewallRuleName = "proxyforward"

// AutostartValueName is our value under the per-user Run key, and it is also
// the entry's identity: reconciling only ever touches this one name, so a Run
// key full of other people's programs is never disturbed.
const AutostartValueName = "proxyforward"

// ErrUnsupported is returned by Windows-only operations on other platforms.
var ErrUnsupported = errors.New("svc: only supported on Windows")

// Elevated tasks understood by `proxyforward elevated-task <task>`. Each
// helper invocation performs exactly one task and exits, so the UAC consent
// covers a single, predictable action.
const (
	TaskAddFirewall      = "add-firewall"
	TaskRemoveFirewall   = "remove-firewall"
	TaskInstallService   = "install-service"
	TaskUninstallService = "uninstall-service"
	TaskStartService     = "start-service"
	TaskStopService      = "stop-service"
)
