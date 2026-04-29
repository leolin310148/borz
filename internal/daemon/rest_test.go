package daemon

import (
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
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

func TestRESTRoutes_RequestBuilders(t *testing.T) {
	s, _ := serverWithFakeCDP(t)
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	cases := []struct {
		path string
		body string
	}{
		{"/v1/open", `{"url":"https://example.test","new":true,"tab":"tab-1"}`},
		{"/v1/forward", `{}`},
		{"/v1/refresh", `{}`},
		{"/v1/close", `{"activate":true}`},
		{"/v1/hover", `{"ref":"e1"}`},
		{"/v1/fill", `{"ref":"e1","text":"hello"}`},
		{"/v1/type", `{"ref":"e1","text":"hello"}`},
		{"/v1/check", `{"ref":"e1"}`},
		{"/v1/uncheck", `{"ref":"e1"}`},
		{"/v1/select", `{"ref":"e1","value":"x"}`},
		{"/v1/press", `{"key":"Enter","modifiers":["shift"]}`},
		{"/v1/key", `{"keyType":"press","key":"A","code":"KeyA","text":"a","modifiers":["ctrl"],"activate":true}`},
		{"/v1/mouse", `{"mouseType":"click","x":1,"y":2,"button":"left","clickCount":1,"activate":true}`},
		{"/v1/clipboard-read", `{"activate":true}`},
		{"/v1/scroll", `{"direction":"down","pixels":10}`},
		{"/v1/eval", `{"script":"1+1"}`},
		{"/v1/wait", `{"ms":1,"activate":true}`},
		{"/v1/snapshot", `{"interactive":true,"compact":true,"maxDepth":2,"selector":"main","role":"button","mode":"text","activate":true}`},
		{"/v1/screenshot", `{"path":"/tmp/shot.png","activate":true}`},
		{"/v1/get", `{"attribute":"text","ref":"e1","activate":true}`},
		{"/v1/network", `{"command":"requests","filter":"api","withBody":true,"method":"GET","status":"200","since":"last_action","activate":true}`},
		{"/v1/console", `{"command":"clear","filter":"x","since":3,"activate":true}`},
		{"/v1/errors", `{"command":"clear","filter":"x","since":"4","activate":true}`},
		{"/v1/fetch", `{"url":"https://api.test","method":"post","activate":true}`},
		{"/v1/tabs/select", `{"index":0}`},
		{"/v1/tabs/select", `{"tabId":"T1"}`},
		{"/v1/tabs/close", `{"index":0}`},
		{"/v1/tabs/close", `{"tabId":"T1"}`},
	}

	for _, tc := range cases {
		t.Run(tc.path+" "+tc.body, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			mux.ServeHTTP(rec, req)
			if rec.Code < 200 || rec.Code >= 500 {
				t.Fatalf("%s returned %d: %s", tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// Smoke the read loop didn't accidentally close the body reader.
var _ io.Reader = (*errReader)(nil)
