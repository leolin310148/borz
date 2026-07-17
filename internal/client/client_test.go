package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/profile"
	"github.com/leolin310148/borz/internal/protocol"
)

// resetState zeros package-level globals so each test is independent.
func resetState() {
	cachedInfo = nil
	daemonReady = false
	legacyRemoteFlag = false
	localVersion = ""
	clientSurface = "cli"
	clientSessionID = ""
	versionWarningShown = false
	cachedProfile = ""
	resolvedTarget = nil
	resolvedTargetProfile = ""
	_ = config.SetProfile("")
}

// writeRemoteProfile declares a remote-transport profile in profiles.json.
func writeRemoteProfile(t *testing.T, home, name, url, token string) {
	t.Helper()
	entry := map[string]any{"transport": "remote", "url": url}
	if token != "" {
		entry["token"] = token
	}
	data, err := json.Marshal(map[string]any{"version": 1, "profiles": map[string]any{name: entry}})
	if err != nil {
		t.Fatalf("marshal profiles.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), data, 0o600); err != nil {
		t.Fatalf("write profiles.json: %v", err)
	}
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

// freePort reserves and releases a local TCP port, so connecting to it fails.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local TCP port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

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

func TestReadDaemonJSON_RejectsInvalidPIDAndPort(t *testing.T) {
	for _, body := range []string{
		`{"pid":-1,"host":"127.0.0.1","port":19824}`,
		`{"pid":123,"host":"127.0.0.1","port":-1}`,
		`{"pid":123,"host":"127.0.0.1","port":65536}`,
	} {
		home := t.TempDir()
		t.Setenv("BORZ_HOME", home)
		if err := os.WriteFile(filepath.Join(home, "daemon.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadDaemonJSON(); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("ReadDaemonJSON(%s) error = %v, want invalid", body, err)
		}
	}
}

func TestRemoteConfigReadWriteAndToggle(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	if err := WriteRemoteConfig(&RemoteConfig{URL: "127.0.0.1:19824/", Token: "secret"}); err != nil {
		t.Fatalf("WriteRemoteConfig: %v", err)
	}
	cfg, err := ReadRemoteConfig()
	if err != nil {
		t.Fatalf("ReadRemoteConfig: %v", err)
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

	// The default profile ignores the legacy client.json for routing.
	cfg, enabled, err := EnabledRemoteConfig()
	if err != nil {
		t.Fatalf("EnabledRemoteConfig: %v", err)
	}
	if enabled || cfg != nil {
		t.Fatalf("default profile should stay local, enabled=%v cfg=%+v", enabled, cfg)
	}

	// Selecting the migrated 'remote' profile (what bare --remote maps to)
	// routes to the server declared by the legacy config.
	if err := config.SetProfile("remote"); err != nil {
		t.Fatal(err)
	}
	SetLegacyRemoteFlag(true)
	cfg, enabled, err = EnabledRemoteConfig()
	if err != nil {
		t.Fatalf("EnabledRemoteConfig for migrated remote profile: %v", err)
	}
	if !enabled || cfg.URL != "http://127.0.0.1:19824" || cfg.Token != "secret" {
		t.Fatalf("enabled=%v cfg=%+v", enabled, cfg)
	}
}

func TestRemoteConfigValidation(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	cases := []struct {
		raw  string
		want string
	}{
		{"", "required"},
		{"ftp://example.test", "must use http"},
		{"http:///missing-host", "include a host"},
		{"http://:19824", "include a host"},
		{"http://user:pass@example.test", "user info"},
		{"http://example.test:0", "TCP port"},
		{"http://example.test:65536", "TCP port"},
		{"http://[::1]:", "TCP port"},
	}
	for _, tc := range cases {
		if err := WriteRemoteConfig(&RemoteConfig{URL: tc.raw}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("WriteRemoteConfig(%q) error = %v, want substring %q", tc.raw, err, tc.want)
		}
	}
}

func TestRemoteRoutingEnabledReflectsProfileTransport(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	if RemoteRoutingEnabled() {
		t.Fatal("remote routing should default to disabled")
	}
	writeRemoteProfile(t, home, "mini", "http://10.0.0.1:13333", "tok")
	if RemoteRoutingEnabled() {
		t.Fatal("default profile must not pick up another profile's remote transport")
	}
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}
	if !RemoteRoutingEnabled() {
		t.Fatal("remote-transport profile should enable remote routing")
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
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(`{"url":"http://:19824"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRemoteConfig(); err == nil || !strings.Contains(err.Error(), "include a host") {
		t.Fatalf("missing-host err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(`{"url":"http://user:pass@example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRemoteConfig(); err == nil || !strings.Contains(err.Error(), "user info") {
		t.Fatalf("userinfo-url err = %v", err)
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

func TestRemoteConfigTrimsToken(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())

	if err := WriteRemoteConfig(&RemoteConfig{URL: "127.0.0.1:19824", Token: "\tstored\n"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadRemoteConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "stored" {
		t.Fatalf("ReadRemoteConfig token = %q, want stored", cfg.Token)
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

// --- WaitForProcessExit ---

func TestWaitForProcessExit_AlreadyDead(t *testing.T) {
	if IsProcessAlive(999999) {
		t.Skip("PID 999999 happened to exist")
	}
	if !WaitForProcessExit(999999, time.Second) {
		t.Error("expected true for already-dead pid")
	}
}

func TestWaitForProcessExit_AliveTimesOut(t *testing.T) {
	start := time.Now()
	if WaitForProcessExit(os.Getpid(), 150*time.Millisecond) {
		t.Error("expected false for self pid")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("returned too quickly (%v); should poll until deadline", elapsed)
	}
}

// --- KillDaemon ---

func TestKillDaemon_KillsLiveProcessAndRemovesDaemonJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX sleep(1)")
	}
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	reaped := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(reaped)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-reaped:
		case <-time.After(2 * time.Second):
		}
	})

	// Plant a stale daemon.json so we can confirm KillDaemon cleans it up.
	info := protocol.DaemonInfo{PID: pid, Host: "127.0.0.1", Port: 1}
	raw, _ := json.Marshal(info)
	dpath := filepath.Join(home, "daemon.json")
	if err := os.WriteFile(dpath, raw, 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
	cachedInfo = &info
	daemonReady = true

	if err := KillDaemon(pid); err != nil {
		t.Fatalf("KillDaemon: %v", err)
	}

	select {
	case <-reaped:
	case <-time.After(3 * time.Second):
		t.Fatalf("sleep pid %d not reaped after KillDaemon", pid)
	}
	if IsProcessAlive(pid) {
		t.Errorf("pid %d still alive after KillDaemon", pid)
	}
	if _, err := os.Stat(dpath); err == nil {
		t.Errorf("daemon.json not removed after KillDaemon")
	}
	if cachedInfo != nil || daemonReady {
		t.Errorf("cachedInfo/daemonReady not reset after KillDaemon")
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

func TestHttpJSONHTTPErrorFormatsBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "trimmed body", statusCode: http.StatusUnauthorized, body: "\n denied \t", want: "borz HTTP 401: denied"},
		{name: "json error", statusCode: http.StatusUnauthorized, body: `{"error":"Unauthorized"}`, want: "borz HTTP 401: Unauthorized"},
		{name: "json message", statusCode: http.StatusBadGateway, body: `{"message":"upstream failed"}`, want: "borz HTTP 502: upstream failed"},
		{name: "empty body", statusCode: http.StatusNotFound, body: " \n ", want: "borz HTTP 404: Not Found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			info := infoForServer(t, ts, "")

			_, err := httpJSON("GET", "/x", info, nil, time.Second)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("httpJSON error = %v, want %q", err, tc.want)
			}
		})
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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	// Bare --remote maps to the 'remote' profile plus the legacy flag; with
	// nothing configured that must be a hard error, not a managed fallthrough.
	if err := config.SetProfile("remote"); err != nil {
		t.Fatal(err)
	}
	SetLegacyRemoteFlag(true)

	if _, enabled, err := EnabledRemoteConfig(); err == nil || enabled || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected missing config error with --remote, enabled=%v err=%v", enabled, err)
	}
}

func TestEnabledRemoteConfigReportsBrokenProfile(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(`{"version":1,"profiles":{"mini":{"transport":"remote"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnabledRemoteConfig(); err == nil || !strings.Contains(err.Error(), `profile "mini"`) {
		t.Fatalf("broken profile error = %v", err)
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

	// Legacy path: only client.json exists; selecting the 'remote' profile
	// (bare --remote) migrates it and routes the command to the server.
	if err := WriteRemoteConfig(&RemoteConfig{URL: ts.URL, Token: "secret", Enabled: false}); err != nil {
		t.Fatalf("write remote config: %v", err)
	}
	if err := config.SetProfile("remote"); err != nil {
		t.Fatal(err)
	}
	SetLegacyRemoteFlag(true)
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

	writeRemoteProfile(t, home, "mini", ts.URL, "secret")
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("BORZ_HOME", t.TempDir())
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
	writeRemoteProfile(t, home, "mini", remote.URL, "remote-token")
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	t.Setenv("BORZ_HOME", t.TempDir())

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
	if ep.OwnedByBorz {
		t.Fatal("BORZ_CDP_URL endpoint must not be marked as borz-owned")
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
	if !ep.OwnedByBorz {
		t.Fatal("managed port-file endpoint should be marked as borz-owned")
	}
}

func TestDiscoverCDPPort_IgnoresOutOfRangeManagedPortFile(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_CDP_URL", "")
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(config.ManagedBrowserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ManagedPortFile(), []byte("70000"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldCanConnect := canConnect
	oldFinder := browserExecutableFinder
	var attemptedPorts []int
	canConnect = func(host string, port int) bool {
		attemptedPorts = append(attemptedPorts, port)
		return false
	}
	browserExecutableFinder = func() string { return "" }
	t.Cleanup(func() {
		canConnect = oldCanConnect
		browserExecutableFinder = oldFinder
	})

	if _, err := DiscoverCDPPort(); err == nil {
		t.Fatal("DiscoverCDPPort succeeded with only an invalid managed port")
	}
	for _, port := range attemptedPorts {
		if port == 70000 {
			t.Fatalf("attempted to connect to invalid managed port: %v", attemptedPorts)
		}
	}
}

func TestDiscoverCDPPort_NamedProfileUsesProfileState(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_CDP_URL", "")
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}

	oldFinder := browserExecutableFinder
	oldCanConnect := canConnect
	oldCommand := execCommand
	browserExecutableFinder = func() string { return "/bin/echo" }
	canConnect = func(host string, port int) bool {
		return host == "127.0.0.1" && port != config.DefaultCDPPort
	}
	var launchedArgs []string
	execCommand = func(_ string, args ...string) *exec.Cmd {
		launchedArgs = append([]string(nil), args...)
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	t.Cleanup(func() {
		browserExecutableFinder = oldFinder
		canConnect = oldCanConnect
		execCommand = oldCommand
	})

	ep, err := DiscoverCDPPort()
	if err != nil {
		t.Fatalf("DiscoverCDPPort: %v", err)
	}
	if ep.Port == config.DefaultCDPPort {
		t.Fatalf("named profile should not use default CDP port: %+v", ep)
	}
	if !ep.OwnedByBorz {
		t.Fatal("newly launched managed endpoint should be marked as borz-owned")
	}
	wantUserDataArg := "--user-data-dir=" + filepath.Join(home, "profiles", "work", "browser", "user-data")
	if !containsArg(launchedArgs, wantUserDataArg) {
		t.Fatalf("launch args missing %q: %v", wantUserDataArg, launchedArgs)
	}
	if data, err := os.ReadFile(filepath.Join(home, "profiles", "work", "browser", "cdp-port")); err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(ep.Port) {
		t.Fatalf("profile port file data=%q err=%v", data, err)
	}
}

func TestCheckCDPEndpoint(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"Browser":"Chrome"}`))
	}))
	defer ts.Close()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckCDPEndpoint(host, toInt(portStr), 0); err != nil {
		t.Fatalf("CheckCDPEndpoint success: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	_, badPortStr, _ := net.SplitHostPort(strings.TrimPrefix(bad.URL, "http://"))
	if err := CheckCDPEndpoint(host, toInt(badPortStr), time.Second); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("CheckCDPEndpoint non-200 err = %v", err)
	}

	dead := freePort(t)
	if err := CheckCDPEndpoint("127.0.0.1", dead, time.Second); err == nil || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("CheckCDPEndpoint dead err = %v", err)
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

func TestEnsureDaemon_RecoversManagedBrowserForRunningDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)

	const cdpPort = 34567
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"running":true,"cdpConnected":false,"cdpHost":"127.0.0.1","cdpPort":%d,"version":"test"}`, cdpPort)
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(config.ManagedBrowserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ManagedPortFile(), []byte(strconv.Itoa(cdpPort)), 0o600); err != nil {
		t.Fatal(err)
	}
	info := infoForServer(t, ts, "")
	data, _ := json.Marshal(info)
	if err := os.WriteFile(config.DaemonJSONPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}

	oldCanConnect := canConnect
	oldLaunch := launchManagedBrowserAtPort
	canConnect = func(string, int) bool { return false }
	launchedPort := 0
	launchManagedBrowserAtPort = func(port int) (*CDPEndpoint, error) {
		launchedPort = port
		return &CDPEndpoint{Host: "127.0.0.1", Port: port}, nil
	}
	t.Cleanup(func() {
		canConnect = oldCanConnect
		launchManagedBrowserAtPort = oldLaunch
	})

	SetLocalVersion("test")
	if err := EnsureDaemon(); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
	if launchedPort != cdpPort {
		t.Fatalf("managed browser launched on port %d, want %d", launchedPort, cdpPort)
	}
	if cachedInfo == nil || !daemonReady {
		t.Fatalf("daemon should remain adopted: cachedInfo=%+v ready=%v", cachedInfo, daemonReady)
	}
}

func TestRecoverDisconnectedDaemonSkipsUnmanagedAndMismatchedEndpoints(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())

	oldCanConnect := canConnect
	oldLaunch := launchManagedBrowserAtPort
	canConnect = func(string, int) bool { return false }
	launches := 0
	launchManagedBrowserAtPort = func(port int) (*CDPEndpoint, error) {
		launches++
		return &CDPEndpoint{Host: "127.0.0.1", Port: port}, nil
	}
	t.Cleanup(func() {
		canConnect = oldCanConnect
		launchManagedBrowserAtPort = oldLaunch
	})

	SetLocalVersion("new")
	statuses := []protocol.DaemonStatus{
		{Running: true, CDPHost: "chrome.internal", CDPPort: config.DefaultCDPPort, Version: "new"},
		{Running: true, CDPHost: "127.0.0.1", CDPPort: 33333, Version: "new"},
		{Running: true, CDPHost: "127.0.0.1", CDPPort: config.DefaultCDPPort, Version: "new"},
		{Running: true, CDPHost: "127.0.0.1", CDPPort: config.DefaultCDPPort, Version: "old"},
		{Running: true, CDPConnected: true, CDPHost: "127.0.0.1", CDPPort: config.DefaultCDPPort, Version: "new"},
	}
	for _, status := range statuses {
		if err := recoverDisconnectedDaemon(status); err != nil {
			t.Fatalf("recoverDisconnectedDaemon(%+v): %v", status, err)
		}
	}
	if launches != 0 {
		t.Fatalf("unexpected managed browser launches: %d", launches)
	}
}

func TestRecoverDisconnectedDaemonReportsLaunchFailure(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	if err := os.MkdirAll(config.ManagedBrowserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ManagedPortFile(), []byte(strconv.Itoa(config.DefaultCDPPort)), 0o600); err != nil {
		t.Fatal(err)
	}

	oldCanConnect := canConnect
	oldLaunch := launchManagedBrowserAtPort
	canConnect = func(string, int) bool { return false }
	launchManagedBrowserAtPort = func(int) (*CDPEndpoint, error) {
		return nil, errors.New("launch failed")
	}
	t.Cleanup(func() {
		canConnect = oldCanConnect
		launchManagedBrowserAtPort = oldLaunch
	})

	err := recoverDisconnectedDaemon(protocol.DaemonStatus{
		Running: true, CDPHost: "127.0.0.1", CDPPort: config.DefaultCDPPort,
	})
	if err == nil || !strings.Contains(err.Error(), "managed browser recovery failed") {
		t.Fatalf("recovery error = %v", err)
	}
}

func TestEnsureDaemon_NamedProfileSpawnsProfileDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}

	statusRunning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":true}`))
	}))
	defer statusRunning.Close()
	runningInfo := infoForServer(t, statusRunning, "")

	oldDiscover := discoverCDPPort
	oldExecutable := osExecutable
	oldCommand := execCommand
	discoverCDPPort = func() (*CDPEndpoint, error) {
		return &CDPEndpoint{Host: "127.0.0.1", Port: 33333, OwnedByBorz: true}, nil
	}
	osExecutable = func() (string, error) { return "/bin/echo", nil }
	var daemonArgs []string
	execCommand = func(_ string, args ...string) *exec.Cmd {
		daemonArgs = append([]string(nil), args...)
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	t.Cleanup(func() {
		discoverCDPPort = oldDiscover
		osExecutable = oldExecutable
		execCommand = oldCommand
	})

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.MkdirAll(filepath.Dir(config.DaemonJSONPath()), 0o755)
		data, _ := json.Marshal(runningInfo)
		_ = os.WriteFile(config.DaemonJSONPath(), data, 0o600)
	}()

	if err := EnsureDaemon(); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
	for _, want := range []string{"daemon", "--profile", "work", "--cdp-port", "33333"} {
		if !containsArg(daemonArgs, want) {
			t.Fatalf("daemon args missing %q: %v", want, daemonArgs)
		}
	}
	if !containsArg(daemonArgs, "--port") {
		t.Fatalf("daemon args should include an auto-selected --port: %v", daemonArgs)
	}
	if !containsArg(daemonArgs, "--close-owned-browser") {
		t.Fatalf("owned managed endpoint should close with its daemon: %v", daemonArgs)
	}
	if cachedInfo == nil || !daemonReady || cachedProfile != "work" {
		t.Fatalf("named profile daemon state not cached: info=%+v ready=%v profile=%q", cachedInfo, daemonReady, cachedProfile)
	}
}

// writeCDPProfile declares a cdp-transport profile in profiles.json.
func writeCDPProfile(t *testing.T, home, name, host string, port int) {
	t.Helper()
	content := fmt.Sprintf(`{"version":1,"profiles":{%q:{"transport":"cdp","cdpHost":%q,"cdpPort":%d}}}`, name, host, port)
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write profiles.json: %v", err)
	}
}

func TestEnsureDaemon_CDPProfileUnreachableFailsWithoutLaunching(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeCDPProfile(t, home, "mdt", "127.0.0.1", 19845)
	if err := config.SetProfile("mdt"); err != nil {
		t.Fatal(err)
	}

	oldCanConnect := canConnect
	oldLaunch := launchManagedBrowserAtPort
	canConnect = func(string, int) bool { return false }
	launched := false
	launchManagedBrowserAtPort = func(port int) (*CDPEndpoint, error) {
		launched = true
		return nil, fmt.Errorf("must not be called")
	}
	stubDiscover(t, func() (*CDPEndpoint, error) {
		t.Error("cdp profile must bypass CDP discovery")
		return nil, errFakeNoCDP
	})
	t.Cleanup(func() {
		canConnect = oldCanConnect
		launchManagedBrowserAtPort = oldLaunch
	})

	err := EnsureDaemon()
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:19845") || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("EnsureDaemon error = %v, want loud unreachable-endpoint error", err)
	}
	if launched {
		t.Fatal("dead cdp endpoint must never fall back to launching a managed browser")
	}
	if _, statErr := os.Stat(filepath.Join(home, "profiles", "mdt", "browser")); !os.IsNotExist(statErr) {
		t.Fatalf("cdp profile grew a managed browser dir: %v", statErr)
	}
}

func TestEnsureDaemon_CDPProfileSpawnsDaemonAtDeclaredEndpoint(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeCDPProfile(t, home, "mdt", "127.0.0.1", 19845)
	if err := config.SetProfile("mdt"); err != nil {
		t.Fatal(err)
	}

	statusRunning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"running":true}`))
	}))
	defer statusRunning.Close()
	runningInfo := infoForServer(t, statusRunning, "")

	oldCanConnect := canConnect
	oldExecutable := osExecutable
	oldCommand := execCommand
	canConnect = func(host string, port int) bool { return host == "127.0.0.1" && port == 19845 }
	osExecutable = func() (string, error) { return "/bin/echo", nil }
	var daemonArgs []string
	execCommand = func(_ string, args ...string) *exec.Cmd {
		daemonArgs = append([]string(nil), args...)
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	stubDiscover(t, func() (*CDPEndpoint, error) {
		t.Error("cdp profile must bypass CDP discovery")
		return nil, errFakeNoCDP
	})
	t.Cleanup(func() {
		canConnect = oldCanConnect
		osExecutable = oldExecutable
		execCommand = oldCommand
	})

	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.MkdirAll(filepath.Dir(config.DaemonJSONPath()), 0o755)
		data, _ := json.Marshal(runningInfo)
		_ = os.WriteFile(config.DaemonJSONPath(), data, 0o600)
	}()

	if err := EnsureDaemon(); err != nil {
		t.Fatalf("EnsureDaemon: %v", err)
	}
	for _, want := range []string{"daemon", "--cdp-host", "127.0.0.1", "--cdp-port", "19845", "--profile", "mdt"} {
		if !containsArg(daemonArgs, want) {
			t.Fatalf("daemon args missing %q: %v", want, daemonArgs)
		}
	}
	if containsArg(daemonArgs, "--close-owned-browser") {
		t.Fatalf("external cdp profile must not close its browser: %v", daemonArgs)
	}
}

func TestEnsureDaemon_CDPProfileCarriesIdleTabTimeoutToSpawn(t *testing.T) {
	// spawnArgs runs one EnsureDaemon auto-spawn against a cdp profile whose
	// entry declares idleTabTimeout=0 and returns the daemon argv.
	spawnArgs := func(t *testing.T) []string {
		t.Helper()
		resetState()
		t.Cleanup(resetState)
		home := t.TempDir()
		t.Setenv("BORZ_HOME", home)
		content := `{"version":1,"profiles":{"mdt":{"transport":"cdp","cdpHost":"127.0.0.1","cdpPort":19845,"idleTabTimeout":0,"maxTabs":12}}}`
		if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := config.SetProfile("mdt"); err != nil {
			t.Fatal(err)
		}

		statusRunning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"running":true}`))
		}))
		t.Cleanup(statusRunning.Close)
		runningInfo := infoForServer(t, statusRunning, "")

		oldCanConnect := canConnect
		oldExecutable := osExecutable
		oldCommand := execCommand
		canConnect = func(host string, port int) bool { return true }
		osExecutable = func() (string, error) { return "/bin/echo", nil }
		var daemonArgs []string
		execCommand = func(_ string, args ...string) *exec.Cmd {
			daemonArgs = append([]string(nil), args...)
			return exec.Command("/bin/sh", "-c", "exit 0")
		}
		t.Cleanup(func() {
			canConnect = oldCanConnect
			osExecutable = oldExecutable
			execCommand = oldCommand
		})

		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = os.MkdirAll(filepath.Dir(config.DaemonJSONPath()), 0o755)
			data, _ := json.Marshal(runningInfo)
			_ = os.WriteFile(config.DaemonJSONPath(), data, 0o600)
		}()

		if err := EnsureDaemon(); err != nil {
			t.Fatalf("EnsureDaemon: %v", err)
		}
		return daemonArgs
	}

	flagValue := func(args []string, name string) (string, bool) {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1], true
			}
		}
		return "", false
	}

	t.Run("profile value rides along", func(t *testing.T) {
		t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
		t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")
		t.Setenv("BORZ_MAX_TABS", "")
		t.Setenv("BB_BROWSER_MAX_TABS", "")
		args := spawnArgs(t)
		if v, ok := flagValue(args, "--idle-tab-timeout"); !ok || v != "0" {
			t.Fatalf("daemon args should carry --idle-tab-timeout 0, got %v", args)
		}
		if v, ok := flagValue(args, "--max-tabs"); !ok || v != "12" {
			t.Fatalf("daemon args should carry --max-tabs 12, got %v", args)
		}
	})

	t.Run("env set: flag omitted so env keeps outranking the profile", func(t *testing.T) {
		t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "5")
		t.Setenv("BORZ_MAX_TABS", "")
		args := spawnArgs(t)
		if _, ok := flagValue(args, "--idle-tab-timeout"); ok {
			t.Fatalf("daemon args must omit --idle-tab-timeout when the env var is set, got %v", args)
		}
	})

	t.Run("max-tabs env set: flag omitted", func(t *testing.T) {
		t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
		t.Setenv("BORZ_MAX_TABS", "5")
		args := spawnArgs(t)
		if _, ok := flagValue(args, "--max-tabs"); ok {
			t.Fatalf("daemon args must omit --max-tabs when env is set, got %v", args)
		}
	})
}

func TestEnsureDaemon_RemoteProfileHasNoLocalDaemon(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeRemoteProfile(t, home, "mini", "http://10.0.0.1:13333", "tok")
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}

	err := EnsureDaemon()
	if err == nil || !strings.Contains(err.Error(), "remote profile") || !strings.Contains(err.Error(), "http://10.0.0.1:13333") {
		t.Fatalf("EnsureDaemon remote error = %v", err)
	}
}

func TestActiveTargetMemoizesPerProfile(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeRemoteProfile(t, home, "mini", "http://10.0.0.1:13333", "tok")

	target, err := ActiveTarget()
	if err != nil || target.Kind != profile.TransportManaged {
		t.Fatalf("default target = %+v err=%v", target, err)
	}
	// Break the file on disk: the memoized default profile must survive, and
	// switching profiles must re-read (and fail loudly).
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if target, err = ActiveTarget(); err != nil || target.Kind != profile.TransportManaged {
		t.Fatalf("memoized target = %+v err=%v", target, err)
	}
	if err := config.SetProfile("mini"); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveTarget(); err == nil {
		t.Fatal("profile switch should re-resolve and surface the broken registry")
	}
}

func TestEnsureDaemon_VersionMismatchWarnsButAdopts(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	SetLocalVersion("2.0.0")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"running":true,"version":"1.0.0"}`))
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	info := infoForServer(t, ts, "")
	data, _ := json.Marshal(info)
	os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600)

	errOut := captureClientStderr(t, func() {
		if err := EnsureDaemon(); err != nil {
			t.Fatalf("EnsureDaemon: %v", err)
		}
	})
	if !strings.Contains(errOut, "daemon is version 1.0.0") || !strings.Contains(errOut, "CLI is version 2.0.0") {
		t.Fatalf("stderr warning = %q", errOut)
	}
	if cachedInfo == nil || !daemonReady {
		t.Fatalf("daemon should be adopted despite mismatch: cachedInfo=%+v ready=%v", cachedInfo, daemonReady)
	}

	errOut = captureClientStderr(t, func() {
		if err := EnsureDaemon(); err != nil {
			t.Fatalf("EnsureDaemon cached: %v", err)
		}
	})
	if errOut != "" {
		t.Fatalf("warning should be emitted once, got %q", errOut)
	}
}

func captureClientStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	return <-done
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
	t.Setenv("BORZ_HOME", t.TempDir())

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
