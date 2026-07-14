package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

func runProfileCLI(t *testing.T, args ...string) string {
	t.Helper()
	oldArgs := os.Args
	os.Args = append([]string{"borz"}, args...)
	defer func() { os.Args = oldArgs }()
	return captureStdout(t, main)
}

func setupProfileHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	return home
}

func TestProfileCLIAddListShowSetRemove(t *testing.T) {
	home := setupProfileHome(t)

	out := runProfileCLI(t, "profile", "list")
	if !strings.Contains(out, "No profiles declared") {
		t.Fatalf("empty list output = %q", out)
	}

	out = runProfileCLI(t, "profile", "add", "mini", "--remote", "http://10.9.9.9:13333/", "--token", "seekrit", "--no-check")
	if !strings.Contains(out, `Profile "mini" added`) || !strings.Contains(out, "http://10.9.9.9:13333") {
		t.Fatalf("add output = %q", out)
	}
	if strings.Contains(out, "seekrit") {
		t.Fatalf("add output leaked the token: %q", out)
	}

	out = runProfileCLI(t, "profile", "add", "mdt", "--cdp", "127.0.0.1:19845", "--no-check")
	if !strings.Contains(out, `Profile "mdt" added`) || !strings.Contains(out, "http://127.0.0.1:19845") {
		t.Fatalf("cdp add output = %q", out)
	}
	out = runProfileCLI(t, "profile", "add", "clean", "--managed")
	if !strings.Contains(out, `Profile "clean" added`) || !strings.Contains(out, "local managed browser") {
		t.Fatalf("managed add output = %q", out)
	}

	// File is 0600 and holds the token.
	st, err := os.Stat(filepath.Join(home, "profiles.json"))
	if err != nil {
		t.Fatalf("stat profiles.json: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("profiles.json mode = %v, want 0600", st.Mode().Perm())
	}

	out = runProfileCLI(t, "profile", "list")
	for _, want := range []string{"mini", "remote", "http://10.9.9.9:13333", "mdt", "cdp", "clean", "managed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "seekrit") {
		t.Fatalf("list leaked the token:\n%s", out)
	}

	out = runProfileCLI(t, "profile", "show", "mini")
	if !strings.Contains(out, "Transport: remote") || !strings.Contains(out, "Token:     configured") {
		t.Fatalf("show output = %q", out)
	}
	if strings.Contains(out, "seekrit") {
		t.Fatalf("show leaked the token:\n%s", out)
	}
	out = runProfileCLI(t, "profile", "show", "nope")
	if !strings.Contains(out, "not declared") || !strings.Contains(out, "managed transport") {
		t.Fatalf("show undeclared output = %q", out)
	}

	out = runProfileCLI(t, "profile", "show", "mini", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("show --json: %v\n%s", err, out)
	}
	if payload["transport"] != "remote" || payload["tokenConfigured"] != true || payload["declared"] != true {
		t.Fatalf("show payload = %+v", payload)
	}

	out = runProfileCLI(t, "profile", "set", "mini", "--token", "next-token", "--no-check")
	if !strings.Contains(out, `Profile "mini" updated`) {
		t.Fatalf("set output = %q", out)
	}
	registry, err := borzprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if registry.Profiles["mini"].Token != "next-token" {
		t.Fatalf("set did not update token: %+v", registry.Profiles["mini"])
	}

	out = runProfileCLI(t, "profile", "set", "mdt", "--remote", "http://10.8.8.8:1111", "--no-check")
	if !strings.Contains(out, `Profile "mdt" updated`) || !strings.Contains(out, "http://10.8.8.8:1111") {
		t.Fatalf("transport switch output = %q", out)
	}
	registry, _ = borzprofile.Load()
	if entry := registry.Profiles["mdt"]; entry.Transport != "remote" || entry.CDPURL != "" {
		t.Fatalf("transport switch left stale cdp fields: %+v", entry)
	}

	out = runProfileCLI(t, "profile", "rm", "clean")
	if !strings.Contains(out, `Profile "clean" removed`) {
		t.Fatalf("rm output = %q", out)
	}
	registry, _ = borzprofile.Load()
	if _, ok := registry.Profiles["clean"]; ok {
		t.Fatal("rm did not delete the profile")
	}

	out = runProfileCLI(t, "profile", "list", "--json")
	if strings.Contains(out, "next-token") {
		t.Fatalf("list --json leaked the token:\n%s", out)
	}
}

func TestProfileCLIIdleTabTimeout(t *testing.T) {
	setupProfileHome(t)
	idleOf := func(name string) *int {
		t.Helper()
		registry, err := borzprofile.Load()
		if err != nil {
			t.Fatal(err)
		}
		return registry.Profiles[name].IdleTabTimeout
	}

	out := runProfileCLI(t, "profile", "add", "mdt", "--cdp", "127.0.0.1:19845", "--idle-tab-timeout", "0", "--no-check")
	if !strings.Contains(out, `Profile "mdt" added`) {
		t.Fatalf("add output = %q", out)
	}
	if got := idleOf("mdt"); got == nil || *got != 0 {
		t.Fatalf("stored idleTabTimeout = %v, want 0", got)
	}

	out = runProfileCLI(t, "profile", "list")
	if !strings.Contains(out, "[idleTabTimeout=0]") {
		t.Fatalf("list should surface the idle timeout:\n%s", out)
	}
	out = runProfileCLI(t, "profile", "show", "mdt")
	if !strings.Contains(out, "Idle tab timeout: 0 (idle-tab auto-close disabled)") {
		t.Fatalf("show output = %q", out)
	}
	out = runProfileCLI(t, "profile", "show", "mdt", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("show --json: %v\n%s", err, out)
	}
	if payload["idleTabTimeout"] != float64(0) {
		t.Fatalf("show payload = %+v", payload)
	}

	// set adjusts the field without touching the transport.
	runProfileCLI(t, "profile", "set", "mdt", "--idle-tab-timeout", "12", "--no-check")
	if got := idleOf("mdt"); got == nil || *got != 12 {
		t.Fatalf("idleTabTimeout after set = %v, want 12", got)
	}
	out = runProfileCLI(t, "profile", "show", "mdt")
	if !strings.Contains(out, "Idle tab timeout: 12 minutes") {
		t.Fatalf("show output = %q", out)
	}

	// Switching between managed and cdp keeps the field.
	runProfileCLI(t, "profile", "set", "mdt", "--managed")
	if got := idleOf("mdt"); got == nil || *got != 12 {
		t.Fatalf("idleTabTimeout after managed switch = %v, want 12", got)
	}
	runProfileCLI(t, "profile", "set", "mdt", "--cdp", "127.0.0.1:9222", "--no-check")
	if got := idleOf("mdt"); got == nil || *got != 12 {
		t.Fatalf("idleTabTimeout after cdp switch = %v, want 12", got)
	}

	// Switching to remote drops the field (it does not apply there).
	runProfileCLI(t, "profile", "set", "mdt", "--remote", "http://10.8.8.8:1111", "--no-check")
	if got := idleOf("mdt"); got != nil {
		t.Fatalf("idleTabTimeout should be dropped on remote switch, got %v", *got)
	}

	// 'default' clears the field so flag/env/default decide again.
	runProfileCLI(t, "profile", "set", "mdt", "--cdp", "127.0.0.1:9222", "--idle-tab-timeout", "5", "--no-check")
	runProfileCLI(t, "profile", "set", "mdt", "--idle-tab-timeout", "default", "--no-check")
	if got := idleOf("mdt"); got != nil {
		t.Fatalf("idleTabTimeout after 'default' = %v, want unset", *got)
	}
	out = runProfileCLI(t, "profile", "show", "mdt")
	if !strings.Contains(out, "Idle tab timeout: default (0 minutes") {
		t.Fatalf("show output = %q", out)
	}
}

func TestProfileCLIMaxTabs(t *testing.T) {
	setupProfileHome(t)
	maxTabsOf := func(name string) *int {
		registry, err := borzprofile.Load()
		if err != nil {
			t.Fatal(err)
		}
		return registry.Profiles[name].MaxTabs
	}

	out := runProfileCLI(t, "profile", "add", "bounded", "--managed", "--max-tabs", "30")
	if got := maxTabsOf("bounded"); got == nil || *got != 30 {
		t.Fatalf("stored maxTabs = %v, want 30", got)
	}
	if !strings.Contains(runProfileCLI(t, "profile", "list"), "[maxTabs=30]") {
		t.Fatal("profile list should surface maxTabs")
	}
	if !strings.Contains(runProfileCLI(t, "profile", "show", "bounded"), "Max tabs:         30") {
		t.Fatal("profile show should surface maxTabs")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(runProfileCLI(t, "profile", "show", "bounded", "--json")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["maxTabs"] != float64(30) {
		t.Fatalf("show payload = %+v", payload)
	}

	runProfileCLI(t, "profile", "set", "bounded", "--max-tabs", "0")
	if got := maxTabsOf("bounded"); got == nil || *got != 0 {
		t.Fatalf("maxTabs=0 = %v", got)
	}
	out = runProfileCLI(t, "profile", "show", "bounded")
	if !strings.Contains(out, "0 (tab cap disabled)") {
		t.Fatalf("show output = %q", out)
	}
	runProfileCLI(t, "profile", "set", "bounded", "--max-tabs", "default")
	if got := maxTabsOf("bounded"); got != nil {
		t.Fatalf("default should clear maxTabs, got %v", *got)
	}
	if !strings.Contains(runProfileCLI(t, "profile", "show", "bounded"), "default (30") {
		t.Fatal("profile show should surface the default maxTabs")
	}

	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() {
			runMainArgsForExit("profile", "add", "remote-cap", "--remote", "http://10.0.0.1:1", "--max-tabs", "30", "--no-check")
		})
	})
	if !strings.Contains(errOut, "maxTabs does not apply to the remote transport") {
		t.Fatalf("remote maxTabs stderr = %q", errOut)
	}
}

func TestProfileCLIErrors(t *testing.T) {
	setupProfileHome(t)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"profile", "add", "bad/name", "--managed"}, "portable single path segment"},
		{[]string{"profile", "add", "x"}, "Usage: borz profile add"},
		{[]string{"profile", "add", "x", "--managed", "--cdp", "127.0.0.1:1"}, "mutually exclusive"},
		{[]string{"profile", "add", "x", "--managed", "--token", "t"}, "--token only applies to remote profiles"},
		{[]string{"profile", "add", "x", "--cdp", "not a url", "--no-check"}, "invalid cdpUrl"},
		{[]string{"profile", "add", "x", "--remote", "http://10.0.0.1:1", "--idle-tab-timeout", "0", "--no-check"}, "does not apply to the remote transport"},
		{[]string{"profile", "add", "x", "--cdp", "127.0.0.1:1", "--idle-tab-timeout", "-3", "--no-check"}, "--idle-tab-timeout must be"},
		{[]string{"profile", "add", "x", "--cdp", "127.0.0.1:1", "--idle-tab-timeout", "soon", "--no-check"}, "--idle-tab-timeout must be"},
		{[]string{"profile", "set", "ghost", "--managed"}, "not declared"},
		{[]string{"profile", "rm", "ghost"}, "not declared"},
		{[]string{"profile", "show"}, "Usage: borz profile show"},
		{[]string{"profile", "frobnicate"}, "profile"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			errOut := captureStderr(t, func() {
				expectExit(t, 1, func() { runMainArgsForExit(tc.args...) })
			})
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("stderr = %q, want substring %q", errOut, tc.want)
			}
		})
	}

	// Duplicate add is rejected, pointing at 'profile set'.
	runProfileCLI(t, "profile", "add", "dup", "--managed")
	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("profile", "add", "dup", "--managed") })
	})
	if !strings.Contains(errOut, "already exists") {
		t.Fatalf("duplicate add stderr = %q", errOut)
	}
}

func TestProfileCLIAddProbesTargets(t *testing.T) {
	setupProfileHome(t)

	remoteProbes := 0
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected remote path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		remoteProbes++
		w.Write([]byte(`{"running":true}`))
	}))
	defer remoteSrv.Close()
	runProfileCLI(t, "profile", "add", "mini", "--remote", remoteSrv.URL, "--token", "tok")
	if remoteProbes != 1 {
		t.Fatalf("remote probe count = %d, want 1", remoteProbes)
	}

	cdpProbes := 0
	cdpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected cdp path %s", r.URL.Path)
		}
		cdpProbes++
		w.Write([]byte(`{"Browser":"Chrome"}`))
	}))
	defer cdpSrv.Close()
	runProfileCLI(t, "profile", "add", "mdt", "--cdp", strings.TrimPrefix(cdpSrv.URL, "http://"))
	if cdpProbes != 1 {
		t.Fatalf("cdp probe count = %d, want 1", cdpProbes)
	}

	// A dead target fails the add and keeps the registry unchanged.
	remoteSrv.Close()
	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("profile", "add", "dead", "--remote", remoteSrv.URL) })
	})
	if !strings.Contains(errOut, "--no-check") {
		t.Fatalf("dead probe stderr = %q", errOut)
	}
	registry, err := borzprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Profiles["dead"]; ok {
		t.Fatal("failed probe still wrote the profile")
	}
}

func TestProfileCLIAddRemoteTokenFallsBackToEnv(t *testing.T) {
	setupProfileHome(t)
	t.Setenv("BORZ_TOKEN", "env-token")
	runProfileCLI(t, "profile", "add", "mini", "--remote", "http://10.0.0.5:1111", "--no-check")
	registry, err := borzprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if registry.Profiles["mini"].Token != "env-token" {
		t.Fatalf("token = %q, want env fallback", registry.Profiles["mini"].Token)
	}
}

func TestProfileCLISetRemoteTokenFallsBackToEnv(t *testing.T) {
	setupProfileHome(t)
	t.Setenv("BORZ_TOKEN", "")
	t.Setenv("BB_BROWSER_TOKEN", "")
	tokenOf := func() string {
		t.Helper()
		registry, err := borzprofile.Load()
		if err != nil {
			t.Fatal(err)
		}
		return registry.Profiles["mini"].Token
	}

	runProfileCLI(t, "profile", "add", "mini", "--remote", "http://10.0.0.5:1111", "--token", "old-token", "--no-check")

	// Without --token and without an env token, set keeps the stored token.
	runProfileCLI(t, "profile", "set", "mini", "--remote", "http://10.0.0.5:2222", "--no-check")
	if got := tokenOf(); got != "old-token" {
		t.Fatalf("token after set without env = %q, want stored token kept", got)
	}

	// With BORZ_TOKEN, set resolves the token exactly like add/client setup:
	// the env token replaces the stored one.
	t.Setenv("BORZ_TOKEN", "env-token")
	runProfileCLI(t, "profile", "set", "mini", "--remote", "http://10.0.0.5:3333", "--no-check")
	if got := tokenOf(); got != "env-token" {
		t.Fatalf("token after set with BORZ_TOKEN = %q, want env fallback", got)
	}

	// An explicit --token still wins over the env.
	runProfileCLI(t, "profile", "set", "mini", "--remote", "http://10.0.0.5:4444", "--token", "explicit", "--no-check")
	if got := tokenOf(); got != "explicit" {
		t.Fatalf("token after explicit --token = %q", got)
	}
}

func TestMainBareRemotePrintsDeprecationWarning(t *testing.T) {
	setupProfileHome(t)
	errOut := captureStderr(t, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "--remote", "client", "status"}
		defer func() { os.Args = oldArgs }()
		captureStdout(t, main)
	})
	if !strings.Contains(errOut, "deprecated") || !strings.Contains(errOut, "--profile remote") {
		t.Fatalf("bare --remote stderr = %q, want deprecation warning pointing at --profile remote", errOut)
	}

	// 'borz profile ... --remote <url>' reuses the flag as a value flag; the
	// deprecation warning must not fire there.
	errOut = captureStderr(t, func() {
		runProfileCLI(t, "profile", "add", "quiet", "--remote", "http://10.0.0.5:1111", "--no-check")
	})
	if strings.Contains(errOut, "deprecated") {
		t.Fatalf("profile add --remote stderr = %q, want no deprecation warning", errOut)
	}
}

func TestMainRemoteAndProfileFlagsAreMutuallyExclusive(t *testing.T) {
	setupProfileHome(t)
	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("--remote", "--profile", "mini", "status") })
	})
	if !strings.Contains(errOut, "mutually exclusive") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestMainBareRemoteWithoutConfigFailsLoudly(t *testing.T) {
	setupProfileHome(t)
	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("--remote", "status") })
	})
	if !strings.Contains(errOut, "not configured") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestMainClientJSONMigratesToRemoteProfile(t *testing.T) {
	home := setupProfileHome(t)
	legacy := `{"url":"http://10.7.7.7:1234","token":"legacy-token","enabled":true}` + "\n"
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	out := runProfileCLI(t, "profile", "list")
	if !strings.Contains(out, "remote") || !strings.Contains(out, "http://10.7.7.7:1234") {
		t.Fatalf("migrated list output = %q", out)
	}
	if strings.Contains(out, "legacy-token") {
		t.Fatalf("list leaked migrated token: %q", out)
	}
	// client.json is preserved for rollback.
	data, err := os.ReadFile(filepath.Join(home, "client.json"))
	if err != nil || string(data) != legacy {
		t.Fatalf("client.json changed: %q err=%v", data, err)
	}
}

func TestDaemonAndServerLifecycleOnRemoteProfile(t *testing.T) {
	home := setupProfileHome(t)
	if err := os.WriteFile(filepath.Join(home, "profiles.json"),
		[]byte(`{"version":1,"profiles":{"mini":{"transport":"remote","url":"http://10.0.0.9:1333"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_PROFILE", "mini")

	out := runProfileCLI(t, "daemon", "status")
	if !strings.Contains(out, "remote profile") || !strings.Contains(out, "no local daemon") {
		t.Fatalf("daemon status output = %q", out)
	}
	out = runProfileCLI(t, "server", "status")
	if !strings.Contains(out, "remote profile") || !strings.Contains(out, "no local server") {
		t.Fatalf("server status output = %q", out)
	}
	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("daemon", "stop") })
	})
	if !strings.Contains(errOut, "no local daemon") {
		t.Fatalf("daemon stop stderr = %q", errOut)
	}
	errOut = captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("server", "shutdown") })
	})
	if !strings.Contains(errOut, "no local server") {
		t.Fatalf("server shutdown stderr = %q", errOut)
	}
}

func TestFirstPositionalArg(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"profile", "add", "x", "--remote", "http://u"}, "profile"},
		{[]string{"--json", "profile", "list"}, "profile"},
		{[]string{"--profile", "mini", "open", "https://x"}, "open"},
		{[]string{"--tab", "t1", "--remote", "open", "u"}, "open"},
		{[]string{"--json"}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstPositionalArg(tc.args); got != tc.want {
			t.Errorf("firstPositionalArg(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
