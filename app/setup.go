package app

import (
	"fmt"
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"proxyforward/internal/config"
	"proxyforward/internal/setup"
	"proxyforward/internal/version"
)

// SetupFileInfo is the passphrase-free header of a .pfsetup file, so the UI
// can show what it is about to import before committing.
type SetupFileInfo struct {
	Path         string `json:"path"`
	Role         string `json:"role"`
	AppVersion   string `json:"appVersion"`
	ExportedAtMs int64  `json:"exportedAtMs"`
	Encrypted    bool   `json:"encrypted"`
}

// ExportSetup writes this machine's complete setup (config with secrets,
// gateway TLS identity, statistics) to a user-chosen .pfsetup file and
// returns the path ("" when the dialog is cancelled). An empty passphrase
// produces a plaintext file — the UI warns about what that means.
func (a *App) ExportSetup(passphrase string) (string, error) {
	a.mu.Lock()
	if a.mode == ModeAttached {
		a.mu.Unlock()
		return "", fmt.Errorf("the setup is owned by the daemon (service or headless run) — stop it first, then export")
	}
	if a.cfg.Role == config.RoleUnset {
		a.mu.Unlock()
		return "", fmt.Errorf("no role configured yet — nothing to export")
	}
	role := a.cfg.Role
	a.mu.Unlock()

	defaultName := fmt.Sprintf("proxyforward-setup-%s-%s%s", role, time.Now().Format("20060102-150405"), setup.FileExt)
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "Export setup",
		Filters:         []runtime.FileFilter{{DisplayName: "proxyforward setup", Pattern: "*" + setup.FileExt}},
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, a.exportSetupToPath(path, passphrase)
}

// exportSetupToPath is ExportSetup minus the dialog, for tests.
func (a *App) exportSetupToPath(path, passphrase string) error {
	a.mu.Lock()
	cfg := *a.cfg
	if a.eng != nil {
		// Current lifetime totals and history belong in the backup.
		if err := a.eng.Stats.Flush(); err != nil {
			a.logger.Warn("setup export: stats flush failed; exporting last flushed state", "err", err)
		}
	}
	a.mu.Unlock()

	data, err := setup.Export(&cfg, a.configDir, version.String(), passphrase)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write setup file: %w", err)
	}
	a.logger.Info("setup exported", "path", path, "encrypted", passphrase != "")
	return nil
}

// ChooseAndInspectSetupFile opens a file picker and returns the chosen
// file's header (role, export date, encrypted) so the UI can confirm and
// ask for a passphrase before ImportSetup. Nil means cancelled.
func (a *App) ChooseAndInspectSetupFile() (*SetupFileInfo, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Import setup",
		Filters: []runtime.FileFilter{{DisplayName: "proxyforward setup", Pattern: "*" + setup.FileExt}},
	})
	if err != nil || path == "" {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read setup file: %w", err)
	}
	info, err := setup.Inspect(data)
	if err != nil {
		return nil, err
	}
	return &SetupFileInfo{
		Path:         path,
		Role:         info.Role,
		AppVersion:   info.AppVersion,
		ExportedAtMs: info.ExportedAt.UnixMilli(),
		Encrypted:    info.Encrypted,
	}, nil
}

// ImportSetup replaces this machine's setup with the contents of a .pfsetup
// file: the engine stops, the decoded files land in the config dir, and the
// engine starts under the imported identity. Existing config, pairing, and
// stats are overwritten — the UI confirms that first.
func (a *App) ImportSetup(path, passphrase string) error {
	return a.importSetupFromPath(path, passphrase)
}

func (a *App) importSetupFromPath(path, passphrase string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read setup file: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode == ModeAttached {
		return fmt.Errorf("the setup is owned by the daemon (service or headless run) — stop it first, then import")
	}
	role, files, err := setup.Decode(data, passphrase)
	if err != nil {
		return err
	}
	// The engine's shutdown includes the final stats flush, so it must be
	// fully down before the imported files land (Windows file locking, and
	// a late flush would clobber the imported stats).
	a.stopEngineLocked()
	if err := setup.WriteFiles(files, a.configDir); err != nil {
		return err
	}
	loaded, err := config.Load(a.configPath)
	if err != nil {
		return fmt.Errorf("imported config failed to load: %w", err)
	}
	*a.cfg = *loaded // App, Engine and role objects share this pointer
	a.startEngineLocked()
	a.logger.Info("setup imported", "path", path, "role", role)
	return nil
}
