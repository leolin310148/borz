package client

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

// resetState zeros package-level globals so each test is independent.
func resetState() {
	cachedInfo = nil
	daemonReady = false
}

// stubDiscover replaces discoverCDPPort for the test duration.
func stubDiscover(t *testing.T, fn func() (*CDPEndpoint, error)) {
	t.Helper()
	orig := discoverCDPPort
	discoverCDPPort = fn
	t.Cleanup(func() { discoverCDPPort = orig })
}

// failingDiscover stubs CDP discovery to always error quickly.
func failingDiscover(t *testing.T) {
	t.Helper()
	stubDiscover(t, func() (*CDPEndpoint, error) {
		return nil, errFakeNoCDP
	})
}

var errFakeNoCDP = &fakeErr{msg: "no cdp"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// infoForServer extracts a DaemonInfo pointing at the given httptest server.
func infoForServer(t *testing.T, ts *httptest.Server, token string) *protocol.DaemonInfo {
	t.Helper()
	u := ts.URL
	u = strings.TrimPrefix(u, "http://")
	host, portStr, err := net.SplitHostPort(u)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, _ := strconv.Atoi(portStr)
	return &protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port, Token: token}
}

// --- ReadDaemonJSON ---

func TestReadDaemonJSON_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	info := protocol.DaemonInfo{PID: 123, Host: "127.0.0.1", Port: 19824, Token: "tok"}
	b, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(home, "daemon.json"), b, 0o644)

	got, err := ReadDaemonJSON()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if *got != info {
		t.Errorf("got = %+v", got)
	}
}

func TestReadDaemonJSON_MissingFile(t *testing.T) {
	t.Setenv("BB_BROWSER_HOME", t.TempDir())
	if _, err := ReadDaemonJSON(); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadDaemonJSON_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	os.WriteFile(filepath.Join(home, "daemon.json"), []byte("not json"), 0o644)
	if _, err := ReadDaemonJSON(); err == nil {
		t.Error("expected json parse error")
	}
}

func TestReadDaemonJSON_ZeroFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	// PID=0 → treated as invalid
	os.WriteFile(filepath.Join(home, "daemon.json"), []byte(`{"pid":0,"host":"","port":0}`), 0o644)
	if _, err := ReadDaemonJSON(); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid error, got %v", err)
	}
}

// --- IsProcessAlive ---

func TestIsProcessAlive_Self(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Error("self process should be alive")
	}
}

func TestIsProcessAlive_Bogus(t *testing.T) {
	// PID 1 exists but we can't signal it; pick a high PID we don't own.
	// Using negative PID to force FindProcess/Signal to fail.
	// On Unix, FindProcess always succeeds; Signal(0) returns "no such process" for unused PIDs.
	// 999999 is a commonly-unused high PID.
	if IsProcessAlive(999999) {
		t.Skip("PID 999999 happened to exist; can't verify")
	}
}

// --- httpJSON ---

func TestHttpJSON_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("auth header = %q", got)
		}
		if r.Method == "POST" && r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing content-type on POST")
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	info := infoForServer(t, ts, "secret")

	raw, err := httpJSON("POST", "/command", info, map[string]string{"a": "b"}, time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("body = %s", raw)
	}
}

func TestHttpJSON_NoTokenOmitsAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	info := infoForServer(t, ts, "")

	if _, err := httpJSON("GET", "/status", info, nil, time.Second); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestHttpJSON_HTTPErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("boom"))
	}))
	defer ts.Close()
	info := infoForServer(t, ts, "")

	_, err := httpJSON("GET", "/x", info, nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got %v", err)
	}
}

func TestHttpJSON_Unreachable(t *testing.T) {
	info := &protocol.DaemonInfo{Host: "127.0.0.1", Port: 1} // port 1 should refuse
	_, err := httpJSON("GET", "/", info, nil, 200*time.Millisecond)
	if err == nil {
		t.Error("expected network error")
	}
}

// --- SendCommand ---

func TestSendCommand_Success(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/command":
			var req protocol.Request
			json.NewDecoder(r.Body).Decode(&req)
			resp := protocol.Response{ID: req.ID, Success: true, Data: &protocol.ResponseData{URL: "https://e"}}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	cachedInfo = infoForServer(t, ts, "")
	daemonReady = true

	resp, err := SendCommand(&protocol.Request{ID: "1", Action: protocol.ActionSnapshot})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !resp.Success || resp.Data.URL != "https://e" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestSendCommand_InvalidResponse(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/command":
			w.Write([]byte("not json"))
		}
	}))
	defer ts.Close()

	cachedInfo = infoForServer(t, ts, "")
	daemonReady = true

	_, err := SendCommand(&protocol.Request{ID: "1"})
	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Errorf("expected invalid response, got %v", err)
	}
}

// --- StopDaemon ---

func TestStopDaemon_Success(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shutdown" {
			hit = true
			w.Write([]byte(`{}`))
		}
	}))
	defer ts.Close()
	cachedInfo = infoForServer(t, ts, "")

	if err := StopDaemon(); err != nil {
		t.Errorf("err: %v", err)
	}
	if !hit {
		t.Error("shutdown not hit")
	}
	if cachedInfo != nil || daemonReady {
		t.Error("state not reset after StopDaemon")
	}
}

func TestStopDaemon_NoDaemonJSON(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BB_BROWSER_HOME", t.TempDir())

	if err := StopDaemon(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running', got %v", err)
	}
}

// --- GetDaemonStatus ---

func TestGetDaemonStatus_Success(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true,"uptime":42}`))
		}
	}))
	defer ts.Close()
	cachedInfo = infoForServer(t, ts, "")

	raw, err := GetDaemonStatus()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(raw), `"uptime":42`) {
		t.Errorf("body = %s", raw)
	}
}

func TestGetDaemonStatus_NoDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BB_BROWSER_HOME", t.TempDir())

	if _, err := GetDaemonStatus(); err == nil {
		t.Error("expected error")
	}
}

// --- GetJSON ---

func TestGetJSON_PassesPathAndAuth(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	var sawPath, sawAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"x":1}`))
	}))
	defer ts.Close()

	cachedInfo = infoForServer(t, ts, "secret")
	daemonReady = true

	raw, err := GetJSON("/v1/cookies/all", time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(raw) != `{"x":1}` {
		t.Errorf("body = %s", raw)
	}
	if sawPath != "/v1/cookies/all" {
		t.Errorf("path = %q", sawPath)
	}
	if sawAuth != "Bearer secret" {
		t.Errorf("auth = %q", sawAuth)
	}
}

func TestGetJSON_NoDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BB_BROWSER_HOME", t.TempDir())
	failingDiscover(t)

	if _, err := GetJSON("/v1/x", time.Second); err == nil {
		t.Error("expected error when daemon unavailable")
	}
}

// --- canConnect ---

func TestCanConnect_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte("{}"))
		}
	}))
	defer ts.Close()
	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)

	if !canConnect(host, port) {
		t.Error("expected canConnect=true")
	}
}

func TestCanConnect_Refuses(t *testing.T) {
	if canConnect("127.0.0.1", 1) {
		t.Error("port 1 should refuse")
	}
}

func TestCanConnect_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()
	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)

	if canConnect(host, port) {
		t.Error("404 should count as not connectable")
	}
}

// --- DiscoverCDPPort ---

func TestDiscoverCDPPort_EnvVar(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte("{}"))
		}
	}))
	defer ts.Close()

	t.Setenv("BB_BROWSER_CDP_URL", ts.URL)
	t.Setenv("BB_BROWSER_HOME", t.TempDir()) // isolate managed-port fallback

	ep, err := DiscoverCDPPort()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ep == nil {
		t.Fatal("nil endpoint")
	}
}

func TestDiscoverCDPPort_ManagedPortFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte("{}"))
		}
	}))
	defer ts.Close()
	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, _ := net.SplitHostPort(u)

	if host != "127.0.0.1" {
		t.Skipf("test server host = %s, managed discovery requires 127.0.0.1", host)
	}

	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	t.Setenv("BB_BROWSER_CDP_URL", "")

	browserDir := filepath.Join(home, "browser")
	os.MkdirAll(browserDir, 0o755)
	os.WriteFile(filepath.Join(browserDir, "cdp-port"), []byte(portStr), 0o644)

	ep, err := DiscoverCDPPort()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ep.Port != toInt(portStr) {
		t.Errorf("port = %d, want %s", ep.Port, portStr)
	}
}

func toInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// --- findBrowserExecutable ---

func TestFindBrowserExecutable_DoesNotPanic(t *testing.T) {
	// Depends on system; just ensure it returns a string (possibly empty) without panicking.
	_ = findBrowserExecutable()
}

// --- setDetached ---

func TestSetDetached(t *testing.T) {
	// Platform-specific: on Unix it sets Setpgid; on Windows it sets flags. Just verify no panic
	// and that something is set.
	cmd := &exec.Cmd{}
	setDetached(cmd)
	if cmd.SysProcAttr == nil {
		t.Error("SysProcAttr should be set after setDetached")
	}
}

// --- EnsureDaemon cached-but-stale path ---

func TestEnsureDaemon_CachedAndStillRunning(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
		}
	}))
	defer ts.Close()
	cachedInfo = infoForServer(t, ts, "")
	daemonReady = true

	if err := EnsureDaemon(); err != nil {
		t.Errorf("err: %v", err)
	}
}

func TestEnsureDaemon_CachedButStatusFails(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)

	// Point cachedInfo at a port nothing listens on so /status call fails → daemon considered stale.
	cachedInfo = &protocol.DaemonInfo{PID: os.Getpid(), Host: "127.0.0.1", Port: 1}
	daemonReady = true
	t.Setenv("BB_BROWSER_HOME", t.TempDir())

	if err := EnsureDaemon(); err == nil {
		t.Error("expected error — no CDP endpoint")
	}
}

func TestEnsureDaemon_FromDaemonJSON(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
		}
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	info := infoForServer(t, ts, "")
	b, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(home, "daemon.json"), b, 0o644)

	if err := EnsureDaemon(); err != nil {
		t.Errorf("err: %v", err)
	}
	if !daemonReady || cachedInfo == nil {
		t.Error("state not set")
	}
}

func TestEnsureDaemon_StaleDaemonJSON_DeadPID(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)

	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	// Impossible PID so IsProcessAlive returns false.
	info := protocol.DaemonInfo{PID: 999999, Host: "127.0.0.1", Port: 65001}
	b, _ := json.Marshal(info)
	daemonPath := filepath.Join(home, "daemon.json")
	os.WriteFile(daemonPath, b, 0o644)

	_ = EnsureDaemon()
	if _, err := os.Stat(daemonPath); err == nil {
		t.Error("stale daemon.json should have been removed")
	}
}

func TestSendCommand_EnsureDaemonFails(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)
	t.Setenv("BB_BROWSER_HOME", t.TempDir())

	_, err := SendCommand(&protocol.Request{ID: "x"})
	if err == nil {
		t.Error("expected SendCommand to fail when no daemon available")
	}
}
