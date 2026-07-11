package app

import (
	"archive/zip"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"proxyforward/internal/config"
	"proxyforward/internal/logging"
	"proxyforward/internal/version"
)

// openInFileManager reveals dir in the OS file manager. explorer.exe returns a
// non-zero exit code even on success, so its error is intentionally ignored.
func openInFileManager(dir string) error {
	switch runtime.GOOS {
	case "windows":
		exec.Command("explorer", dir).Start()
		return nil
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

// testReachability dials the gateway's public port across the real network —
// the same path a player takes — validating DNS, gateway firewall, router
// forwarding, the listener, and the tunnel in one check.
func testReachability(host string, port int) (string, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w — check the gateway's firewall rule, the router's port forward, and DNS", addr, err)
	}
	conn.Close()
	return fmt.Sprintf("Reachable: %s answered in %s — players can connect.", addr, time.Since(start).Round(time.Millisecond)), nil
}

// writeDiagnostics builds the support bundle: version, redacted config,
// recent in-memory log lines, and the on-disk log file when present.
func writeDiagnostics(path string, cfg *config.Config, configDir string, ring *logging.Ring) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	// version.txt
	w, err := zw.Create("version.txt")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, version.String())

	// config.redacted.toml — secrets masked, structure intact.
	redacted := *cfg
	if redacted.Gateway.Token != "" {
		redacted.Gateway.Token = "[redacted]"
	}
	if redacted.Agent.Token != "" {
		redacted.Agent.Token = "[redacted]"
	}
	data, err := toml.Marshal(&redacted)
	if err != nil {
		return err
	}
	if w, err = zw.Create("config.redacted.toml"); err != nil {
		return err
	}
	w.Write(data)

	// logs-recent.txt — the GUI ring (what the user was just looking at).
	if ring != nil {
		if w, err = zw.Create("logs-recent.txt"); err != nil {
			return err
		}
		for _, e := range ring.EntriesSince(0) {
			fmt.Fprintf(w, "%s %-5s %s %s\n", time.UnixMilli(e.TimeMs).Format(time.RFC3339), e.Level, e.Msg, e.Attrs)
		}
	}

	// proxyforward.log — the rotating file log, if enabled.
	logPath := logging.DefaultFilePath(configDir)
	if lf, err := os.Open(logPath); err == nil {
		defer lf.Close()
		if w, err := zw.Create("proxyforward.log"); err == nil {
			io.Copy(w, lf)
		}
	}
	return nil
}
