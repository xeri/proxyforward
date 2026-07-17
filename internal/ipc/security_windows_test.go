//go:build windows

package ipc

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// sidOf resolves a well-known SID to its string form, so a test asserts against
// what the SDDL *means* rather than the two-letter alias it happens to be spelled
// with.
func sidOf(t *testing.T, w windows.WELL_KNOWN_SID_TYPE) string {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(w)
	if err != nil {
		t.Fatalf("well-known sid: %v", err)
	}
	return sid.String()
}

// grantsSID reports whether the DACL that sddl describes grants sid.
func grantsSID(t *testing.T, sddl, sid string) bool {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("parse %q: %v", sddl, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("read DACL: %v", err)
	}
	want, err := windows.StringToSid(sid)
	if err != nil {
		t.Fatalf("parse sid %q: %v", sid, err)
	}
	for i := uint32(0); dacl != nil && i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("read ACE %d: %v", i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if (*windows.SID)(unsafePointer(&ace.SidStart)).Equals(want) {
			return true
		}
	}
	return false
}

// TestPipeDeniesOtherInteractiveUsers is the whole point of this file.
//
// The pipe carried "D:P(A;;GA;;;BA)(A;;GA;;;SY)(A;;GA;;;IU)" under a comment
// saying it admitted "the interactive user". IU is not that. IU is S-1-5-4,
// INTERACTIVE — *every* account logged on interactively, including a standard
// non-admin one and every RDP session. And the pipe is not a status feed: the
// same envelope carries OpIssuePairing, OpRevokeAgent and OpSetAgentScope. So on
// any machine with a second account, that account could mint a pairing code —
// which embeds the gateway token — enroll its own agent, or revoke the real one.
// Against a service-mode daemon (LocalSystem) that is a local privilege
// escalation to a SYSTEM service's security policy.
//
// The rule this pins: the pipe fronts the config directory's secrets, so it must
// be no easier to reach than that directory. secureio grants SY, BA and the
// account we run as; anything the pipe grants beyond those is a hole straight
// through the ACL secureio installs.
func TestPipeDeniesOtherInteractiveUsers(t *testing.T) {
	sddl, err := pipeSecurity(nil)
	if err != nil {
		t.Fatalf("pipeSecurity: %v", err)
	}

	for name, w := range map[string]windows.WELL_KNOWN_SID_TYPE{
		"INTERACTIVE":         windows.WinInteractiveSid,
		"BUILTIN\\Users":      windows.WinBuiltinUsersSid,
		"Everyone":            windows.WinWorldSid,
		"Authenticated Users": windows.WinAuthenticatedUserSid,
		"ANONYMOUS LOGON":     windows.WinAnonymousSid,
	} {
		if grantsSID(t, sddl, sidOf(t, w)) {
			t.Errorf("the ipc pipe grants %s — it can mint pairing codes and revoke agents", name)
		}
	}

	// The principals that must keep it, or the app cannot run: SYSTEM (service
	// mode) and the account serving the pipe (a GUI-hosted engine).
	if !grantsSID(t, sddl, sidOf(t, windows.WinLocalSystemSid)) {
		t.Error("SYSTEM is not granted; a service-mode engine could not serve its own pipe")
	}
	self, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if !grantsSID(t, sddl, self) {
		t.Errorf("the account serving the pipe (%s) is not granted; the GUI could not attach to its own engine", self)
	}
	if !strings.HasPrefix(sddl, "D:P") {
		t.Errorf("the DACL is not protected (%q): inherited grants survive", sddl)
	}
}

// TestPipeGrantsRecordedOwner covers service mode, where the engine runs as
// LocalSystem and so "the account we run as" is SYSTEM — which would leave the
// installing admin's *unelevated* GUI locked out, because UAC filters the
// Administrators SID to deny-only in an unelevated token. The installer records
// its own SID for exactly this, and only SYSTEM and admins can write that file
// (it lives in the secureio-protected config dir), so it cannot be an injection
// point.
func TestPipeGrantsRecordedOwner(t *testing.T) {
	// A well-formed SID that is nobody: it must land in the DACL verbatim.
	const owner = "S-1-5-21-1111111111-2222222222-3333333333-1001"
	sddl, err := pipeSecurity([]string{owner})
	if err != nil {
		t.Fatalf("pipeSecurity: %v", err)
	}
	if !grantsSID(t, sddl, owner) {
		t.Errorf("the recorded owner %s is not granted; a service-mode GUI cannot attach", owner)
	}
	if grantsSID(t, sddl, sidOf(t, windows.WinInteractiveSid)) {
		t.Error("granting an owner reintroduced INTERACTIVE")
	}
}

// TestPipeSecurityRejectsAGarbageOwner: the recorded SID is formatted straight
// into an SDDL string, so a malformed one must fail loudly at startup rather than
// produce a descriptor that parses to something else.
func TestPipeSecurityRejectsAGarbageOwner(t *testing.T) {
	for _, bad := range []string{"", "not-a-sid", "S-1-5-21-x", `BA)(A;;GA;;;IU`} {
		if _, err := pipeSecurity([]string{bad}); err == nil {
			t.Errorf("pipeSecurity accepted owner %q; an SDDL injection or a silently wrong ACL", bad)
		}
	}
}
