//go:build windows

package secureio

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// dirSDDL grants full control to SYSTEM (SY), Administrators (BA), and the
// account we run as, and nobody else. "D:P" is the load-bearing part: P
// (PROTECTED) severs inheritance from the parent, which is what drops
// %ProgramData%'s BUILTIN\Users read grant — without it the ACEs below would be
// added *beside* that grant rather than replacing it. OICI makes the grants
// inheritable by the files and subdirectories written later, which is how a key
// written with a meaningless 0o600 still ends up protected.
//
// The service runs as LocalSystem (svc/service.go serviceConfig sets no UserName,
// so the SCM default applies), so SY covers it; BA keeps an administrator able to
// manage and repair the install; the third ACE is what keeps a non-admin GUI user
// able to read their own %APPDATA% config. The current-user SID is formatted in
// rather than hardcoded because that account differs per role.
const dirSDDLFmt = "D:P(A;OICI;GA;;;SY)(A;OICI;GA;;;BA)(A;OICI;GA;;;%s)"

func mkdirAll(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolve current user for %s ACL: %w", dir, err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(dirSDDLFmt, sid))
	if err != nil {
		return fmt.Errorf("build ACL for %s: %w", dir, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read ACL for %s: %w", dir, err)
	}
	// PROTECTED_DACL_SECURITY_INFORMATION is what actually clears the inherited
	// ACEs; DACL_SECURITY_INFORMATION alone would leave them in place.
	err = windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
	// dacl points into sd's Go-allocated backing memory.
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	return nil
}

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

// CurrentUserSID is the account this process runs as, in string form — the third
// principal in dirSDDL, and the one the service installer records so the engine
// (running as LocalSystem by then) can still let that person's GUI in.
func CurrentUserSID() (string, error) { return currentUserSID() }
