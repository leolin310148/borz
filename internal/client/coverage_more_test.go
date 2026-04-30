package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestRemoteConfigAndHTTPErrorBranches(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	if err := WriteRemoteConfig(&RemoteConfig{URL: "http://example.test", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewRemoteConfig("https://other.test/base/?q=1#frag", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.URL != "https://other.test/base" {
		t.Fatalf("NewRemoteConfig preserved/normalized = %+v", cfg)
	}
	if _, err := ConfigureRemote("http://127.0.0.1:1", "tok"); err != nil {
		t.Fatalf("ConfigureRemote: %v", err)
	}
	if _, err := SetRemoteEnabled(true); err != nil {
		t.Fatalf("SetRemoteEnabled true: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "client.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := SetRemoteEnabled(false); err == nil {
		t.Fatal("SetRemoteEnabled should fail without config")
	}

	if _, err := httpJSONEndpoint("POST", "://bad-url", "", "/x", nil, time.Second); err == nil {
		t.Fatal("httpJSONEndpoint should reject invalid request URLs")
	}
	if _, err := httpJSONEndpoint("POST", "http://example.test", "", "/x", make(chan int), time.Second); err == nil {
		t.Fatal("httpJSONEndpoint should report JSON marshal errors")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
	}))
	defer ts.Close()
	if _, err := httpJSONEndpoint("GET", ts.URL, "", "/", nil, time.Second); err == nil {
		t.Fatal("httpJSONEndpoint should report response body read errors")
	}
}

func TestEnsureDaemonCacheDaemonJSONAndSpawnBranches(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	statusRunning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":true}`))
	}))
	defer statusRunning.Close()
	runningInfo := infoForServer(t, statusRunning, "")

	cachedInfo = runningInfo
	daemonReady = true
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("cached running EnsureDaemon: %v", err)
	}

	statusStopped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":false}`))
	}))
	defer statusStopped.Close()
	cachedInfo = infoForServer(t, statusStopped, "")
	daemonReady = true
	stubDiscover(t, func() (*CDPEndpoint, error) { return nil, errFakeNoCDP })
	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "Cannot find") {
		t.Fatalf("stale cached EnsureDaemon error = %v", err)
	}
	if daemonReady || cachedInfo != nil {
		t.Fatalf("stale cache not cleared: ready=%v info=%+v", daemonReady, cachedInfo)
	}

	data, _ := json.Marshal(protocol.DaemonInfo{PID: 999999, Host: "127.0.0.1", Port: 1})
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "Cannot find") {
		t.Fatalf("dead daemon json EnsureDaemon error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("dead daemon json should be removed, stat err=%v", err)
	}

	oldDiscover := discoverCDPPort
	oldExecutable := osExecutable
	oldCommand := execCommand
	discoverCDPPort = func() (*CDPEndpoint, error) { return &CDPEndpoint{Host: "127.0.0.1", Port: 9222}, nil }
	osExecutable = func() (string, error) { return "/bin/echo", nil }
	execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	t.Cleanup(func() {
		discoverCDPPort = oldDiscover
		osExecutable = oldExecutable
		execCommand = oldCommand
	})
	go func() {
		time.Sleep(100 * time.Millisecond)
		data, _ := json.Marshal(runningInfo)
		_ = os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)
	}()
	resetState()
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("spawned EnsureDaemon: %v", err)
	}
	if cachedInfo == nil || !daemonReady {
		t.Fatalf("spawned daemon state not cached: ready=%v info=%+v", daemonReady, cachedInfo)
	}

	osExecutable = func() (string, error) { return "", os.ErrNotExist }
	resetState()
	if err := os.Remove(filepath.Join(home, "daemon.json")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "cannot find self") {
		t.Fatalf("osExecutable error = %v", err)
	}
}

func TestLocalJSONFallbackAndLaunchManagedBrowserBranches(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/v1/get":
			w.Write([]byte(`{"ok":true}`))
		case "/v1/post":
			w.Write([]byte(`{"posted":true}`))
		case "/command":
			w.Write([]byte(`not-json`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	info := infoForServer(t, ts, "")
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if raw, err := GetJSON("/v1/get", time.Second); err != nil || !strings.Contains(string(raw), "ok") {
		t.Fatalf("GetJSON fallback raw=%s err=%v", raw, err)
	}
	resetState()
	if raw, err := PostJSON("/v1/post", map[string]string{"x": "y"}, time.Second); err != nil || !strings.Contains(string(raw), "posted") {
		t.Fatalf("PostJSON fallback raw=%s err=%v", raw, err)
	}
	resetState()
	if _, err := SendCommand(&protocol.Request{ID: "bad"}); err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("SendCommand invalid local response = %v", err)
	}

	oldFinder := browserExecutableFinder
	oldCanConnect := canConnect
	oldCommand := execCommand
	browserExecutableFinder = func() string { return "" }
	if _, err := launchManagedBrowser(33333); err == nil || !strings.Contains(err.Error(), "no browser") {
		t.Fatalf("no browser err = %v", err)
	}
	browserExecutableFinder = func() string { return "/bin/echo" }
	canConnect = func(host string, port int) bool { return true }
	execCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	t.Cleanup(func() {
		browserExecutableFinder = oldFinder
		canConnect = oldCanConnect
		execCommand = oldCommand
	})
	ep, err := launchManagedBrowser(33334)
	if err != nil {
		t.Fatalf("launchManagedBrowser fake: %v", err)
	}
	if ep.Host != "127.0.0.1" || ep.Port != 33334 {
		t.Fatalf("endpoint = %+v", ep)
	}
	if data, err := os.ReadFile(filepath.Join(home, "browser", "cdp-port")); err != nil || strings.TrimSpace(string(data)) != "33334" {
		t.Fatalf("managed port data=%q err=%v", data, err)
	}
}
