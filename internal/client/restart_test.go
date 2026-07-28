package client

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

func withRestartStubs(t *testing.T) {
	t.Helper()
	oldRead := restartReadDaemonJSON
	oldProbe := restartProbeDaemonPort
	oldPort := restartDaemonPort
	oldKill := restartKillDaemon
	oldEnsure := restartEnsureDaemon
	t.Cleanup(func() {
		restartReadDaemonJSON = oldRead
		restartProbeDaemonPort = oldProbe
		restartDaemonPort = oldPort
		restartKillDaemon = oldKill
		restartEnsureDaemon = oldEnsure
	})
}

func TestRestartDaemonPreservingBrowserReplacesVerifiedDaemon(t *testing.T) {
	withRestartStubs(t)
	oldInfo := &protocol.DaemonInfo{PID: 111, Host: "127.0.0.1", Port: 19824}
	newInfo := &protocol.DaemonInfo{PID: 222, Host: "127.0.0.1", Port: 19824}
	readCount := 0
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) {
		readCount++
		if readCount == 1 {
			return oldInfo, nil
		}
		return newInfo, nil
	}
	restartProbeDaemonPort = func(port int) (daemonPortSquatter, bool) {
		if port != oldInfo.Port {
			t.Fatalf("probe port = %d", port)
		}
		return daemonPortSquatter{PID: oldInfo.PID, Version: "old"}, true
	}
	var killed int
	restartKillDaemon = func(pid int) error {
		killed = pid
		return nil
	}
	ensured := false
	restartEnsureDaemon = func() error {
		ensured = true
		return nil
	}

	got, err := RestartDaemonPreservingBrowser()
	if err != nil {
		t.Fatalf("RestartDaemonPreservingBrowser: %v", err)
	}
	if killed != oldInfo.PID || !ensured {
		t.Fatalf("killed=%d ensured=%v", killed, ensured)
	}
	if !got.Success || got.PreviousPID != oldInfo.PID || got.NewPID != newInfo.PID || got.RecoveredStale || !got.BrowserPreserved {
		t.Fatalf("result = %+v", got)
	}
}

func TestRestartDaemonPreservingBrowserRecoversMissingState(t *testing.T) {
	withRestartStubs(t)
	newInfo := &protocol.DaemonInfo{PID: 333, Host: "localhost", Port: 21982}
	readCount := 0
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) {
		readCount++
		if readCount == 1 {
			return nil, os.ErrNotExist
		}
		return newInfo, nil
	}
	restartDaemonPort = func() (int, error) { return 21982, nil }
	restartProbeDaemonPort = func(port int) (daemonPortSquatter, bool) {
		return daemonPortSquatter{PID: 444, Version: "stale"}, true
	}
	var killed int
	restartKillDaemon = func(pid int) error {
		killed = pid
		return nil
	}
	restartEnsureDaemon = func() error { return nil }

	got, err := RestartDaemonPreservingBrowser()
	if err != nil {
		t.Fatalf("RestartDaemonPreservingBrowser: %v", err)
	}
	if killed != 444 || !got.Success || got.PreviousPID != 444 || got.NewPID != 333 || !got.RecoveredStale || !got.BrowserPreserved {
		t.Fatalf("killed=%d result=%+v", killed, got)
	}
}

func TestRestartDaemonPreservingBrowserStartsWhenStopped(t *testing.T) {
	withRestartStubs(t)
	readCount := 0
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) {
		readCount++
		if readCount == 1 {
			return nil, os.ErrNotExist
		}
		return &protocol.DaemonInfo{PID: 555, Host: "::1", Port: 19824}, nil
	}
	restartDaemonPort = func() (int, error) { return 19824, nil }
	restartProbeDaemonPort = func(int) (daemonPortSquatter, bool) {
		return daemonPortSquatter{}, false
	}
	restartKillDaemon = func(pid int) error {
		t.Fatalf("unexpected kill of pid %d", pid)
		return nil
	}
	restartEnsureDaemon = func() error { return nil }

	got, err := RestartDaemonPreservingBrowser()
	if err != nil {
		t.Fatalf("RestartDaemonPreservingBrowser: %v", err)
	}
	if !got.Success || got.PreviousPID != 0 || got.NewPID != 555 || !got.BrowserPreserved {
		t.Fatalf("result = %+v", got)
	}
}

func TestRestartDaemonPreservingBrowserRefusesMissingNamedProfileState(t *testing.T) {
	withRestartStubs(t)
	t.Setenv("BORZ_HOME", t.TempDir())
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) {
		return nil, os.ErrNotExist
	}
	restartDaemonPort = func() (int, error) {
		t.Fatal("must not guess a named-profile daemon port")
		return 0, nil
	}

	_, err := RestartDaemonPreservingBrowser()
	if err == nil || !strings.Contains(err.Error(), "cannot safely locate a named-profile daemon") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartDaemonPIDAndLoopbackValidation(t *testing.T) {
	if err := validateDaemonPID(os.Getpid()); err == nil || !strings.Contains(err.Error(), "current CLI pid") {
		t.Fatalf("self pid err = %v", err)
	}
	for _, host := range []string{"127.0.0.1", "::1", "localhost", "LOCALHOST"} {
		if !isLoopbackDaemonHost(host) {
			t.Errorf("expected loopback host %q", host)
		}
	}
	for _, host := range []string{"", "0.0.0.0", "192.0.2.1", "example.test"} {
		if isLoopbackDaemonHost(host) {
			t.Errorf("unexpected loopback host %q", host)
		}
	}
}

func TestRestartDaemonPreservingBrowserRefusesUnverifiedTargets(t *testing.T) {
	tests := []struct {
		name string
		info *protocol.DaemonInfo
		live daemonPortSquatter
		ok   bool
		want string
	}{
		{
			name: "non-loopback",
			info: &protocol.DaemonInfo{PID: 111, Host: "0.0.0.0", Port: 19824},
			want: "only manages a local loopback",
		},
		{
			name: "health unavailable",
			info: &protocol.DaemonInfo{PID: 111, Host: "127.0.0.1", Port: 19824},
			want: "did not identify a borz daemon",
		},
		{
			name: "pid mismatch",
			info: &protocol.DaemonInfo{PID: 111, Host: "127.0.0.1", Port: 19824},
			live: daemonPortSquatter{PID: 222},
			ok:   true,
			want: "reports pid 222",
		},
		{
			name: "invalid live pid",
			info: &protocol.DaemonInfo{PID: 0, Host: "127.0.0.1", Port: 19824},
			want: "invalid pid 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withRestartStubs(t)
			restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) { return tt.info, nil }
			restartProbeDaemonPort = func(int) (daemonPortSquatter, bool) { return tt.live, tt.ok }
			restartKillDaemon = func(pid int) error {
				t.Fatalf("unexpected kill of pid %d", pid)
				return nil
			}
			restartEnsureDaemon = func() error {
				t.Fatal("unexpected daemon start")
				return nil
			}
			_, err := RestartDaemonPreservingBrowser()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRestartDaemonPreservingBrowserPropagatesLifecycleErrors(t *testing.T) {
	withRestartStubs(t)
	oldInfo := &protocol.DaemonInfo{PID: 111, Host: "127.0.0.1", Port: 19824}
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) { return oldInfo, nil }
	restartProbeDaemonPort = func(int) (daemonPortSquatter, bool) {
		return daemonPortSquatter{PID: oldInfo.PID}, true
	}
	restartKillDaemon = func(int) error { return errors.New("kill failed") }
	restartEnsureDaemon = func() error {
		t.Fatal("unexpected daemon start")
		return nil
	}
	if _, err := RestartDaemonPreservingBrowser(); err == nil || !strings.Contains(err.Error(), "kill failed") {
		t.Fatalf("kill err = %v", err)
	}

	withRestartStubs(t)
	readCount := 0
	restartReadDaemonJSON = func() (*protocol.DaemonInfo, error) {
		readCount++
		if readCount == 1 {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("missing new state")
	}
	restartDaemonPort = func() (int, error) { return 19824, nil }
	restartProbeDaemonPort = func(int) (daemonPortSquatter, bool) { return daemonPortSquatter{}, false }
	restartEnsureDaemon = func() error { return nil }
	if _, err := RestartDaemonPreservingBrowser(); err == nil || !strings.Contains(err.Error(), "missing new state") {
		t.Fatalf("replacement state err = %v", err)
	}
}
