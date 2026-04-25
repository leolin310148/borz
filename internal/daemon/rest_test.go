package daemon

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

func TestRestBody_TabID(t *testing.T) {
	if got := (restBody{}).tabID(); got != nil {
		t.Fatalf("empty body: got %v want nil", got)
	}
	if got := (restBody{TabID: "abc"}).tabID(); got != "abc" {
		t.Fatalf("tabId string: got %v", got)
	}
	if got := (restBody{TabID: float64(3)}).tabID(); got != float64(3) {
		t.Fatalf("tabId number: got %v", got)
	}
	if got := (restBody{Tab: "xyz"}).tabID(); got != "xyz" {
		t.Fatalf("tab alias: got %v", got)
	}
	// TabID takes precedence over Tab.
	if got := (restBody{TabID: "primary", Tab: "fallback"}).tabID(); got != "primary" {
		t.Fatalf("precedence: got %v", got)
	}
}

func TestRestBody_SinceValue(t *testing.T) {
	if got := (restBody{}).sinceValue(); got != nil {
		t.Fatalf("nil: got %v", got)
	}
	if got := (restBody{Since: float64(5)}).sinceValue(); got != 5 {
		t.Fatalf("float64: got %v", got)
	}
	if got := (restBody{Since: "last_action"}).sinceValue(); got != "last_action" {
		t.Fatalf("last_action: got %v", got)
	}
	if got := (restBody{Since: "42"}).sinceValue(); got != 42 {
		t.Fatalf("numeric string: got %v", got)
	}
	// Non-numeric string falls through to raw value.
	if got := (restBody{Since: "garbage"}).sinceValue(); got != "garbage" {
		t.Fatalf("non-numeric string: got %v", got)
	}
	// Unknown type also falls through.
	if got := (restBody{Since: true}).sinceValue(); got != true {
		t.Fatalf("bool: got %v", got)
	}
}

func TestRestBody_ApplyWait(t *testing.T) {
	// Empty body leaves req untouched.
	req := (restBody{}).applyWait(&protocol.Request{Action: protocol.ActionClick})
	if req.WaitFor != "" || req.TimeoutMs != nil {
		t.Fatalf("empty body should not set wait fields: %+v", req)
	}
	// WaitFor + TimeoutMs propagate.
	ms := 2500
	req = (restBody{WaitFor: ".loaded", TimeoutMs: &ms}).applyWait(&protocol.Request{Action: protocol.ActionClick})
	if req.WaitFor != ".loaded" {
		t.Fatalf("waitFor = %q", req.WaitFor)
	}
	if req.TimeoutMs == nil || *req.TimeoutMs != 2500 {
		t.Fatalf("timeoutMs = %v", req.TimeoutMs)
	}
}

func TestReadBody_ParsesNewFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"waitFor":".x","timeoutMs":250,"mode":"text"}`))
	body, err := readBody(req)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if body.WaitFor != ".x" {
		t.Errorf("waitFor = %q", body.WaitFor)
	}
	if body.TimeoutMs == nil || *body.TimeoutMs != 250 {
		t.Errorf("timeoutMs = %v", body.TimeoutMs)
	}
	if body.Mode != "text" {
		t.Errorf("mode = %q", body.Mode)
	}
}

func TestHandleDoctor_NoCDP(t *testing.T) {
	s := newTestServer(t, "")
	s.opts.Version = "test-1.0"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/doctor", nil)
	s.handleDoctor(rec, req)
	// CDP is unattached in tests, so the handler must report the failure.
	if rec.Code != 503 {
		t.Fatalf("expected 503 when CDP not attached, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "\"checks\"") || !strings.Contains(rec.Body.String(), "test-1.0") {
		t.Fatalf("body missing expected fields: %s", rec.Body.String())
	}
}

func TestHandleDoctor_RejectsWrongMethod(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/doctor", nil)
	s.handleDoctor(rec, req)
	if rec.Code != 405 {
		t.Fatalf("got %d want 405", rec.Code)
	}
}

func TestNewReqID(t *testing.T) {
	id := newReqID()
	if len(id) != 16 {
		t.Fatalf("length: got %d want 16", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("not hex: %v", err)
	}
	if other := newReqID(); other == id {
		t.Fatalf("IDs should differ: %q %q", id, other)
	}
}

func TestReadBody(t *testing.T) {
	// Empty body -> zero struct, no error.
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(""))
	body, err := readBody(req)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if body.URL != "" {
		t.Fatalf("empty body should yield zero struct, got %+v", body)
	}

	// Valid JSON.
	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"url":"https://example.com","new":true}`))
	body, err = readBody(req)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if body.URL != "https://example.com" || !body.New {
		t.Fatalf("parsed: %+v", body)
	}

	// Invalid JSON.
	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{not json`))
	if _, err = readBody(req); err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// Read failure.
	req = httptest.NewRequest(http.MethodPost, "/x", &errReader{})
	if _, err = readBody(req); err == nil {
		t.Fatal("expected error for broken reader")
	}
}

// The /v1/* handlers all share restJSON's method + body validation path;
// exercising one is enough to cover that code. The success path requires a real
// CDP connection and is covered by integration tests.
func TestRestJSON_MethodRejection(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	// Wrong method on an arbitrary /v1/* route.
	req := httptest.NewRequest(http.MethodGet, "/v1/click", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /v1/click: got %d want 405", rec.Code)
	}
}

func TestRestJSON_BadJSON(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/click", strings.NewReader(`{bogus`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad JSON: got %d want 400", rec.Code)
	}
}

func TestRestJSON_ReadBodyError(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/click", &errReader{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("read err: got %d want 400", rec.Code)
	}
}

func TestTabsRoute_MethodDispatch(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	// PUT is not allowed.
	req := httptest.NewRequest(http.MethodPut, "/v1/tabs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("PUT /v1/tabs: got %d want 405", rec.Code)
	}

	// POST with broken body -> 400.
	req = httptest.NewRequest(http.MethodPost, "/v1/tabs", &errReader{})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("POST /v1/tabs err body: got %d want 400", rec.Code)
	}
}

// registerRESTRoutes registers many handlers; calling it once asserts there are
// no duplicate registrations and no panics.
func TestRegisterRESTRoutes_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registerRESTRoutes panicked: %v", r)
		}
	}()
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)
}

// Smoke the read loop didn't accidentally close the body reader.
var _ io.Reader = (*errReader)(nil)
