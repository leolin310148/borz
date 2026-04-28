package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/daemon/extbridge"
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

	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["hello"] != "world" {
		t.Fatalf("body: got %+v", got)
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

func TestHandleStatus(t *testing.T) {
	s := newTestServer(t, "")
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
