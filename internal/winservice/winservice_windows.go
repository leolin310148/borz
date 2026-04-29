//go:build windows

package winservice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func Supported() bool { return true }

func Install(cfg Config) error {
	cfg = normalizeConfig(cfg)
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(cfg.Name); err == nil {
		existing.Close()
		return fmt.Errorf("service %q already exists", cfg.Name)
	}

	s, err := m.CreateService(cfg.Name, cfg.ExecutablePath, mgr.Config{
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		StartType:   mgr.StartAutomatic,
	}, cfg.Args...)
	if err != nil {
		return fmt.Errorf("create service %q: %w", cfg.Name, err)
	}
	defer s.Close()
	return nil
}

func Uninstall(name string) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service %q: %w", name, err)
	}
	return nil
}

func Start(name string) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service %q: %w", name, err)
	}
	return nil
}

func Stop(name string) error {
	m, s, err := openService(name)
	if err != nil {
		return err
	}
	defer m.Disconnect()
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("query service %q: %w", name, err)
	}
	if status.State == svc.Stopped {
		return nil
	}
	status, err = s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("stop service %q: %w", name, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("stop service %q: timed out waiting for stopped state", name)
		}
		time.Sleep(500 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("query service %q: %w", name, err)
		}
	}
	return nil
}

func Status(name string) (string, error) {
	m, s, err := openService(name)
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("query service %q: %w", name, err)
	}
	return stateName(status.State), nil
}

func Run(name string, runner Runner) error {
	if name == "" {
		name = DefaultName
	}
	return svc.Run(name, &handler{runner: runner})
}

type handler struct {
	runner Runner
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	changes <- svc.Status{State: svc.StartPending}
	go func() {
		errCh <- h.runner(ctx)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				if err := waitRunner(errCh); err != nil {
					return true, 1
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
				changes <- svc.Status{State: svc.Running, Accepts: accepts}
			}
		case err := <-errCh:
			if err != nil {
				return true, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.Name == "" {
		cfg.Name = DefaultName
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = DefaultDisplayName
	}
	if cfg.Description == "" {
		cfg.Description = DefaultDescription
	}
	if cfg.ExecutablePath == "" {
		cfg.ExecutablePath, _ = os.Executable()
	}
	if abs, err := filepath.Abs(cfg.ExecutablePath); err == nil {
		cfg.ExecutablePath = abs
	}
	return cfg
}

func openService(name string) (*mgr.Mgr, *mgr.Service, error) {
	if name == "" {
		name = DefaultName
	}
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("connect to service manager: %w", err)
	}
	s, err := m.OpenService(name)
	if err != nil {
		m.Disconnect()
		return nil, nil, fmt.Errorf("open service %q: %w", name, err)
	}
	return m, s, nil
}

func waitRunner(errCh <-chan error) error {
	select {
	case err := <-errCh:
		return err
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timed out waiting for service runner to stop")
	}
}

func stateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start pending"
	case svc.StopPending:
		return "stop pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue pending"
	case svc.PausePending:
		return "pause pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown (%d)", state)
	}
}
