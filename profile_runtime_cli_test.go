package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

func writeRuntimeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stubCloseManagedBrowser replaces the real CDP shutdown for the duration of a
// test; performPurge must never talk to a real Chrome from the unit suite.
func stubCloseManagedBrowser(t *testing.T, err error) *int {
	t.Helper()
	calls := 0
	old := closeManagedBrowser
	closeManagedBrowser = func(runtimeView) error {
		calls++
		return err
	}
	t.Cleanup(func() { closeManagedBrowser = old })
	return &calls
}

func TestRuntimeStatusOrdering(t *testing.T) {
	cases := []struct {
		name string
		view runtimeView
		want string
	}{
		{"both alive", runtimeView{DaemonAlive: true, BrowserAlive: true}, statusLive},
		{"daemon only", runtimeView{DaemonAlive: true}, statusDaemonOnly},
		// A daemon-less Chrome outranks the disk checks: it is the only status
		// that costs the user memory right now.
		{"browser only", runtimeView{
			Runtime:      borzprofile.Runtime{HasRuntimeDir: true, HasLogsDir: true},
			BrowserAlive: true,
		}, statusBrowserOnly},
		{"idle", runtimeView{Runtime: borzprofile.Runtime{HasRuntimeDir: true, HasLogsDir: true}}, statusIdle},
		{"logs only", runtimeView{Runtime: borzprofile.Runtime{HasLogsDir: true}}, statusLogsOnly},
		{"nothing local", runtimeView{}, statusNoLocalState},
	}
	for _, c := range cases {
		if got := runtimeStatus(c.view); got != c.want {
			t.Fatalf("%s: status = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestInspectRuntimeViewTransportFromRegistry(t *testing.T) {
	registry := &borzprofile.File{Profiles: map[string]borzprofile.Entry{
		"mini":  {Transport: "remote", URL: "http://10.0.0.1:13333"},
		"blank": {},
	}}
	// Declared with an explicit transport.
	if got := inspectRuntimeView(borzprofile.Runtime{Name: "mini"}, registry); got.Transport != "remote" {
		t.Fatalf("mini transport = %q", got.Transport)
	}
	// Declared but transport-less, and undeclared entirely, both fall back to
	// managed — the same rule ResolveTarget applies.
	if got := inspectRuntimeView(borzprofile.Runtime{Name: "blank"}, registry); got.Transport != "managed" {
		t.Fatalf("blank transport = %q", got.Transport)
	}
	if got := inspectRuntimeView(borzprofile.Runtime{Name: "adhoc"}, registry); got.Transport != "managed" {
		t.Fatalf("adhoc transport = %q", got.Transport)
	}
	// A recorded PID that no longer exists must not read as alive.
	dead := inspectRuntimeView(borzprofile.Runtime{Name: "adhoc", DaemonPID: 0x7FFFFFFF}, registry)
	if dead.DaemonAlive {
		t.Fatal("bogus pid reported alive")
	}
}

func TestProfileListAllShowsUndeclaredProfiles(t *testing.T) {
	home := setupProfileHome(t)
	writeRuntimeFile(t, filepath.Join(home, "profiles.json"),
		`{"version":1,"profiles":{"mini":{"transport":"remote","url":"http://10.0.0.1:13333"}}}`)
	writeRuntimeFile(t, filepath.Join(home, "profiles", "adhoc", "browser", "user-data", "blob"), "0123456789")
	writeRuntimeFile(t, filepath.Join(home, "logs", "ghost", "daemon.jsonl"), "x\n")

	// Plain `profile list` only knows the registry.
	plain := runProfileCLI(t, "profile", "list")
	if strings.Contains(plain, "adhoc") || strings.Contains(plain, "ghost") {
		t.Fatalf("plain list leaked runtime-only profiles: %q", plain)
	}

	out := runProfileCLI(t, "profile", "list", "--all")
	for _, want := range []string{"default", "adhoc", "ghost", "mini", "LAST USED", "Total on disk"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list --all missing %q: %q", want, out)
		}
	}
	// "default" is built-in, so it must not be counted among the strays the
	// hint is warning about — only adhoc and ghost are.
	if !strings.Contains(out, "2 profile(s) are undeclared") {
		t.Fatalf("undeclared hint missing or miscounted: %q", out)
	}
	if !strings.Contains(out, "built-in") {
		t.Fatalf("default not labelled built-in: %q", out)
	}
	if !strings.Contains(out, statusIdle) || !strings.Contains(out, statusLogsOnly) {
		t.Fatalf("statuses missing: %q", out)
	}
}

func TestProfileListAllJSON(t *testing.T) {
	home := setupProfileHome(t)
	writeRuntimeFile(t, filepath.Join(home, "profiles", "adhoc", "browser", "browser.json"),
		`{"port":19901,"browserId":"BID-1"}`)
	writeRuntimeFile(t, filepath.Join(home, "profiles", "adhoc", "daemon.json"), `{"pid":2147483647,"port":19902}`)

	var payload struct {
		Path     string `json:"path"`
		Profiles []struct {
			Name         string `json:"name"`
			Declared     bool   `json:"declared"`
			Transport    string `json:"transport"`
			Status       string `json:"status"`
			DaemonPID    int    `json:"daemonPid"`
			BrowserPort  int    `json:"browserPort"`
			BrowserBytes int64  `json:"browserBytes"`
			LastUsed     string `json:"lastUsed"`
		} `json:"profiles"`
	}
	out := runProfileCLI(t, "profile", "list", "--all", "--json")
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if payload.Path != filepath.Join(home, "profiles.json") {
		t.Fatalf("path = %q", payload.Path)
	}
	if len(payload.Profiles) != 2 {
		t.Fatalf("profiles = %+v", payload.Profiles)
	}
	adhoc := payload.Profiles[1]
	if adhoc.Name != "adhoc" || adhoc.Declared || adhoc.Transport != "managed" {
		t.Fatalf("adhoc = %+v", adhoc)
	}
	if adhoc.DaemonPID != 2147483647 || adhoc.BrowserPort != 19901 || adhoc.BrowserBytes == 0 {
		t.Fatalf("adhoc state = %+v", adhoc)
	}
	if adhoc.LastUsed == "" {
		t.Fatalf("adhoc lastUsed missing: %+v", adhoc)
	}
	// Nothing is running under a temp home, so the disk-only status wins.
	if adhoc.Status != statusIdle {
		t.Fatalf("adhoc status = %q, want %q", adhoc.Status, statusIdle)
	}
	// The default profile has no state at all in a fresh home.
	if def := payload.Profiles[0]; def.Name != "default" || def.Status != statusNoLocalState {
		t.Fatalf("default = %+v", def)
	}
}

func TestProfilePurgeRefusesDefaultAndSelf(t *testing.T) {
	setupProfileHome(t)
	t.Cleanup(func() { _ = config.SetProfile("") })

	for _, name := range []string{"default", "DEFAULT", ""} {
		expectExit(t, 1, func() { handleProfilePurge(name, nil, false) })
	}
	// Purging the profile the command itself runs as would delete the runtime
	// directory out from under the daemon this process just talked to.
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() { handleProfilePurge("work", nil, false) })
	// A different profile is fine.
	if err := config.SetProfile(""); err != nil {
		t.Fatal(err)
	}
	expectExit(t, 1, func() { handleProfilePurge("bad/name", nil, false) })
}

func TestProfilePurgeNothingToPurge(t *testing.T) {
	home := setupProfileHome(t)
	writeRuntimeFile(t, filepath.Join(home, "profiles.json"),
		`{"version":1,"profiles":{"mini":{"transport":"remote","url":"http://10.0.0.1:13333"}}}`)

	out := captureStdout(t, func() { handleProfilePurge("mini", nil, false) })
	if !strings.Contains(out, "no local runtime state") {
		t.Fatalf("output = %q", out)
	}
	// Purge reclaims disk, not declarations — say so instead of silently
	// leaving the user thinking the profile is gone.
	if !strings.Contains(out, "profile rm mini") {
		t.Fatalf("missing rm hint: %q", out)
	}

	out = captureStdout(t, func() { handleProfilePurge("mini", nil, true) })
	var payload struct {
		Name   string `json:"name"`
		Purged bool   `json:"purged"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if payload.Name != "mini" || payload.Purged || payload.Reason == "" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestProfilePurgePreviewsBeforeDeleting(t *testing.T) {
	home := setupProfileHome(t)
	runtimeDir := filepath.Join(home, "profiles", "adhoc")
	logsDir := filepath.Join(home, "logs", "adhoc")
	writeRuntimeFile(t, filepath.Join(runtimeDir, "browser", "user-data", "blob"), "0123456789")
	writeRuntimeFile(t, filepath.Join(logsDir, "daemon.jsonl"), "x\n")

	out := captureStdout(t, func() { handleProfilePurge("adhoc", nil, false) })
	if !strings.Contains(out, "Would purge") || !strings.Contains(out, "Nothing was deleted") {
		t.Fatalf("preview output = %q", out)
	}
	if !strings.Contains(out, runtimeDir) {
		t.Fatalf("preview did not name the directory: %q", out)
	}
	// Logs are kept unless asked for, and the preview must say so.
	if strings.Contains(out, "delete "+logsDir) || !strings.Contains(out, "add --logs") {
		t.Fatalf("logs handling wrong: %q", out)
	}
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("preview deleted the runtime directory: %v", err)
	}

	// --force without --logs removes runtime state only.
	out = captureStdout(t, func() { handleProfilePurge("adhoc", []string{"--force"}, false) })
	if !strings.Contains(out, "Purged profile") || !strings.Contains(out, "deleted "+runtimeDir) {
		t.Fatalf("purge output = %q", out)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime directory survived: %v", err)
	}
	if _, err := os.Stat(logsDir); err != nil {
		t.Fatalf("logs were removed without --logs: %v", err)
	}

	// --logs finishes the job.
	out = captureStdout(t, func() { handleProfilePurge("adhoc", []string{"--force", "--logs"}, false) })
	if !strings.Contains(out, "deleted "+logsDir) {
		t.Fatalf("logs purge output = %q", out)
	}
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Fatalf("logs directory survived: %v", err)
	}
}

func TestProfilePurgeJSONMentionsDeclaration(t *testing.T) {
	home := setupProfileHome(t)
	writeRuntimeFile(t, filepath.Join(home, "profiles.json"),
		`{"version":1,"profiles":{"volvo":{"transport":"managed"}}}`)
	writeRuntimeFile(t, filepath.Join(home, "profiles", "volvo", "browser", "blob"), "0123456789")

	out := captureStdout(t, func() { handleProfilePurge("volvo", nil, true) })
	var preview struct {
		Purged      bool     `json:"purged"`
		DryRun      bool     `json:"dryRun"`
		WouldRemove []string `json:"wouldRemove"`
		Bytes       int64    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if preview.Purged || !preview.DryRun || len(preview.WouldRemove) != 1 || preview.Bytes != 10 {
		t.Fatalf("preview = %+v", preview)
	}

	out = captureStdout(t, func() { handleProfilePurge("volvo", []string{"--force"}, true) })
	var done struct {
		Purged bool     `json:"purged"`
		Steps  []string `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &done); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if !done.Purged || len(done.Steps) != 1 {
		t.Fatalf("done = %+v", done)
	}
	// The registry entry is deliberately left alone.
	registry, err := borzprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Profiles["volvo"]; !ok {
		t.Fatal("purge removed the declaration")
	}
}

func TestPurgeTargetsAndBytes(t *testing.T) {
	view := runtimeView{
		Runtime: borzprofile.Runtime{
			Name: "adhoc", RuntimeDir: "/rt", LogsDir: "/logs",
			HasRuntimeDir: true, HasLogsDir: true,
			BrowserPort: 19901, DaemonPID: 4242,
			BrowserBytes: 2048, LogBytes: 1024,
		},
		DaemonAlive: true, BrowserAlive: true,
	}
	targets := purgeTargets(view, true)
	if len(targets) != 4 {
		t.Fatalf("targets = %v", targets)
	}
	// Order is the order performPurge acts in: daemon, browser, then files.
	for i, want := range []string{"stop daemon (pid 4242)", "close managed browser on port 19901", "/rt", "/logs"} {
		if !strings.Contains(targets[i], want) {
			t.Fatalf("targets[%d] = %q, want %q", i, targets[i], want)
		}
	}
	if got := purgeBytes(view, true); got != 3072 {
		t.Fatalf("bytes with logs = %d", got)
	}
	if got := purgeBytes(view, false); got != 2048 {
		t.Fatalf("bytes without logs = %d", got)
	}
	if got := purgeTargets(view, false); len(got) != 3 {
		t.Fatalf("targets without logs = %v", got)
	}
	if got := purgeTargets(runtimeView{}, true); got != nil {
		t.Fatalf("empty profile targets = %v", got)
	}
}

func TestPerformPurgeClosesBrowserThenDeletes(t *testing.T) {
	home := t.TempDir()
	runtimeDir := filepath.Join(home, "profiles", "adhoc")
	logsDir := filepath.Join(home, "logs", "adhoc")
	writeRuntimeFile(t, filepath.Join(runtimeDir, "browser", "blob"), "x")
	writeRuntimeFile(t, filepath.Join(logsDir, "daemon.jsonl"), "x")

	calls := stubCloseManagedBrowser(t, nil)
	view := runtimeView{
		Runtime: borzprofile.Runtime{
			Name: "adhoc", RuntimeDir: runtimeDir, LogsDir: logsDir,
			HasRuntimeDir: true, HasLogsDir: true, BrowserPort: 19901,
		},
		BrowserAlive: true,
	}
	steps := performPurge(view, true)
	if *calls != 1 {
		t.Fatalf("closeManagedBrowser calls = %d", *calls)
	}
	if len(steps) != 3 || !strings.Contains(steps[0], "closed managed browser") {
		t.Fatalf("steps = %v", steps)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime dir survived: %v", err)
	}
	if _, err := os.Stat(logsDir); !os.IsNotExist(err) {
		t.Fatalf("logs dir survived: %v", err)
	}
}

func TestPerformPurgeReportsBrowserCloseFailureAndStillDeletes(t *testing.T) {
	home := t.TempDir()
	runtimeDir := filepath.Join(home, "profiles", "adhoc")
	writeRuntimeFile(t, filepath.Join(runtimeDir, "browser", "blob"), "x")

	stubCloseManagedBrowser(t, errors.New("not the recorded browser"))
	view := runtimeView{
		Runtime: borzprofile.Runtime{
			Name: "adhoc", RuntimeDir: runtimeDir, HasRuntimeDir: true, BrowserPort: 19901,
		},
		BrowserAlive: true,
	}
	steps := performPurge(view, false)
	if len(steps) != 2 || !strings.Contains(steps[0], "could not close managed browser") {
		t.Fatalf("steps = %v", steps)
	}
	// A browser we could not (or should not) close must not block reclaiming
	// the disk the user asked for.
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime dir survived: %v", err)
	}
}

func TestPerformPurgeReportsUndeletableDirectory(t *testing.T) {
	home := t.TempDir()
	// A path whose parent is a regular file cannot be removed as a directory.
	blocker := filepath.Join(home, "blocker")
	writeRuntimeFile(t, blocker, "x")
	view := runtimeView{Runtime: borzprofile.Runtime{
		Name: "adhoc", RuntimeDir: filepath.Join(blocker, "rt"), LogsDir: filepath.Join(blocker, "logs"),
		HasRuntimeDir: true, HasLogsDir: true,
	}}
	steps := performPurge(view, true)
	if len(steps) != 2 {
		t.Fatalf("steps = %v", steps)
	}
	for _, step := range steps {
		if !strings.Contains(step, "could not delete") {
			t.Fatalf("step = %q, want a failure report", step)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{-1, "0"}, {0, "0"}, {512, "512B"},
		{1024, "1.0K"}, {10 * 1024, "10K"}, {1536, "1.5K"},
		{5 * 1024 * 1024, "5.0M"}, {2 * 1024 * 1024 * 1024, "2.0G"},
		{3 * 1024 * 1024 * 1024 * 1024, "3.0T"},
		{4096 * 1024 * 1024 * 1024 * 1024, "4096T"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, "never"},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-50 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		if got := humanAge(c.in); got != c.want {
			t.Fatalf("humanAge(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The daemon leg of a purge: a clean /shutdown when the daemon answers, and a
// forced kill when it does not. Both use a pid that is already gone, so
// WaitForProcessExit resolves immediately without a real daemon.
func TestPerformPurgeStopsDaemonThenFallsBackToKill(t *testing.T) {
	home := setupProfileHome(t)
	const deadPID = 0x7FFFFFFF

	hitShutdown := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shutdown" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		hitShutdown = true
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	runtimeDir := filepath.Join(home, "profiles", "adhoc")
	writeRuntimeFile(t, filepath.Join(runtimeDir, "daemon.json"),
		fmt.Sprintf(`{"pid":%d,"host":%q,"port":%d}`, deadPID, host, port))
	view := runtimeView{
		Runtime: borzprofile.Runtime{
			Name: "adhoc", RuntimeDir: runtimeDir, HasRuntimeDir: true, DaemonPID: deadPID,
		},
		DaemonAlive: true,
	}
	steps := performPurge(view, false)
	if !hitShutdown {
		t.Fatal("purge did not ask the daemon to shut down")
	}
	if len(steps) != 2 || !strings.Contains(steps[0], "stopped daemon") {
		t.Fatalf("steps = %v", steps)
	}

	// Same profile, but daemon.json is gone: there is no address to shut down
	// politely, so the purge must not stall on it.
	writeRuntimeFile(t, filepath.Join(runtimeDir, "keep"), "x")
	steps = performPurge(view, false)
	if len(steps) != 2 || !strings.Contains(steps[0], "killed daemon") {
		t.Fatalf("fallback steps = %v", steps)
	}
}
