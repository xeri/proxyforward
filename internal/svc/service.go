package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kardianos/service"

	"proxyforward/internal/config"
)

const (
	ServiceName        = "proxyforward"
	serviceDisplayName = "proxyforward tunnel"
	serviceDescription = "ngrok-style reverse tunnel for Minecraft servers behind NAT (proxyforward)."
	// stopTimeout is how long Stop waits for the engine to wind down before
	// letting the SCM proceed anyway.
	stopTimeout = 15 * time.Second
)

// serviceConfig describes the installed service: it runs
// `proxyforward service run`, reading config from %ProgramData%.
func serviceConfig() *service.Config {
	return &service.Config{
		Name:        ServiceName,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
		Arguments:   []string{"service", "run"},
		Option: service.KeyValue{
			"StartType":              "automatic",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"OnFailureResetPeriod":   10,
		},
	}
}

// RunFunc is the engine entrypoint the service hosts; it must return once
// ctx is cancelled.
type RunFunc func(ctx context.Context) error

// program adapts a RunFunc to kardianos/service's Start/Stop model.
type program struct {
	run    RunFunc
	cancel context.CancelFunc
	done   chan struct{}
	svc    service.Service
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := p.run(ctx); err != nil && ctx.Err() == nil {
			// A fatal engine error (bad config, unrecoverable) must reach the
			// SCM so OnFailure=restart applies; exiting is the sanctioned way.
			if logger, lerr := p.svc.Logger(nil); lerr == nil {
				logger.Errorf("engine failed: %v", err)
			}
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
	}
	return nil
}

func newService(run RunFunc) (service.Service, error) {
	p := &program{run: run}
	s, err := service.New(p, serviceConfig())
	if err != nil {
		return nil, err
	}
	p.svc = s
	return s, nil
}

// RunService is the `service run` entrypoint: blocks under SCM control (or
// runs interactively for debugging) until stopped.
func RunService(run RunFunc) error {
	s, err := newService(run)
	if err != nil {
		return err
	}
	return s.Run()
}

// InstallService registers the Windows service and seeds
// %ProgramData%\proxyforward with the invoking user's config when none
// exists yet (the service account cannot read %APPDATA%). Requires
// elevation.
func InstallService(args []string) error {
	if err := seedServiceConfig(); err != nil {
		return err
	}
	s, err := newService(nil)
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("install service: %w", err)
	}
	return nil
}

// UninstallService stops (best effort) and removes the service. Requires
// elevation.
func UninstallService() error {
	s, err := newService(nil)
	if err != nil {
		return err
	}
	s.Stop() // best effort; uninstall of a running service would fail
	if err := s.Uninstall(); err != nil {
		return fmt.Errorf("uninstall service: %w", err)
	}
	return nil
}

// StartService / StopService drive the SCM (typically require elevation).
func StartService() error {
	s, err := newService(nil)
	if err != nil {
		return err
	}
	return s.Start()
}

func StopService() error {
	s, err := newService(nil)
	if err != nil {
		return err
	}
	return s.Stop()
}

// ServiceStatus reports the installed service's state: "running",
// "stopped", "not-installed", or "unknown".
func ServiceStatus() (string, error) {
	s, err := newService(nil)
	if err != nil {
		return "unknown", err
	}
	st, err := s.Status()
	if err != nil {
		if errors.Is(err, service.ErrNotInstalled) {
			return "not-installed", nil
		}
		return "unknown", err
	}
	switch st {
	case service.StatusRunning:
		return "running", nil
	case service.StatusStopped:
		return "stopped", nil
	default:
		return "unknown", nil
	}
}

// seedServiceConfig copies the per-user config into %ProgramData% when the
// service location is empty, so "install as service" carries the setup the
// user already made.
func seedServiceConfig() error {
	dst := config.DefaultPath(true)
	if _, err := os.Stat(dst); err == nil {
		return nil // service config already present — never overwrite
	}
	src := config.DefaultPath(false)
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to seed; service starts from defaults
		}
		return fmt.Errorf("read user config for seeding: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create service config dir: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("seed service config: %w", err)
	}
	return nil
}
