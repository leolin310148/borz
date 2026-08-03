package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon/extbridge"
	"github.com/leolin310148/borz/internal/observability"
	"github.com/leolin310148/borz/internal/processlock"
	"github.com/leolin310148/borz/internal/protocol"
)

func newTestServer(t *testing.T, token string) *Server {
	t.Helper()
	tabs := NewTabStateManager()
	cdp := NewCdpConnection("127.0.0.1", 9222, tabs)
	return &Server{
		opts:   ServerOptions{Host: "127.0.0.1", Port: 0, Token: token},
		cdp:    cdp,
		extHub: extbridge.NewHub(),
	}
}

func TestSendJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	sendJSON(rec, 201, map[string]string{"hello": "world"})

	if rec.Code != 201 {
		t.Fatalf("status: got %d want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q", ct)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("x-content-type-options: got %q", nosniff)
	}

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["hello"] != "world" {
		t.Fatalf("body: got %+v", got)
	}
}

func TestSendJSON_MarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	sendJSON(rec, 201, map[string]any{"bad": make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusInternalServerError)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: got %q", ct)
	}
	if got := rec.Body.String(); got != `{"error":"failed to encode JSON response"}` {
		t.Fatalf("body: got %q", got)
	}
}

func TestIsAddrInUse(t *testing.T) {
	if isAddrInUse(nil) {
		t.Fatal("nil err should be false")
	}
	if !isAddrInUse(errors.New("listen tcp: bind: address already in use")) {
		t.Fatal("unix variant should match")
	}
	if !isAddrInUse(errors.New("Only one usage of each socket address (protocol/network address/port) is normally permitted")) {
		t.Fatal("windows variant should match")
	}
	if isAddrInUse(errors.New("connection refused")) {
		t.Fatal("unrelated error should not match")
	}
}

func TestServerUptime(t *testing.T) {
	s := newTestServer(t, "")
	if s.uptime() != 0 {
		t.Fatal("zero start time should yield 0")
	}
	s.startTime = time.Now().Add(-3 * time.Second)
	if got := s.uptime(); got < 2 || got > 5 {
		t.Fatalf("uptime: got %d want ~3", got)
	}
}

func TestCorsMiddleware(t *testing.T) {
	// Non-OPTIONS passes through; headers set.
	reached := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	h.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("next handler not called")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS origin header")
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("missing CORS methods header")
	}

	// OPTIONS short-circuits.
	reached = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/foo", nil)
	h.ServeHTTP(rec, req)
	if reached {
		t.Fatal("OPTIONS should not reach next")
	}
	if rec.Code != 204 {
		t.Fatalf("OPTIONS code: got %d want 204", rec.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	s := newTestServer(t, "")
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	s.authMiddleware(inner).ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler should have been called when no token configured")
	}
}

func TestAuthMiddleware_TokenRequired(t *testing.T) {
	s := newTestServer(t, "secret")
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	// Missing header: 401.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	s.authMiddleware(inner).ServeHTTP(rec, req)
	if called {
		t.Fatal("handler should NOT be called with missing token")
	}
	if rec.Code != 401 {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}

	// Wrong token: 401.
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/foo", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	s.authMiddleware(inner).ServeHTTP(rec, req)
	if called {
		t.Fatal("handler should NOT be called with wrong token")
	}
	if rec.Code != 401 {
		t.Fatalf("status: got %d want 401", rec.Code)
	}

	// Correct token: pass through.
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/foo", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.authMiddleware(inner).ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler should be called with correct token")
	}

	// Token via ?token= query (browser WebSocket can't set headers).
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/foo?token=secret", nil)
	s.authMiddleware(inner).ServeHTTP(rec, req)
	if !called {
		t.Fatal("handler should be called with correct token via query")
	}

	// Wrong query token.
	called = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/foo?token=nope", nil)
	s.authMiddleware(inner).ServeHTTP(rec, req)
	if called || rec.Code != 401 {
		t.Fatalf("wrong query token should 401, got %d called=%v", rec.Code, called)
	}
}

func TestHandleHealthz(t *testing.T) {
	s := newTestServer(t, "")

	t.Run("rejects non-GET", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/healthz", nil)
		s.handleHealthz(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("DELETE /healthz = status %d Allow %q, want 405 Allow GET", rec.Code, rec.Header().Get("Allow"))
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.handleHealthz(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status: got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("ok: %+v", body)
	}
	if body["cdpConnected"] != false {
		t.Fatalf("cdpConnected should be false when not connected: %+v", body)
	}
}

func TestHandleHealthzIdentityIsLoopbackOnly(t *testing.T) {
	s := newTestServer(t, "")
	s.opts.Version = "test-version"

	healthz := func(remoteAddr string) map[string]interface{} {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = remoteAddr
		s.handleHealthz(rec, req)
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body: %v", err)
		}
		return body
	}

	// Loopback callers get pid/version: that is what lets a CLI name the
	// daemon squatting on the port when daemon.json is gone.
	local := healthz("127.0.0.1:54321")
	if local["version"] != "test-version" {
		t.Fatalf("loopback healthz should report the version: %+v", local)
	}
	if pid, ok := local["pid"].(float64); !ok || int(pid) != os.Getpid() {
		t.Fatalf("loopback healthz should report the pid: %+v", local)
	}

	// Remote callers of 'borz server' get nothing extra to fingerprint.
	remote := healthz("10.1.2.3:54321")
	if _, ok := remote["version"]; ok {
		t.Fatalf("remote healthz leaked the version: %+v", remote)
	}
	if _, ok := remote["pid"]; ok {
		t.Fatalf("remote healthz leaked the pid: %+v", remote)
	}
	if remote["ok"] != true {
		t.Fatalf("remote healthz should still report health: %+v", remote)
	}

	// IPv6 loopback counts, and an unparsable RemoteAddr does not.
	if _, ok := healthz("[::1]:54321")["pid"]; !ok {
		t.Fatal("IPv6 loopback should count as local")
	}
	if _, ok := healthz("garbage")["pid"]; ok {
		t.Fatal("unparsable RemoteAddr must not be treated as loopback")
	}
}

func TestHandleStatus(t *testing.T) {
	s := newTestServer(t, "")
	s.opts.Version = "test-version"
	s.cdp.TabManager.AddTab("target-1")

	// Wrong method.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/status", nil)
	s.handleStatus(rec, req)
	if rec.Code != 405 {
		t.Fatalf("POST status: got %d want 405", rec.Code)
	}

	// GET.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/status", nil)
	s.handleStatus(rec, req)

	if rec.Code != 200 {
		t.Fatalf("GET status: got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["running"] != true {
		t.Fatalf("running: %+v", body)
	}
	if body["version"] != "test-version" {
		t.Fatalf("version: %+v", body)
	}
	if body["cdpHost"] != s.cdp.Host || int(body["cdpPort"].(float64)) != s.cdp.Port {
		t.Fatalf("CDP endpoint: %+v", body)
	}
	tabs, ok := body["tabs"].([]interface{})
	if !ok || len(tabs) != 1 {
		t.Fatalf("tabs: %+v", body["tabs"])
	}
	tab := tabs[0].(map[string]interface{})
	if tab["targetId"] != "target-1" {
		t.Fatalf("targetId: %+v", tab)
	}
}

func TestHandleShutdown_WrongMethod(t *testing.T) {
	// Only test wrong-method path — the POST path calls os.Exit asynchronously.
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/shutdown", nil)
	s.handleShutdown(rec, req)

	if rec.Code != 405 {
		t.Fatalf("GET /shutdown: got %d want 405", rec.Code)
	}
}

func TestHandleCommand_Rejections(t *testing.T) {
	s := newTestServer(t, "")

	// Wrong method.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/command", nil)
	s.handleCommand(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /command: got %d want 405", rec.Code)
	}

	// Invalid JSON body.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/command", strings.NewReader("not json"))
	s.handleCommand(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad JSON: got %d want 400", rec.Code)
	}

	// Body read failure via an errReader.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/command", &errReader{})
	s.handleCommand(rec, req)
	if rec.Code != 400 {
		t.Fatalf("body read err: got %d want 400", rec.Code)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestNewServer_Defaults(t *testing.T) {
	s := NewServer(ServerOptions{})
	if s == nil {
		t.Fatal("nil server")
	}
	if s.opts.Host == "" || s.opts.Port == 0 {
		t.Fatalf("defaults not applied: %+v", s.opts)
	}
	if s.cdp == nil {
		t.Fatal("cdp not initialized")
	}
}

func TestServerExtHub(t *testing.T) {
	s := NewServer(ServerOptions{})
	if s.ExtHub() == nil {
		t.Fatal("ExtHub returned nil")
	}
}

func TestLogCommandPersistsSafeMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	logger, err := observability.Open("daemon", "test")
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "")
	s.logger = logger
	s.logCommand(&protocol.Request{
		ID: "r1", Action: protocol.ActionFill, URL: "https://example.test/private?token=secret",
		Ref: "9", Text: "super-secret", Script: "secret-script",
	}, "mcp", "session-1", time.Now().Add(-20*time.Millisecond), false, "stale_ref")
	_ = logger.Close()
	entries, err := observability.ReadEntries(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != "fill" || entries[0].URLHost != "example.test" || entries[0].TextBytes == 0 {
		t.Fatalf("entries = %+v", entries)
	}
	raw, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret") || strings.Contains(string(raw), "secret-script") || strings.Contains(string(raw), "token=") {
		t.Fatalf("sensitive command data leaked: %s", raw)
	}
}

func TestServerRunReportsAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	s := NewServer(ServerOptions{Host: "127.0.0.1", Port: addr.Port})
	err = s.Run()
	if err == nil {
		t.Fatal("expected address-in-use error")
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServerRunRejectsSecondDaemonForProfile(t *testing.T) {
	t.Setenv("BORZ_HOME", t.TempDir())
	if err := config.SetProfile("locked"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
	if _, err := config.EnsureRuntimeDir(); err != nil {
		t.Fatal(err)
	}
	held, err := processlock.Acquire(config.DaemonLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s := NewServer(ServerOptions{Host: "127.0.0.1", Port: port, CDPHost: "127.0.0.1", CDPPort: 1})
	if err := s.RunContext(context.Background()); err == nil || !strings.Contains(err.Error(), "daemon already running for profile") {
		t.Fatalf("RunContext error = %v", err)
	}
}

func TestServerShutdownCleansState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	own := fmt.Sprintf(`{"pid":%d,"host":"127.0.0.1","port":19824}`, os.Getpid())
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), []byte(own), 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}

	s := newTestServer(t, "")
	s.httpSrv = &http.Server{}
	reaperCtx, cancel := context.WithCancel(context.Background())
	s.cancelReaper = cancel

	if err := s.shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon.json still exists or stat failed differently: %v", err)
	}
	select {
	case <-reaperCtx.Done():
	default:
		t.Fatal("reaper context was not cancelled")
	}
}

// A daemon that is shutting down after a successor already republished
// daemon.json must leave that record alone. Deleting it would strand the
// successor: alive, holding the port and the daemon lock, addressable by
// nobody, with no CLI able to recover until it dies.
func TestServerShutdownKeepsSuccessorDaemonJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	path := filepath.Join(home, "daemon.json")
	successor := fmt.Sprintf(`{"pid":%d,"host":"127.0.0.1","port":19824}`, os.Getpid()+1)
	if err := os.WriteFile(path, []byte(successor), 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}

	s := newTestServer(t, "")
	s.httpSrv = &http.Server{}
	_, cancel := context.WithCancel(context.Background())
	s.cancelReaper = cancel

	if err := s.shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("successor daemon.json was deleted: %v", err)
	}
	if string(got) != successor {
		t.Fatalf("daemon.json = %s, want it untouched", got)
	}

	// An unparsable record is also left alone: we cannot prove it is ours, and
	// the next daemon overwrites it anyway.
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeOwnDaemonJSON()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt daemon.json was deleted: %v", err)
	}
	// Absent file: must not panic or error.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	removeOwnDaemonJSON()
}

func TestServerShutdownClosesOnlyOwnedManagedBrowser(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ServerOptions
		want int
	}{
		{name: "owned managed browser", opts: ServerOptions{CloseOwnedBrowser: true}, want: 1},
		{name: "ensure hook launched browser", opts: ServerOptions{BrowserOwned: func() bool { return true }}, want: 1},
		{name: "external cdp browser", opts: ServerOptions{BrowserOwned: func() bool { return false }}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCDP(t)
			setupOnePage(f, "T1", "https://a", "A")
			f.On("Browser.close", func(json.RawMessage) (interface{}, error) {
				return map[string]interface{}{}, nil
			})
			c := connectCdp(t, f)

			s := NewServer(tc.opts)
			s.cdp = c
			if err := s.shutdown(); err != nil {
				t.Fatalf("shutdown: %v", err)
			}
			closed := 0
			for _, call := range f.Calls() {
				if call.Method == "Browser.close" {
					closed++
				}
			}
			if closed != tc.want {
				t.Fatalf("Browser.close calls = %d, want %d", closed, tc.want)
			}
		})
	}
}

func TestServerShutdownContinuesWhenOwnedBrowserCloseFails(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Browser.close", func(json.RawMessage) (interface{}, error) {
		return nil, errors.New("close failed")
	})
	c := connectCdp(t, f)

	s := NewServer(ServerOptions{CloseOwnedBrowser: true})
	s.cdp = c
	if err := s.shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if c.Connected() {
		t.Fatal("daemon should disconnect even when Browser.close fails")
	}
}
