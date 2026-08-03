package profile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findRuntime(t *testing.T, runtimes []Runtime, name string) Runtime {
	t.Helper()
	for _, r := range runtimes {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("profile %q missing from scan (%d entries)", name, len(runtimes))
	return Runtime{}
}

func TestScanRuntimeEmptyHomeStillReportsDefault(t *testing.T) {
	setHome(t)
	runtimes, err := ScanRuntime()
	if err != nil {
		t.Fatalf("ScanRuntime: %v", err)
	}
	if len(runtimes) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(runtimes), runtimes)
	}
	got := runtimes[0]
	if !got.IsDefault() {
		t.Fatalf("first entry = %q, want default", got.Name)
	}
	// ~/.borz always exists once borz has run, so an existing home must not by
	// itself make the default profile look like it holds runtime state.
	if got.HasRuntimeDir || got.HasBrowserDir || got.HasLogsDir {
		t.Fatalf("empty home reported state: %+v", got)
	}
	if got.Declared {
		t.Fatal("default reported as declared with no profiles.json")
	}
}

func TestScanRuntimeUnionsRegistryDirsAndLogs(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"mini":{"transport":"remote","url":"http://10.0.0.1:13333"}}}`)
	mkdirAll(t, filepath.Join(home, "profiles", "adhoc", "browser"))
	mkdirAll(t, filepath.Join(home, "logs", "logsonly"))
	// Dotted entries are borz bookkeeping, never profiles.
	mkdirAll(t, filepath.Join(home, "profiles", ".tmp"))

	runtimes, err := ScanRuntime()
	if err != nil {
		t.Fatalf("ScanRuntime: %v", err)
	}
	var names []string
	for _, r := range runtimes {
		names = append(names, r.Name)
	}
	want := []string{"default", "adhoc", "logsonly", "mini"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("names = %v, want %v (default first, rest sorted)", names, want)
		}
	}

	// A name only ever used as `--profile adhoc` is undeclared but real.
	adhoc := findRuntime(t, runtimes, "adhoc")
	if adhoc.Declared {
		t.Fatal("adhoc reported as declared")
	}
	if !adhoc.HasRuntimeDir || !adhoc.HasBrowserDir || adhoc.HasLogsDir {
		t.Fatalf("adhoc state wrong: %+v", adhoc)
	}
	if mini := findRuntime(t, runtimes, "mini"); !mini.Declared || mini.HasRuntimeDir {
		t.Fatalf("mini state wrong: %+v", mini)
	}
	logsOnly := findRuntime(t, runtimes, "logsonly")
	if logsOnly.HasRuntimeDir || !logsOnly.HasLogsDir {
		t.Fatalf("logsonly state wrong: %+v", logsOnly)
	}
}

func TestScanRuntimeInvalidRegistryIsAnError(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{`)
	if _, err := ScanRuntime(); err == nil {
		t.Fatal("ScanRuntime accepted a malformed profiles.json")
	}
}

func TestRuntimeForReadsStateFiles(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"work":{"transport":"managed"}}}`)
	dir := filepath.Join(home, "profiles", "work")
	writeFile(t, filepath.Join(dir, "browser", "browser.json"), `{"port":19901,"browserId":"BID-1"}`)
	writeFile(t, filepath.Join(dir, "daemon.json"), `{"pid":4242,"port":19902}`)
	writeFile(t, filepath.Join(dir, "browser", "user-data", "blob"), "0123456789")
	writeFile(t, filepath.Join(home, "logs", "work", "daemon.jsonl"), "line\n")

	got, err := RuntimeFor("work")
	if err != nil {
		t.Fatalf("RuntimeFor: %v", err)
	}
	if !got.Declared || got.IsDefault() {
		t.Fatalf("declared/default wrong: %+v", got)
	}
	if got.BrowserPort != 19901 || got.BrowserID != "BID-1" {
		t.Fatalf("browser state = %d/%q", got.BrowserPort, got.BrowserID)
	}
	if got.DaemonPID != 4242 || got.DaemonPort != 19902 {
		t.Fatalf("daemon state = %d/%d", got.DaemonPID, got.DaemonPort)
	}
	// browser.json (34B) + blob (10B); logs are counted separately so the
	// caller can offer to keep them.
	if got.BrowserBytes < 40 || got.LogBytes != 5 {
		t.Fatalf("sizes = browser %d, logs %d", got.BrowserBytes, got.LogBytes)
	}
	if got.LastUsed.IsZero() || time.Since(got.LastUsed) > time.Minute {
		t.Fatalf("LastUsed = %v", got.LastUsed)
	}
}

func TestRuntimeForDefaultUsesHomeButScopesSize(t *testing.T) {
	home := setHome(t)
	writeFile(t, filepath.Join(home, "browser", "browser.json"), `{"port":19825,"browserId":"BID-D"}`)
	// A sibling profile's data must not be attributed to default, whose
	// runtime directory is the borz home itself.
	writeFile(t, filepath.Join(home, "profiles", "other", "browser", "big"), string(make([]byte, 4096)))

	got, err := RuntimeFor("default")
	if err != nil {
		t.Fatalf("RuntimeFor: %v", err)
	}
	if got.RuntimeDir != home {
		t.Fatalf("RuntimeDir = %q, want %q", got.RuntimeDir, home)
	}
	if !got.HasRuntimeDir || !got.HasBrowserDir {
		t.Fatalf("default state wrong: %+v", got)
	}
	if got.BrowserBytes >= 4096 {
		t.Fatalf("BrowserBytes = %d, swept in a sibling profile", got.BrowserBytes)
	}
	if got.BrowserPort != 19825 {
		t.Fatalf("BrowserPort = %d", got.BrowserPort)
	}
	// "" is the other spelling of default and must resolve identically.
	empty, err := RuntimeFor("")
	if err != nil {
		t.Fatalf("RuntimeFor(\"\"): %v", err)
	}
	if empty.Name != got.Name || empty.RuntimeDir != got.RuntimeDir {
		t.Fatalf("RuntimeFor(\"\") = %+v, want same as default", empty)
	}
}

func TestRuntimeForDefaultWithOnlyDaemonJSON(t *testing.T) {
	home := setHome(t)
	writeFile(t, filepath.Join(home, "daemon.json"), `{"pid":1,"port":19824}`)
	got, err := RuntimeFor("default")
	if err != nil {
		t.Fatalf("RuntimeFor: %v", err)
	}
	if !got.HasRuntimeDir || got.HasBrowserDir {
		t.Fatalf("state wrong: %+v", got)
	}
}

func TestRuntimeForInvalidRegistryIsAnError(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `not json`)
	if _, err := RuntimeFor("work"); err == nil {
		t.Fatal("RuntimeFor accepted a malformed profiles.json")
	}
}

func TestReadStateFilesToleratesGarbage(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.json")
	if port, id := readBrowserState(missing); port != 0 || id != "" {
		t.Fatalf("missing browser.json = %d/%q", port, id)
	}
	if pid, port := readDaemonState(missing); pid != 0 || port != 0 {
		t.Fatalf("missing daemon.json = %d/%d", pid, port)
	}
	bad := filepath.Join(dir, "bad.json")
	writeFile(t, bad, "{oops")
	if port, id := readBrowserState(bad); port != 0 || id != "" {
		t.Fatalf("malformed browser.json = %d/%q", port, id)
	}
	if pid, port := readDaemonState(bad); pid != 0 || port != 0 {
		t.Fatalf("malformed daemon.json = %d/%d", pid, port)
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if got, _ := dirSize(filepath.Join(dir, "nope")); got != 0 {
		t.Fatalf("missing dir size = %d", got)
	}
	writeFile(t, filepath.Join(dir, "a"), "12345")
	writeFile(t, filepath.Join(dir, "sub", "b"), "123")
	got, err := dirSize(dir)
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}
	if got != 8 {
		t.Fatalf("dirSize = %d, want 8", got)
	}
}

func TestNewestMTime(t *testing.T) {
	dir := t.TempDir()
	if got := newestMTime(filepath.Join(dir, "absent")); !got.IsZero() {
		t.Fatalf("absent path = %v, want zero", got)
	}
	old := filepath.Join(dir, "old")
	recent := filepath.Join(dir, "recent")
	writeFile(t, old, "x")
	writeFile(t, recent, "x")
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	got := newestMTime(filepath.Join(dir, "absent"), old, recent)
	if got.Sub(past) < time.Hour {
		t.Fatalf("newestMTime = %v, want the recent file", got)
	}
}
