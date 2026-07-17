//go:build windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// unsafePointer is unsafe.Pointer behind a name, so the test file can walk an
// ACE's SidStart without importing unsafe itself.
func unsafePointer(p *uint32) unsafe.Pointer { return unsafe.Pointer(p) }

// pipeSecurity builds the named pipe's SDDL: full control for SYSTEM (SY),
// Administrators (BA), the account this process runs as, and any explicitly
// recorded owners — and nobody else. "D:P" is load-bearing: P (PROTECTED) means
// no inherited ACE can add to this set.
//
// It used to be a constant ending in "(A;;GA;;;IU)", under a comment saying it
// admitted "the interactive user". IU is not that. IU is S-1-5-4, INTERACTIVE:
// *every* account logged on interactively, including a standard non-admin one and
// every RDP session. That would be defensible for a status feed, but this pipe is
// not one — the same request envelope carries OpIssuePairing, OpRevokeAgent and
// OpSetAgentScope. A pairing code embeds the gateway token, so any interactive
// account could enroll its own agent, or revoke the real one; against a
// service-mode daemon (LocalSystem) that is escalation to a SYSTEM service's
// security policy.
//
// The rule: this pipe fronts the config directory's secrets, so it must be no
// easier to reach than that directory. secureio grants SY, BA and the account we
// run as (secureio_windows.go dirSDDL) — the same three, deliberately. Anything
// granted here beyond them is a hole straight through the ACL secureio installs.
//
// extraOwners covers service mode, where the engine runs as LocalSystem and so
// "the account we run as" is SYSTEM. That would lock out the installing admin's
// *unelevated* GUI, because UAC filters the Administrators SID to deny-only in an
// unelevated token, leaving BA unable to help. The installer records its own SID
// (svc.recordPipeOwner) and the engine passes it here.
func pipeSecurity(extraOwners []string) (string, error) {
	self, err := currentUserSID()
	if err != nil {
		return "", fmt.Errorf("resolve the account serving the pipe: %w", err)
	}
	var b strings.Builder
	b.WriteString("D:P(A;;GA;;;SY)(A;;GA;;;BA)")
	seen := map[string]bool{}
	for _, sid := range append([]string{self}, extraOwners...) {
		if seen[sid] {
			continue
		}
		seen[sid] = true
		// Validate before formatting in: these strings are concatenated into an
		// SDDL, so a malformed one must fail here rather than shift the meaning of
		// the descriptor. The recorded owner comes from a file only SYSTEM and
		// admins can write, but "trusted source" is not a reason to skip parsing.
		if _, err := windows.StringToSid(sid); err != nil {
			return "", fmt.Errorf("refusing to build a pipe ACL from an unparseable sid %q: %w", sid, err)
		}
		fmt.Fprintf(&b, "(A;;GA;;;%s)", sid)
	}
	sddl := b.String()
	// Parse what we built. A descriptor that does not round-trip is one whose
	// meaning we are guessing at, and this one is the whole authorization model.
	if _, err := windows.SecurityDescriptorFromString(sddl); err != nil {
		return "", fmt.Errorf("built an invalid pipe ACL %q: %w", sddl, err)
	}
	return sddl, nil
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// ErrUntrustedServer is returned by Dial when something is answering the pipe
// but it is not an engine this user should trust.
var ErrUntrustedServer = errors.New("ipc: the process answering the pipe is not a trusted engine")

// fder is go-winio's promoted handle accessor (win32Pipe embeds *win32File).
type fder interface{ Fd() uintptr }

// verifyServer checks who *created* the pipe we just opened, and is the mirror of
// pipeSecurity: that says who may reach the engine, this says who may be it.
//
// A named pipe belongs to whoever creates its first instance, and any local
// process may create one under a name nobody holds yet. So a low-privileged
// process that gets there first — at boot, or after the engine dies — becomes the
// engine as far as the GUI is concerned: App.Startup finds a pipe answering,
// attaches as a thin client, and from then on believes whatever it is told and
// tells it whatever it is asked. That is not only spoofed status; the GUI would
// display an attacker's pairing code as its own, and a pairing code is how an
// agent is told which gateway to trust.
//
// The owner is the right thing to check because it is the one part a squatter
// cannot fake: setting an object's owner to a SID you do not hold needs
// SeRestorePrivilege, so an unprivileged process cannot claim to be SYSTEM. (An
// elevated attacker can — but an elevated attacker already owns the machine, so
// there is no boundary left to defend.)
func verifyServer(conn net.Conn) error {
	f, ok := conn.(fder)
	if !ok {
		// Never happens with go-winio, and if it ever did we would be trusting an
		// unverified pipe silently. Fail closed.
		return fmt.Errorf("%w: cannot inspect the pipe handle", ErrUntrustedServer)
	}
	// GENERIC_READ maps to include READ_CONTROL, which is what this needs; DialPipe
	// asks for it, so the owner is readable on any pipe we could open at all.
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: cannot read its owner: %w", ErrUntrustedServer, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("%w: cannot read its owner: %w", ErrUntrustedServer, err)
	}
	trusted, err := trustedServerOwner(owner)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUntrustedServer, err)
	}
	if !trusted {
		return fmt.Errorf("%w: it belongs to %s — expected this account, SYSTEM, or an administrator", ErrUntrustedServer, owner)
	}
	return nil
}

// trustedServerOwner reports whether sid may own the engine's pipe: SYSTEM (the
// service), Administrators (an elevated engine), or this very account (the
// ordinary GUI-hosted engine — the same three principals pipeSecurity grants).
func trustedServerOwner(sid *windows.SID) (bool, error) {
	for _, w := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid,
		windows.WinBuiltinAdministratorsSid,
	} {
		known, err := windows.CreateWellKnownSid(w)
		if err != nil {
			return false, err
		}
		if sid.Equals(known) {
			return true, nil
		}
	}
	self, err := currentUserSID()
	if err != nil {
		return false, err
	}
	return sid.String() == self, nil
}
