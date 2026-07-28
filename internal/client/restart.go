package client

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/leolin310148/borz/internal/config"
)

// DaemonRestartResult is the machine-readable result of replacing only the
// local daemon process. The managed browser is deliberately left running.
type DaemonRestartResult struct {
	Success          bool `json:"success"`
	PreviousPID      int  `json:"previousPid,omitempty"`
	NewPID           int  `json:"newPid"`
	RecoveredStale   bool `json:"recoveredStale,omitempty"`
	BrowserPreserved bool `json:"browserPreserved"`
}

var (
	restartReadDaemonJSON  = ReadDaemonJSON
	restartProbeDaemonPort = probeDaemonPort
	restartDaemonPort      = daemonPortForProfile
	restartKillDaemon      = KillDaemon
	restartEnsureDaemon    = EnsureDaemon
)

// RestartDaemonPreservingBrowser replaces the verified local daemon process
// without running its graceful shutdown path. Graceful shutdown intentionally
// closes a borz-owned managed Chrome, so restart uses a hard process stop after
// confirming the PID through the daemon's loopback-only /healthz endpoint.
//
// For the default profile this also recovers a stale daemon whose daemon.json
// is missing because its listen port is stable. It refuses to kill an
// unverified process, guess a named-profile daemon port, or manage a
// non-loopback server.
func RestartDaemonPreservingBrowser() (*DaemonRestartResult, error) {
	result := &DaemonRestartResult{Success: true, BrowserPreserved: true}

	info, err := restartReadDaemonJSON()
	switch {
	case err == nil:
		if !isLoopbackDaemonHost(info.Host) {
			return nil, fmt.Errorf("daemon restart only manages a local loopback daemon (found host %q); use the server lifecycle command for remote-accessible servers", info.Host)
		}
		if err := stopVerifiedDaemon(info.PID, info.Port); err != nil {
			return nil, err
		}
		result.PreviousPID = info.PID
	case errors.Is(err, os.ErrNotExist):
		if config.Profile() != "" {
			return nil, fmt.Errorf("cannot safely locate a named-profile daemon after %s is missing; if the daemon is confirmed stopped, run any browser command to start it again", config.DaemonJSONPath())
		}
		port, portErr := restartDaemonPort()
		if portErr != nil {
			return nil, fmt.Errorf("resolve daemon port: %w", portErr)
		}
		if squatter, ok := restartProbeDaemonPort(port); ok {
			if err := validateDaemonPID(squatter.PID); err != nil {
				return nil, fmt.Errorf("refusing to replace stale daemon on port %d: %w", port, err)
			}
			if err := restartKillDaemon(squatter.PID); err != nil {
				return nil, fmt.Errorf("replace stale daemon pid %d: %w", squatter.PID, err)
			}
			result.PreviousPID = squatter.PID
			result.RecoveredStale = true
		}
	default:
		return nil, fmt.Errorf("read daemon state: %w", err)
	}

	if err := restartEnsureDaemon(); err != nil {
		return nil, fmt.Errorf("start replacement daemon: %w", err)
	}
	newInfo, err := restartReadDaemonJSON()
	if err != nil {
		return nil, fmt.Errorf("read replacement daemon state: %w", err)
	}
	result.NewPID = newInfo.PID
	return result, nil
}

func stopVerifiedDaemon(pid, port int) error {
	if err := validateDaemonPID(pid); err != nil {
		return fmt.Errorf("refusing to replace daemon on port %d: %w", port, err)
	}
	live, ok := restartProbeDaemonPort(port)
	if !ok {
		return fmt.Errorf("refusing to replace daemon pid %d: loopback health check on port %d did not identify a borz daemon", pid, port)
	}
	if live.PID != pid {
		return fmt.Errorf("refusing to replace daemon pid %d: loopback health check on port %d reports pid %d", pid, port, live.PID)
	}
	if err := restartKillDaemon(pid); err != nil {
		return fmt.Errorf("replace daemon pid %d: %w", pid, err)
	}
	return nil
}

func validateDaemonPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("health check returned invalid pid %d", pid)
	}
	if pid == os.Getpid() {
		return fmt.Errorf("health check returned the current CLI pid %d", pid)
	}
	return nil
}

func isLoopbackDaemonHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	host = net.ParseIP(host).String()
	return host == "127.0.0.1" || host == "::1"
}
