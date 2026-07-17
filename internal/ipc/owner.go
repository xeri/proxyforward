package ipc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"proxyforward/internal/secureio"
)

// PipeOwnerFile names the file recording which account may reach the engine's
// IPC pipe besides SYSTEM, Administrators, and whoever serves it.
//
// It exists for service mode only. There the engine runs as LocalSystem, so "the
// account we run as" is SYSTEM, and the installing admin's ordinary GUI would be
// locked out: UAC filters the Administrators SID to deny-only in an unelevated
// token, so the pipe's BA grant cannot help a GUI that is not running elevated.
// Before this the gap was covered by granting INTERACTIVE — every account logged
// on to the machine — which handed gateway administration (pairing codes carry
// the gateway token; revoke and scope are on the same pipe) to any local user.
//
// It lives in the config dir, whose ACL secureio has already narrowed to SYSTEM,
// Administrators, and the owner — so it cannot be an injection point: writing it
// already requires the privilege it grants. ipc.pipeSecurity parses it anyway.
const PipeOwnerFile = "pipe-owner.sid"

// RecordPipeOwner writes the installing account's SID next to the service config,
// so the engine — which will be running as LocalSystem — can let that person's
// unelevated GUI attach. Called from the install path, which is already elevated,
// so the SID is the real user's: elevation raises a token's privileges, it does
// not change which account it belongs to.
func RecordPipeOwner(dir string) error {
	sid, err := secureio.CurrentUserSID()
	if err != nil {
		return fmt.Errorf("resolve the installing account: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, PipeOwnerFile), []byte(sid), 0o600); err != nil {
		return fmt.Errorf("record the pipe owner: %w", err)
	}
	return nil
}

// PipeOwners returns the extra SIDs allowed to reach the IPC pipe, read from
// dir. Empty when the file is absent, which is the normal GUI-hosted case: there
// the engine already runs as the person using it, so it grants them by being them.
//
// A read failure is not fatal. The cost of ignoring it is an unelevated GUI that
// cannot attach to a service daemon; the cost of treating a garbled file as a
// reason to widen the ACL would be the bug this replaces.
func PipeOwners(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, PipeOwnerFile))
	if err != nil {
		return nil
	}
	sid := strings.TrimSpace(string(b))
	if sid == "" {
		return nil
	}
	return []string{sid}
}
