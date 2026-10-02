package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/config"
)

func writeSingletonLock(t *testing.T, dir, target string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
}

func TestManagedProfileHolder(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname")
	}
	oldAlive := processAlive
	t.Cleanup(func() { processAlive = oldAlive })
	processAlive = func(pid int) bool { return pid == 4242 }

	live := t.TempDir()
	writeSingletonLock(t, live, host+"-4242")
	if err := os.WriteFile(filepath.Join(live, "DevToolsActivePort"), []byte("63729\n/devtools/browser/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	holder, ok := managedProfileHolder(live)
	if !ok || holder.PID != 4242 || holder.DevToolsPort != 63729 {
		t.Fatalf("live holder = %+v, %v", holder, ok)
	}

	// The ".local" suffix Chrome sometimes records must still match.
	suffixed := t.TempDir()
	label, _, _ := strings.Cut(host, ".")
	writeSingletonLock(t, suffixed, label+".local-4242")
	if holder, ok := managedProfileHolder(suffixed); !ok || holder.DevToolsPort != 0 {
		t.Fatalf("suffixed host holder = %+v, %v", holder, ok)
	}

	for name, target := range map[string]string{
		"dead pid":   host + "-999",
		"other host": "some-other-machine-4242",
		"garbage":    "nonsense",
	} {
		dir := t.TempDir()
		writeSingletonLock(t, dir, target)
		if holder, ok := managedProfileHolder(dir); ok {
			t.Fatalf("%s: unexpected holder %+v", name, holder)
		}
	}
	if _, ok := managedProfileHolder(t.TempDir()); ok {
		t.Fatal("no lock must mean no holder")
	}
}

func TestProfileHolderLaunchErrorExplainsFix(t *testing.T) {
	msg := profileHolder{PID: 68576, DevToolsPort: 63729}.launchError("/x/clean/user-data", 50259).Error()
	for _, want := range []string{"already in use by another Chrome", "pid 68576", "DevTools port 63729", "port 50259", "/x/clean/user-data", "kill 68576"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("launch error missing %q:\n%s", want, msg)
		}
	}
	if msg := (profileHolder{PID: 7}).launchError("/d", 1).Error(); !strings.Contains(msg, "no DevTools port") {
		t.Fatalf("holder without DevTools port: %s", msg)
	}
}

func TestLaunchManagedBrowserFailsFastWhenProfileHeld(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	host, err := os.Hostname()
	if err != nil {
		t.Skip("no hostname")
	}
	oldFinder, oldCanConnect, oldCommand, oldAlive := browserExecutableFinder, canConnect, execCommand, processAlive
	t.Cleanup(func() {
		browserExecutableFinder, canConnect, execCommand, processAlive = oldFinder, oldCanConnect, oldCommand, oldAlive
	})
	browserExecutableFinder = func() string { return "/bin/echo" }
	canConnect = func(string, int) bool { return false }
	processAlive = func(pid int) bool { return pid == 4242 }
	launched := false
	execCommand = func(string, ...string) *exec.Cmd {
		launched = true
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	writeSingletonLock(t, config.ManagedUserDataDir(), host+"-4242")

	_, err = launchManagedBrowser(50259)
	if err == nil || !strings.Contains(err.Error(), "already in use by another Chrome (pid 4242") {
		t.Fatalf("want profile-in-use error, got %v", err)
	}
	if launched {
		t.Fatal("must not launch a second Chrome into a held profile")
	}
}

func TestFormatHTTPErrorIncludesReason(t *testing.T) {
	err := formatHTTPError(503, "503", []byte(`{"error":"Chrome not connected (CDP at 127.0.0.1:50259)","reason":"managed browser launch failed: profile in use"}`))
	if !strings.Contains(err.Error(), "Chrome not connected") || !strings.Contains(err.Error(), "reason: managed browser launch failed: profile in use") {
		t.Fatalf("reason dropped: %v", err)
	}
	err = formatHTTPError(400, "400", []byte(`{"error":"bad ref","reason":"bad ref"}`))
	if strings.Contains(err.Error(), "reason:") {
		t.Fatalf("duplicate reason must not repeat: %v", err)
	}
}

func TestEnsureDaemonReportsLaunchFailureInsteadOfMissingBrowser(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
		deny string
	}{
		"profile held":  {fmt.Errorf("no CDP endpoint found: %w", profileHolder{PID: 4242}.launchError("/d", 50259)), "could not start the managed browser", "Cannot find a Chromium-based browser"},
		"no executable": {fmt.Errorf("no CDP endpoint found: %w", errNoBrowserExecutable), "Cannot find a Chromium-based browser", "could not start"},
	} {
		t.Run(name, func(t *testing.T) {
			resetState()
			t.Cleanup(resetState)
			t.Setenv("BORZ_HOME", t.TempDir())
			oldDiscover := discoverCDPPort
			t.Cleanup(func() { discoverCDPPort = oldDiscover })
			discoverCDPPort = func() (*CDPEndpoint, error) { return nil, tc.err }

			err := EnsureDaemon()
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), tc.deny) {
				t.Fatalf("EnsureDaemon error = %v; want %q, not %q", err, tc.want, tc.deny)
			}
		})
	}
}
