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

	"github.com/leolin310148/borz/internal/protocol"
)

// resetState zeros package-level globals so each test is independent.
func resetState() {
	cachedInfo = nil
	daemonReady = false
	useRemote = false
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
	t.Setenv("BORZ_HOME", home)
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
	t.Setenv("BORZ_HOME", t.TempDir())
	if _, err := ReadDaemonJSON(); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadDaemonJSON_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	os.WriteFile(filepath.Join(home, "daemon.json"), []byte("not json"), 0o644)
	if _, err := ReadDaemonJSON(); err == nil {
		t.Error("expected json parse error")
	}
}

func TestReadDaemonJSON_ZeroFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	// PID=0 → treated as invalid
	os.WriteFile(filepath.Join(home, "daemon.json"), []byte(`{"pid":0,"host":"","port":0}`), 0o644)
	if _, err := ReadDaemonJSON(); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("expected invalid error, got %v", err)
	}
}

func TestRemoteConfigReadWriteAndToggle(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	cfg, err := ConfigureRemote("127.0.0.1:19824/", "secret")
	if err != nil {
		t.Fatalf("ConfigureRemote: %v", err)
	}
	if cfg.URL != "http://127.0.0.1:19824" || cfg.Token != "secret" || cfg.Enabled {
		t.Fatalf("configured cfg = %+v", cfg)
	}
	st, err := os.Stat(filepath.Join(home, "client.json"))
	if err != nil {
		t.Fatalf("client.json stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("client.json perms = %o, want 600", st.Mode().Perm())
	}

	cfg, err = SetRemoteEnabled(true)
	if err != nil {
		t.Fatalf("enable remote: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected enabled config")
	}
	cfg, enabled, err := EnabledRemoteConfig()
	if err != nil {
		t.Fatalf("EnabledRemoteConfig: %v", err)
	}
	if enabled || cfg != nil {
		t.Fatalf("remote routing should stay inactive without explicit --remote, enabled=%v cfg=%+v", enabled, cfg)
	}

	SetRemoteRouting(true)
	cfg, enabled, err = EnabledRemoteConfig()
	if err != nil {
		t.Fatalf("EnabledRemoteConfig after SetRemoteRouting: %v", err)
	}
	if !enabled || cfg.URL != "http://127.0.0.1:19824" {
		t.Fatalf("enabled=%v cfg=%+v", enabled, cfg)
	}
}

func TestRemoteConfigValidation(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.test", "http:///missing-host"} {
		if _, err := ConfigureRemote(raw, ""); err == nil {
			t.Fatalf("ConfigureRemote(%q) expected error", raw)
		}
	}
}

func TestRemoteRoutingEnabledReflectsProcessFlag(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	if RemoteRoutingEnabled() {
		t.Fatal("remote routing should default to disabled")
	}
	SetRemoteRouting(true)
	if !RemoteRoutingEnabled() {
		t.Fatal("remote routing flag was not enabled")
	}
}

func TestReadRemoteConfigValidationErrors(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(`{"token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRemoteConfig(); err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Fatalf("missing-url err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(`{"url":"ftp://example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRemoteConfig(); err == nil || !strings.Contains(err.Error(), "must use http") {
		t.Fatalf("bad-url err = %v", err)
	}
}

func TestWriteRemoteConfigValidationErrors(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	if err := WriteRemoteConfig(nil); err == nil || !strings.Contains(err.Error(), "missing remote") {
		t.Fatalf("nil config err = %v", err)
	}
	if err := WriteRemoteConfig(&RemoteConfig{URL: "ftp://example.test"}); err == nil || !strings.Contains(err.Error(), "must use http") {
		t.Fatalf("bad URL err = %v", err)
	}
}

func TestCheckRemoteConfig(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	var sawAuth bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization") == "Bearer secret"
		w.Write([]byte(`{"running":true}`))
	}))
	defer ts.Close()

	if err := CheckRemoteConfig(&RemoteConfig{URL: ts.URL, Token: "secret"}, 0); err != nil {
		t.Fatalf("CheckRemoteConfig success: %v", err)
	}
	if !sawAuth {
		t.Fatal("authorization header was not sent")
	}
	if err := CheckRemoteConfig(nil, time.Second); err == nil || !strings.Contains(err.Error(), "missing remote") {
		t.Fatalf("nil config err = %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	}))
	defer bad.Close()
	if err := CheckRemoteConfig(&RemoteConfig{URL: bad.URL}, time.Second); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("bad status err = %v", err)
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

func TestEnabledRemoteConfigRequiresSetupWhenRemoteRequested(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	SetRemoteRouting(true)

	if _, enabled, err := EnabledRemoteConfig(); err == nil || enabled {
		t.Fatalf("expected missing config error with remote routing enabled, enabled=%v err=%v", enabled, err)
	}
}

func TestSendCommand_RemoteFlag(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	var sawCommand bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/command":
			sawCommand = true
			var req protocol.Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode command: %v", err)
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(protocol.Response{ID: req.ID, Success: true, Data: &protocol.ResponseData{Value: "remote"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	if err := WriteRemoteConfig(&RemoteConfig{URL: ts.URL, Token: "secret", Enabled: false}); err != nil {
		t.Fatalf("write remote config: %v", err)
	}
	SetRemoteRouting(true)
	resp, err := SendCommand(&protocol.Request{ID: "r1", Action: protocol.ActionGet})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !sawCommand || resp.Data == nil || resp.Data.Value != "remote" {
		t.Fatalf("resp=%+v sawCommand=%v", resp, sawCommand)
	}
}

func TestGetJSONAndStatus_RemoteFlag(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/v1/tabs/events":
			w.Write([]byte(`{"events":[],"latest_seq":0}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()

	if err := WriteRemoteConfig(&RemoteConfig{URL: ts.URL, Token: "secret", Enabled: false}); err != nil {
		t.Fatalf("write remote config: %v", err)
	}
	SetRemoteRouting(true)
	status, err := GetDaemonStatus()
	if err != nil {
		t.Fatalf("GetDaemonStatus: %v", err)
	}
	if !strings.Contains(string(status), `"running":true`) {
		t.Fatalf("status = %s", status)
	}
	raw, err := GetJSON("/v1/tabs/events", time.Second)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !strings.Contains(string(raw), `"events"`) {
		t.Fatalf("raw = %s", raw)
	}
}

func TestPostJSON_LocalAndRemote(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	localBodies := 0
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/v1/ext/call":
			localBodies++
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("local content-type = %q", r.Header.Get("Content-Type"))
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["method"] != "local.method" {
				t.Errorf("local body = %+v", body)
			}
			w.Write([]byte(`{"local":true}`))
		default:
			t.Errorf("unexpected local path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer local.Close()
	cachedInfo = infoForServer(t, local, "")
	daemonReady = true
	raw, err := PostJSON("/v1/ext/call", map[string]any{"method": "local.method"}, time.Second)
	if err != nil {
		t.Fatalf("local PostJSON: %v", err)
	}
	if string(raw) != `{"local":true}` || localBodies != 1 {
		t.Fatalf("local raw=%s localBodies=%d", raw, localBodies)
	}

	resetState()
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	remoteBodies := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ext/call" {
			t.Errorf("unexpected remote path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer remote-token" {
			t.Errorf("remote auth = %q", r.Header.Get("Authorization"))
		}
		remoteBodies++
		w.Write([]byte(`{"remote":true}`))
	}))
	defer remote.Close()
	if err := WriteRemoteConfig(&RemoteConfig{URL: remote.URL, Token: "remote-token"}); err != nil {
		t.Fatalf("write remote config: %v", err)
	}
	SetRemoteRouting(true)
	raw, err = PostJSON("/v1/ext/call", map[string]any{"method": "remote.method"}, time.Second)
	if err != nil {
		t.Fatalf("remote PostJSON: %v", err)
	}
	if string(raw) != `{"remote":true}` || remoteBodies != 1 {
		t.Fatalf("remote raw=%s remoteBodies=%d", raw, remoteBodies)
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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	t.Setenv("BORZ_HOME", t.TempDir())
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

	t.Setenv("BORZ_CDP_URL", ts.URL)
	t.Setenv("BORZ_HOME", t.TempDir()) // isolate managed-port fallback

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
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_CDP_URL", "")

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

func TestResetForTestsClearsCachedState(t *testing.T) {
	cachedInfo = &protocol.DaemonInfo{PID: os.Getpid(), Host: "127.0.0.1", Port: 1}
	daemonReady = true
	ResetForTests()
	if cachedInfo != nil || daemonReady {
		t.Fatalf("state not reset: cachedInfo=%+v daemonReady=%v", cachedInfo, daemonReady)
	}
}

func TestEnsureDaemon_UsesExistingDaemonJSON(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"running":true}`))
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	info := infoForServer(t, ts, "")
	data, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)

	if err := EnsureDaemon(); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
	if cachedInfo == nil || !daemonReady {
		t.Fatalf("daemon state not cached: cachedInfo=%+v daemonReady=%v", cachedInfo, daemonReady)
	}
}

func TestEnsureDaemon_ClearsCachedStoppedDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)
	t.Setenv("BORZ_HOME", t.TempDir())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":false}`))
	}))
	defer ts.Close()
	cachedInfo = infoForServer(t, ts, "")
	daemonReady = true

	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "Cannot find") {
		t.Fatalf("expected discovery failure after cache clear, got %v", err)
	}
	if cachedInfo != nil || daemonReady {
		t.Fatalf("stopped cached daemon not cleared: cachedInfo=%+v daemonReady=%v", cachedInfo, daemonReady)
	}
}

func TestEnsureDaemon_RemovesStaleDaemonJSON(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	data, _ := json.Marshal(protocol.DaemonInfo{PID: 999999, Host: "127.0.0.1", Port: 19824})
	path := filepath.Join(home, "daemon.json")
	os.WriteFile(path, data, 0o600)

	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "Cannot find") {
		t.Fatalf("expected discovery failure, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale daemon.json was not removed, stat err=%v", err)
	}
}

func TestEnsureDaemon_ExistingDaemonStatusNotRunning(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	failingDiscover(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"running":false}`))
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	info := infoForServer(t, ts, "")
	data, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)

	if err := EnsureDaemon(); err == nil || !strings.Contains(err.Error(), "Cannot find") {
		t.Fatalf("expected discovery failure, got %v", err)
	}
	if cachedInfo != nil || daemonReady {
		t.Fatalf("daemon should not be marked ready: cachedInfo=%+v ready=%v", cachedInfo, daemonReady)
	}
}

func TestGetDaemonStatus_ReadsDaemonJSONWhenCacheEmpty(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"running":true,"uptime":9}`))
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	data, _ := json.Marshal(infoForServer(t, ts, ""))
	os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)

	raw, err := GetDaemonStatus()
	if err != nil {
		t.Fatalf("GetDaemonStatus: %v", err)
	}
	if !strings.Contains(string(raw), `"uptime":9`) {
		t.Fatalf("status raw = %s", raw)
	}
}

func TestStopDaemon_ReadsDaemonJSONWhenCacheEmpty(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	hitShutdown := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shutdown" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		hitShutdown = true
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	data, _ := json.Marshal(infoForServer(t, ts, ""))
	os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)

	if err := StopDaemon(); err != nil {
		t.Fatalf("StopDaemon: %v", err)
	}
	if !hitShutdown {
		t.Fatal("shutdown endpoint was not called")
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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	t.Setenv("BORZ_HOME", home)
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
	t.Setenv("BORZ_HOME", home)
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
	t.Setenv("BORZ_HOME", t.TempDir())

	_, err := SendCommand(&protocol.Request{ID: "x"})
	if err == nil {
		t.Error("expected SendCommand to fail when no daemon available")
	}
}
