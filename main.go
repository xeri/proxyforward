package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v2"
	wailslogger "github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"proxyforward/app"
	"proxyforward/internal/config"
	"proxyforward/internal/engine"
	"proxyforward/internal/link"
	"proxyforward/internal/logging"
	"proxyforward/internal/svc"
	"proxyforward/internal/version"
	"proxyforward/internal/wincon"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Cobra's Windows "mousetrap" exits any Explorer-launched process after a
	// 5s sleep, assuming a CLI was double-clicked by mistake. This app IS the
	// GUI when run with no arguments, so double-clicking must work.
	cobra.MousetrapHelpText = ""
	crashLog := installCrashLog()
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if crashLog != nil {
			fmt.Fprintf(crashLog, "%s error: %v\n", time.Now().Format(time.RFC3339), err)
		}
		os.Exit(1)
	}
}

// installCrashLog routes fatal runtime crashes and top-level errors to
// %APPDATA%\proxyforward\logs\crash.log. The production binary is a
// windowsgui-subsystem app: it has no usable stderr, so without this a panic
// or startup error dies completely silently (window never appears, nothing
// in the event log).
func installCrashLog() *os.File {
	dir := filepath.Join(config.DefaultDir(false), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return nil
	}
	return f
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "proxyforward",
		Short:         "ngrok-style reverse tunnel for Minecraft servers behind NAT",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGUI()
		},
	}
	root.AddCommand(
		newRoleCmd(config.RoleAgent, "Run the agent (Server A: hosts Minecraft, dials out to the gateway)"),
		newRoleCmd(config.RoleGateway, "Run the gateway (Server B: public-facing, accepts players)"),
		newPairCmd(),
		newServiceCmd(),
		newFirewallCmd(),
		newElevatedTaskCmd(),
		newTraySpikeCmd(),
	)
	return root
}

// newPairCmd wires an agent to a gateway from a pasted pairing code — the
// headless counterpart of the GUI wizard.
func newPairCmd() *cobra.Command {
	var (
		configPath string
		localAddr  string
		publicPort int
	)
	cmd := &cobra.Command{
		Use:   "pair <pairing-code>",
		Short: "Configure this machine as an agent using a gateway's pairing code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			code, err := link.ParsePairingCode(args[0])
			if err != nil {
				return err
			}
			if configPath == "" {
				configPath = config.DefaultPath(false)
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.Role == config.RoleGateway {
				return fmt.Errorf("this machine is configured as a gateway; an agent pairs from a different machine (or pass --config for a separate config)")
			}
			cfg.Role = config.RoleAgent
			if cfg.Agent.AgentID == "" {
				cfg.Agent.AgentID = config.NewID()
			}
			cfg.Agent.GatewayHost = code.Host
			cfg.Agent.GatewayPort = code.Port
			cfg.Agent.Token = code.Token
			cfg.Agent.CertFingerprint = code.Fingerprint
			if len(cfg.Agent.Tunnels) == 0 {
				cfg.Agent.Tunnels = []config.Tunnel{{
					ID:         config.NewID(),
					Name:       "Minecraft",
					Type:       config.TunnelTCP,
					LocalAddr:  localAddr,
					PublicPort: publicPort,
					Enabled:    true,
				}}
			}
			if err := cfg.Save(configPath); err != nil {
				return err
			}
			fmt.Printf("Paired with gateway %s:%d.\n", code.Host, code.Port)
			fmt.Printf("Default tunnel: %s -> public port %d\n", localAddr, publicPort)
			fmt.Println("Start the agent with: proxyforward agent")
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path to config.toml (default: %APPDATA%\\proxyforward\\config.toml)")
	cmd.Flags().StringVar(&localAddr, "local", "127.0.0.1:25565", "local Minecraft server address for the default tunnel")
	cmd.Flags().IntVar(&publicPort, "public-port", config.DefaultPublicPort, "public port to request on the gateway for the default tunnel")
	return cmd
}

// newRoleCmd builds the `agent` / `gateway` headless subcommands, which share
// everything except the role they enforce and the engine they start.
func newRoleCmd(role config.Role, short string) *cobra.Command {
	var (
		configPath string
		logLevel   string
		headless   bool
	)
	cmd := &cobra.Command{
		Use:   string(role),
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()

			configDir := config.DefaultDir(false)
			if configPath == "" {
				configPath = config.DefaultPath(false)
			} else {
				configDir = filepath.Dir(configPath)
			}
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			if cfg.Role == config.RoleUnset {
				cfg.Role = role
			}
			if cfg.Role != role {
				return fmt.Errorf("config %s has role %q; refusing to run as %q (pass --config or fix the config)", configPath, cfg.Role, role)
			}
			// First-run bootstrap: identities and tokens are generated once
			// and must persist before validation demands them.
			changed := false
			if role == config.RoleGateway && cfg.Gateway.Token == "" {
				cfg.Gateway.Token = config.NewToken()
				changed = true
			}
			if role == config.RoleAgent && cfg.Agent.AgentID == "" {
				cfg.Agent.AgentID = config.NewID()
				changed = true
			}
			if logLevel != "" {
				cfg.Logging.Level = logLevel
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			if changed {
				if err := cfg.Save(configPath); err != nil {
					return fmt.Errorf("persist first-run identity: %w", err)
				}
			}

			var filePath string
			if cfg.Logging.FileEnabled {
				filePath = logging.DefaultFilePath(configDir)
			}
			logger, closeLogs, err := logging.New(logging.Options{
				Level:    cfg.Logging.Level,
				Console:  true,
				FilePath: filePath,
			})
			if err != nil {
				return err
			}
			defer closeLogs()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			logger.Info("starting", "role", role, "version", version.String(), "config", configPath)
			return runEngineWithIPC(ctx, cfg, configDir, configPath, logger)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path to config.toml (default: %APPDATA%\\proxyforward\\config.toml)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "override log level (debug|info|warn|error)")
	cmd.Flags().BoolVar(&headless, "headless", true, "run without GUI (always true for this subcommand)")
	return cmd
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service <install|uninstall|start|stop|run>",
		Short: "Manage proxyforward as a Windows service (config in %ProgramData%\\proxyforward)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			switch args[0] {
			case "install":
				var err error
				if svc.IsElevated() {
					err = svc.InstallService(nil)
				} else {
					err = svc.RunElevatedTask(svc.TaskInstallService)
				}
				if err != nil {
					return err
				}
				fmt.Println("Service installed. Start it with: proxyforward service start")
				return nil
			case "uninstall":
				var err error
				if svc.IsElevated() {
					err = svc.UninstallService()
				} else {
					err = svc.RunElevatedTask(svc.TaskUninstallService)
				}
				if err != nil {
					return err
				}
				fmt.Println("Service uninstalled.")
				return nil
			case "start":
				if err := svc.StartService(); err != nil {
					return fmt.Errorf("start service (may need an elevated prompt): %w", err)
				}
				fmt.Println("Service started.")
				return nil
			case "stop":
				if err := svc.StopService(); err != nil {
					return fmt.Errorf("stop service (may need an elevated prompt): %w", err)
				}
				fmt.Println("Service stopped.")
				return nil
			case "run":
				return svc.RunService(runServiceEngine)
			default:
				return fmt.Errorf("unknown service action %q (want install, uninstall, start, stop, or run)", args[0])
			}
		},
	}
	return cmd
}

// runServiceEngine is what the installed service executes: engine + IPC pipe
// with config from %ProgramData%.
func runServiceEngine(ctx context.Context) error {
	configDir := config.DefaultDir(true)
	configPath := config.DefaultPath(true)
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if cfg.Role == config.RoleUnset {
		return fmt.Errorf("service has no configured role — create %s (installing from a configured GUI/CLI seeds it automatically)", configPath)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	logger, closeLogs, err := logging.New(logging.Options{
		Level:    cfg.Logging.Level,
		FilePath: logging.DefaultFilePath(configDir), // services always log to file
	})
	if err != nil {
		return err
	}
	defer closeLogs()
	logger.Info("service starting", "role", cfg.Role, "version", version.String(), "config", configPath)
	return runEngineWithIPC(ctx, cfg, configDir, configPath, logger)
}

// runEngineWithIPC hosts one engine.Engine (role engine + IPC pipe) to
// completion.
func runEngineWithIPC(ctx context.Context, cfg *config.Config, configDir, configPath string, logger *slog.Logger) error {
	e, err := engine.New(cfg, configDir, configPath, logger)
	if err != nil {
		return err
	}
	return e.Run(ctx)
}

func newElevatedTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "elevated-task <task>",
		Short:  "Internal: run a single privileged task (firewall rule, service install) and exit",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			switch args[0] {
			case svc.TaskAddFirewall:
				return svc.AddFirewallRule()
			case svc.TaskRemoveFirewall:
				return svc.RemoveFirewallRule()
			case svc.TaskInstallService:
				return svc.InstallService(args[1:])
			case svc.TaskUninstallService:
				return svc.UninstallService()
			default:
				return fmt.Errorf("unknown elevated task %q", args[0])
			}
		},
	}
	return cmd
}

// newFirewallCmd manages the inbound firewall rule. Status needs no
// elevation; add/remove run directly when already elevated, otherwise
// through the scoped elevation helper (one UAC prompt, one task).
func newFirewallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "firewall <status|add|remove>",
		Short: "Manage the Windows Firewall rule that allows inbound connections",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wincon.AttachParent()
			runPrivileged := func(task string, direct func() error) error {
				if svc.IsElevated() {
					return direct()
				}
				return svc.RunElevatedTask(task)
			}
			switch args[0] {
			case "status":
				present, err := svc.FirewallRulePresent()
				if err != nil {
					return err
				}
				if present {
					fmt.Printf("Firewall rule %q is present.\n", svc.FirewallRuleName)
				} else {
					fmt.Printf("Firewall rule %q is missing — run: proxyforward firewall add\n", svc.FirewallRuleName)
				}
				return nil
			case "add":
				if err := runPrivileged(svc.TaskAddFirewall, svc.AddFirewallRule); err != nil {
					return err
				}
				fmt.Println("Firewall rule added.")
				return nil
			case "remove":
				if err := runPrivileged(svc.TaskRemoveFirewall, svc.RemoveFirewallRule); err != nil {
					return err
				}
				fmt.Println("Firewall rule removed.")
				return nil
			default:
				return fmt.Errorf("unknown firewall action %q (want status, add, or remove)", args[0])
			}
		},
	}
	return cmd
}

func runGUI() error {
	configPath := config.DefaultPath(false)
	cfg, err := config.Load(configPath)
	if err != nil {
		// The GUI must still open on a broken config so the user can fix it;
		// start from defaults and surface the load error in the UI later.
		cfg = config.Default()
	}
	ring := logging.NewRing(2000)
	var filePath string
	if cfg.Logging.FileEnabled {
		filePath = logging.DefaultFilePath(config.DefaultDir(false))
	}
	logger, closeLogs, lerr := logging.New(logging.Options{
		Level:    cfg.Logging.Level,
		FilePath: filePath,
		Ring:     ring,
	})
	if lerr != nil {
		return lerr
	}
	defer closeLogs()
	if err != nil {
		logger.Error("config failed to load; using defaults", "err", err)
	}

	a := app.New(configPath, cfg, ring, logger)
	// Wails reports webview/runtime failures through its own logger and can
	// os.Exit without returning an error; in a windowsgui process that output
	// is invisible, so route it to a file next to the crash log.
	wailsLog := wailslogger.NewFileLogger(filepath.Join(config.DefaultDir(false), "logs", "wails.log"))
	return wails.Run(&options.App{
		Title:              "proxyforward",
		Width:              1100,
		Height:             720,
		MinWidth:           900,
		MinHeight:          600,
		AssetServer:        &assetserver.Options{Assets: assets},
		BackgroundColour:   &options.RGBA{R: 15, G: 17, B: 21, A: 1},
		OnStartup:          a.Startup,
		OnShutdown:         a.Shutdown,
		Bind:               []interface{}{a},
		Logger:             wailsLog,
		LogLevelProduction: wailslogger.ERROR,
	})
}
