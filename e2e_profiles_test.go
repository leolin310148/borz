package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

// startE2EVerifySite spins up the local verification site with cleanup.
func startE2EVerifySite(t *testing.T) *e2everify.Site {
	t.Helper()
	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})
	return site
}

func writeE2EProfiles(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write profiles.json: %v", err)
	}
}

// TestE2EProfilesZeroConfigUnchanged locks in the compatibility guarantee:
// on a machine without profiles.json, bare commands and --profile default
// behave exactly as before and nothing writes a profiles.json.
func TestE2EProfilesZeroConfigUnchanged(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	site := startE2EVerifySite(t)
	env := startE2EDaemon(t, home)

	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open returned no tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.title", "E2E Verify Home")

	// --profile default is the same profile: same daemon, same tab.
	tabs := runE2EJSON(t, env, "--profile", "default", "tab", "list", "--json")
	found := false
	for _, entry := range tabs.Data.Tabs {
		if entry.Tab == tab {
			found = true
		}
	}
	if !found {
		t.Fatalf("--profile default did not see the default profile's tab %q: %+v", tab, tabs.Data.Tabs)
	}

	// Zero perception also means zero new files: no profiles.json appears,
	// and no per-profile runtime dir was created for 'default'.
	if _, err := os.Stat(filepath.Join(home, "profiles.json")); !os.IsNotExist(err) {
		t.Fatalf("plain usage must not create profiles.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "default")); !os.IsNotExist(err) {
		t.Fatalf("--profile default must keep using the top-level runtime dir: %v", err)
	}
}

// TestE2EProfileCDPTransportAttachesWithoutLaunching declares a cdp profile
// pointing at the already-running Chrome and verifies borz attaches to it
// without ever launching a managed browser for that profile. Uses a real
// built binary because the profile daemon is auto-spawned from the CLI's own
// executable.
func TestE2EProfileCDPTransportAttachesWithoutLaunching(t *testing.T) {
	skipUnlessE2E(t)

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	home := t.TempDir()
	site := startE2EVerifySite(t)
	bin := filepath.Join(t.TempDir(), "borz")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build borz e2e binary: %v\n%s", buildErr, out)
	}

	const profileName = "e2e-cdp-attach"
	writeE2EProfiles(t, home, fmt.Sprintf(
		`{"version":1,"profiles":{%q:{"transport":"cdp","cdpUrl":"http://%s:%d"}}}`,
		profileName, ep.Host, ep.Port,
	))

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "BORZ_HOME="+home)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), runErr, out)
		}
		return string(out)
	}
	runJSON := func(args ...string) protocol.Response {
		t.Helper()
		out := run(args...)
		var resp protocol.Response
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !resp.Success || resp.Data == nil {
			t.Fatalf("borz %s returned unsuccessful response: %+v", strings.Join(args, " "), resp)
		}
		return resp
	}

	var tab string
	t.Cleanup(func() {
		if tab != "" {
			cmd := exec.Command(bin, "--profile", profileName, "close", "--tab", tab, "--json")
			cmd.Env = append(os.Environ(), "BORZ_HOME="+home)
			_ = cmd.Run()
		}
		daemonPath := filepath.Join(home, "profiles", profileName, "daemon.json")
		if raw, readErr := os.ReadFile(daemonPath); readErr == nil {
			var info protocol.DaemonInfo
			_ = json.Unmarshal(raw, &info)
			cmd := exec.Command(bin, "daemon", "shutdown", "--profile", profileName)
			cmd.Env = append(os.Environ(), "BORZ_HOME="+home)
			_ = cmd.Run()
			if info.PID > 0 && !client.WaitForProcessExit(info.PID, 3*time.Second) {
				if process, findErr := os.FindProcess(info.PID); findErr == nil {
					_ = process.Kill()
				}
			}
		}
	})

	opened := runJSON("--profile", profileName, "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json")
	tab = opened.Data.Tab
	if tab == "" {
		t.Fatalf("cdp profile open returned no tab: %+v", opened.Data)
	}
	title := runJSON("--profile", profileName, "eval", "document.title", "--tab", tab, "--json")
	if title.Data.Result != "E2E Verify Page Two" {
		t.Fatalf("cdp profile eval title = %#v", title.Data.Result)
	}

	// The profile daemon must be pinned to the declared endpoint...
	status := run("daemon", "status", "--profile", profileName)
	requireContains(t, status, fmt.Sprintf(`"cdpPort": %d`, ep.Port), "cdp profile daemon status")
	requireContains(t, status, `"cdpConnected": true`, "cdp profile daemon status")
	// ...and it must never have launched a managed browser of its own.
	if _, err := os.Stat(filepath.Join(home, "profiles", profileName, "browser")); !os.IsNotExist(err) {
		t.Fatalf("cdp profile grew a managed browser dir: %v", err)
	}
}

// TestE2EProfileCDPDeadEndpointFailsLoudly verifies the no-fallback rule: a
// cdp profile whose endpoint is down errors out without launching a browser
// or a daemon.
func TestE2EProfileCDPDeadEndpointFailsLoudly(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	env := e2eDaemonEnv{home: home}
	deadPort := freeTCPPort(t)
	const profileName = "e2e-cdp-dead"
	writeE2EProfiles(t, home, fmt.Sprintf(
		`{"version":1,"profiles":{%q:{"transport":"cdp","cdpHost":"127.0.0.1","cdpPort":%d}}}`,
		profileName, deadPort,
	))

	_, out := runE2ECLIError(t, env, "--profile", profileName, "open", "https://example.com")
	requireContains(t, out, "unreachable", "dead cdp endpoint error")
	requireContains(t, out, fmt.Sprintf("127.0.0.1:%d", deadPort), "dead cdp endpoint error")
	requireNotContains(t, out, "Cannot find a Chromium-based browser", "dead cdp endpoint error")

	profileDir := filepath.Join(home, "profiles", profileName)
	if _, err := os.Stat(filepath.Join(profileDir, "browser")); !os.IsNotExist(err) {
		t.Fatalf("dead cdp endpoint launched a managed browser: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profileDir, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("dead cdp endpoint still spawned a daemon: %v", err)
	}
}

// TestE2EProfileRemoteTransportNeverSpawnsLocally routes a remote-transport
// profile at a live borz server and verifies no local daemon appears for it.
func TestE2EProfileRemoteTransportNeverSpawnsLocally(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	site := startE2EVerifySite(t)

	token := "e2e-profile-remote-token"
	env, serverURL := startE2EServer(t, home, token)
	const profileName = "e2e-mini"
	writeE2EProfiles(t, home, fmt.Sprintf(
		`{"version":1,"profiles":{%q:{"transport":"remote","url":%q,"token":%q}}}`,
		profileName, serverURL, token,
	))

	statusOut := runE2ECLI(t, env, "--profile", profileName, "status")
	requireContains(t, statusOut, `"cdpConnected": true`, "remote profile status")

	opened := runE2EJSON(t, env, "--profile", profileName, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("remote profile open returned no tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2ECLI(t, env, "--profile", profileName, "close", "--tab", tab, "--json") })
	requireEvalStringWithPrefix(t, env, []string{"--profile", profileName, "--tab", tab}, "document.title", "E2E Verify Home")

	// No local daemon state may exist for a remote profile.
	if _, err := os.Stat(filepath.Join(home, "profiles", profileName)); !os.IsNotExist(err) {
		t.Fatalf("remote profile grew a local runtime dir: %v", err)
	}
	lifecycle := runE2ECLI(t, env, "daemon", "status", "--profile", profileName)
	requireContains(t, lifecycle, "remote profile", "remote profile daemon status")
	requireContains(t, lifecycle, "no local daemon", "remote profile daemon status")
}

// TestE2EProfileLegacyClientJSONMigration verifies the client.json → profiles.json
// migration and the deprecated --remote alias end to end.
func TestE2EProfileLegacyClientJSONMigration(t *testing.T) {
	skipUnlessE2E(t)

	serverHome := t.TempDir()
	t.Setenv("BORZ_HOME", serverHome)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	token := "e2e-migration-token"
	_, serverURL := startE2EServer(t, serverHome, token)

	clientHome := t.TempDir()
	clientEnv := e2eDaemonEnv{home: clientHome}
	legacy := fmt.Sprintf(`{"url":%q,"token":%q,"enabled":true}`+"\n", serverURL, token)
	if err := os.WriteFile(filepath.Join(clientHome, "client.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy client.json: %v", err)
	}

	// Bare --remote resolves through the migrated 'remote' profile.
	statusOut := runE2ECLI(t, clientEnv, "--remote", "status")
	requireContains(t, statusOut, `"cdpConnected": true`, "legacy --remote status")

	migratedPath := filepath.Join(clientHome, "profiles.json")
	first, err := os.ReadFile(migratedPath)
	if err != nil {
		t.Fatalf("migration did not write profiles.json: %v", err)
	}
	requireContains(t, string(first), `"transport": "remote"`, "migrated profiles.json")
	requireNotContains(t, string(first), `"enabled"`, "migrated profiles.json")
	st, statErr := os.Stat(migratedPath)
	if statErr != nil {
		t.Fatalf("stat migrated profiles.json: %v", statErr)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("migrated profiles.json mode = %v, want 0600", st.Mode().Perm())
	}
	if data, readErr := os.ReadFile(filepath.Join(clientHome, "client.json")); readErr != nil || string(data) != legacy {
		t.Fatalf("client.json changed during migration: %q err=%v", data, readErr)
	}

	// Idempotent: a second command leaves the registry byte-identical.
	runE2ECLI(t, clientEnv, "--remote", "status")
	again, err := os.ReadFile(migratedPath)
	if err != nil || string(again) != string(first) {
		t.Fatalf("migration is not idempotent: err=%v\nfirst:\n%s\nsecond:\n%s", err, first, again)
	}

	// The old silent no-op is now an explicit error.
	_, out := runE2ECLIError(t, clientEnv, "--remote", "--profile", "other", "status")
	requireContains(t, out, "mutually exclusive", "--remote with --profile")

	// No local daemon may have been spawned anywhere in the client home.
	if _, err := os.Stat(filepath.Join(clientHome, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy --remote spawned a local daemon: %v", err)
	}
	if entries, globErr := filepath.Glob(filepath.Join(clientHome, "profiles", "*", "daemon.json")); globErr != nil || len(entries) != 0 {
		t.Fatalf("legacy --remote spawned a profile daemon: %v (err %v)", entries, globErr)
	}
	if !strings.Contains(string(first), serverURL) {
		t.Fatalf("migrated profile does not point at the server: %s", first)
	}
}
