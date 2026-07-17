//go:build windows

package secureio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// allowedSIDs returns the SIDs an object's DACL grants access to. It walks the
// ACEs rather than matching on the SDDL text: SDDL renders well-known SIDs as
// two-letter aliases (SY, BA, BU), so a substring test for "S-1-5-32-545" silently
// never fires and the test passes while the hole is wide open.
func allowedSIDs(t *testing.T, path string) []*windows.SID {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read security info for %s: %v", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("read DACL for %s: %v", path, err)
	}
	var out []*windows.SID
	for i := uint32(0); dacl != nil && i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("read ACE %d of %s: %v", i, path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		// The SID lives inside the ACE, which lives inside sd's buffer; copy it out
		// so the caller can outlive both.
		sid, err := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Copy()
		if err != nil {
			t.Fatalf("copy SID from ACE %d: %v", i, err)
		}
		out = append(out, sid)
	}
	runtime.KeepAlive(sd)
	return out
}

func grants(t *testing.T, path string, w windows.WELL_KNOWN_SID_TYPE) bool {
	t.Helper()
	want, err := windows.CreateWellKnownSid(w)
	if err != nil {
		t.Fatalf("well-known sid: %v", err)
	}
	for _, got := range allowedSIDs(t, path) {
		if got.Equals(want) {
			return true
		}
	}
	return false
}

func isProtected(t *testing.T, path string) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read security info: %v", err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("read SD control: %v", err)
	}
	return control&windows.SE_DACL_PROTECTED != 0
}

// TestMkdirAllDeniesLocalUsers is the whole point of the package: a directory of
// secrets must not be readable by every local account. It asks the OS rather than
// trusting the SDDL constant, because the bug this replaces was exactly a
// permission the code *stated* (0o600 on the gateway TLS key, the shared token,
// the agent store) that Windows silently ignored. Only the kernel's answer counts.
func TestMkdirAllDeniesLocalUsers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// The %ProgramData% grant that exposed the service's secrets, and its friends.
	for name, sid := range map[string]windows.WELL_KNOWN_SID_TYPE{
		"BUILTIN\\Users":      windows.WinBuiltinUsersSid,
		"Everyone":            windows.WinWorldSid,
		"Authenticated Users": windows.WinAuthenticatedUserSid,
		"ANONYMOUS LOGON":     windows.WinAnonymousSid,
		"INTERACTIVE":         windows.WinInteractiveSid,
	} {
		if grants(t, dir, sid) {
			t.Errorf("a secrets dir grants %s", name)
		}
	}

	// The accounts that must keep access, or the app breaks: SYSTEM (the service
	// runs as LocalSystem) and this process's own user (the GUI reads its config).
	if !grants(t, dir, windows.WinLocalSystemSid) {
		t.Error("SYSTEM is not granted — the service could not read its own config")
	}
	self, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	selfOK := false
	for _, got := range allowedSIDs(t, dir) {
		if got.String() == self {
			selfOK = true
		}
	}
	if !selfOK && !grants(t, dir, windows.WinBuiltinAdministratorsSid) {
		t.Errorf("neither this user (%s) nor Administrators is granted", self)
	}

	// Protected, or %ProgramData%'s Users grant inherits straight back in.
	if !isProtected(t, dir) {
		t.Error("DACL is not protected, so inherited grants survive")
	}
}

// TestMkdirAllRepairsAnExistingLooseDir: installs predate this hardening, and the
// old code left %ProgramData%\proxyforward readable by every local account. MkdirAll
// runs on every start, so it must repair a directory that already exists — otherwise
// existing installs stay exposed until someone reinstalls.
func TestMkdirAllRepairsAnExistingLooseDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil { // how the old code made it
		t.Fatal(err)
	}
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	loose, err := windows.SecurityDescriptorFromString("D:P(A;OICI;GA;;;" + usersSID.String() + ")(A;OICI;GA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := loose.DACL()
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
	runtime.KeepAlive(loose)
	if err != nil {
		t.Fatal(err)
	}
	if !grants(t, dir, windows.WinBuiltinUsersSid) {
		t.Fatal("setup failed: the Users grant did not take, so the repair below would prove nothing")
	}

	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll on an existing dir: %v", err)
	}
	if grants(t, dir, windows.WinBuiltinUsersSid) {
		t.Error("a pre-existing BUILTIN\\Users grant survived MkdirAll — existing installs stay exposed")
	}
}

// TestMkdirAllSecretsInheritTheDACL: the directory is the enforcement point only if
// what lands in it inherits the grant. That inheritance is what lets cred.go and
// cert.go keep writing keys with a mode Windows ignores and still be protected.
func TestMkdirAllSecretsInheritTheDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := MkdirAll(dir); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	key := filepath.Join(dir, "agent_identity.key")
	if err := os.WriteFile(key, []byte("-----BEGIN PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatal(err)
	}
	if grants(t, key, windows.WinBuiltinUsersSid) {
		t.Error("a private key written into a hardened dir is readable by BUILTIN\\Users")
	}
	if !grants(t, key, windows.WinLocalSystemSid) {
		t.Error("the key did not inherit SYSTEM access — the service could not read it")
	}
}
