package config

import (
	"os"
	"path/filepath"
)

const appDirName = "proxyforward"

// DefaultDir returns the configuration directory for the given mode.
//
// GUI/console runs use the per-user %APPDATA%\proxyforward. Service runs use
// %ProgramData%\proxyforward because a service account (e.g. LocalService)
// cannot see the configuring user's %APPDATA%.
func DefaultDir(serviceMode bool) string {
	if serviceMode {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, appDirName)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, appDirName)
}

// DefaultPath returns the config file path for the given mode.
func DefaultPath(serviceMode bool) string {
	return filepath.Join(DefaultDir(serviceMode), "config.toml")
}
