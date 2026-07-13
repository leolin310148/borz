package daemon

import (
	"encoding/hex"
	"encoding/json"
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
	if got := (restBody{Since: " 42 "}).sinceValue(); got != 42 {
		t.Fatalf("trimmed numeric string: got %v", got)
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
	// WaitFor is normalized and TimeoutMs propagates.
	ms := 2500
	req = (restBody{WaitFor: " .loaded ", TimeoutMs: &ms}).applyWait(&protocol.Request{Action: protocol.ActionClick})
	if req.WaitFor != ".loaded" {
		t.Fatalf("waitFor = %q", req.WaitFor)
	}
	if req.TimeoutMs == nil || *req.TimeoutMs != 2500 {
		t.Fatalf("timeoutMs = %v", req.TimeoutMs)
	}
}

func TestRestBody_ApplyDelays(t *testing.T) {
	pre, post := 100, 200
	// Both helpers must forward pre/post delay onto the request.
	req := (restBody{PreDelayMs: &pre, PostDelayMs: &post}).applyWait(&protocol.Request{Action: protocol.ActionClick})
	if req.PreDelayMs == nil || *req.PreDelayMs != 100 {
		t.Fatalf("applyWait: preDelayMs = %v", req.PreDelayMs)
	}
	if req.PostDelayMs == nil || *req.PostDelayMs != 200 {
		t.Fatalf("applyWait: postDelayMs = %v", req.PostDelayMs)
	}
	req = (restBody{PreDelayMs: &pre, PostDelayMs: &post}).withActivate(&protocol.Request{Action: protocol.ActionGet})
	if req.PreDelayMs == nil || *req.PreDelayMs != 100 {
		t.Fatalf("withActivate: preDelayMs = %v", req.PreDelayMs)
	}
	if req.PostDelayMs == nil || *req.PostDelayMs != 200 {
		t.Fatalf("withActivate: postDelayMs = %v", req.PostDelayMs)
	}
}

func TestRestBody_ValidateTiming(t *testing.T) {
	zero := 0
	if err := (restBody{TimeoutMs: &zero, PreDelayMs: &zero, PostDelayMs: &zero}).validateTiming(); err != nil {
		t.Fatalf("validateTiming with zero values: %v", err)
	}

	negative := -1
	for name, body := range map[string]restBody{
		"timeoutMs":   {TimeoutMs: &negative},
		"preDelayMs":  {PreDelayMs: &negative},
		"postDelayMs": {PostDelayMs: &negative},
	} {
		t.Run(name, func(t *testing.T) {
			err := body.validateTiming()
			if err == nil || !strings.Contains(err.Error(), name+" must be a non-negative integer") {
				t.Fatalf("validateTiming error = %v", err)
			}
		})
	}
}

func TestReadBody_ParsesDelays(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"preDelayMs":150,"postDelayMs":350}`))
	body, err := readBody(req)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if body.PreDelayMs == nil || *body.PreDelayMs != 150 {
		t.Errorf("preDelayMs = %v", body.PreDelayMs)
	}
	if body.PostDelayMs == nil || *body.PostDelayMs != 350 {
		t.Errorf("postDelayMs = %v", body.PostDelayMs)
	}
}

func TestReadBody_ParsesNewFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"waitFor":".x","timeoutMs":250,"mode":"text","limit":12}`))
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
	if body.Limit == nil || *body.Limit != 12 {
		t.Errorf("limit = %v", body.Limit)
	}
}

func TestRestBody_ViewportOptions(t *testing.T) {
	width, height := 414, 896
	dpr := 3.0
	touch := true
	body := restBody{Preset: "mobile", Width: &width, Height: &height, DPR: &dpr, Touch: &touch}
	vp, err := body.viewportOptions()
	if err != nil {
		t.Fatalf("viewportOptions: %v", err)
	}
	if vp == nil || vp.Width != 414 || vp.Height != 896 || vp.DPR != 3 || !vp.Mobile || vp.Touch == nil || !*vp.Touch {
		t.Fatalf("viewport options = %+v", vp)
	}
	vp, err = (restBody{Mobile: true}).viewportOptions()
	if err != nil {
		t.Fatalf("mobile shorthand viewportOptions: %v", err)
	}
	if vp == nil || vp.Width != 390 || vp.Height != 844 || vp.DPR != 3 || !vp.Mobile || vp.Touch == nil || !*vp.Touch {
		t.Fatalf("mobile shorthand viewport options = %+v", vp)
	}
	vp, err = (restBody{Mobile: true, Width: &width, Height: &height}).viewportOptions()
	if err != nil {
		t.Fatalf("custom mobile viewportOptions: %v", err)
	}
	if vp == nil || vp.Width != 414 || vp.Height != 896 || vp.DPR != 0 || !vp.Mobile {
		t.Fatalf("custom mobile viewport options = %+v", vp)
	}
	vp, err = (restBody{Reset: true}).viewportOptions()
	if err != nil {
		t.Fatalf("reset viewportOptions: %v", err)
	}
	if vp == nil || !vp.Reset {
		t.Fatalf("reset viewport options = %+v", vp)
	}
	if got, err := (restBody{}).viewportOptions(); err != nil || got != nil {
		t.Fatalf("empty viewport options = %+v", got)
	}
	if _, err := (restBody{Preset: "moblie"}).viewportOptions(); err == nil || !strings.Contains(err.Error(), "preset must be one of") {
		t.Fatalf("invalid preset error = %v", err)
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
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, POST" {
		t.Fatalf("Allow = %q want %q", got, "GET, POST")
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

	// Whitespace-only body is also treated as empty for clients that send a
	// blank JSON body with formatting.
	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(" \n\t "))
	body, err = readBody(req)
	if err != nil {
		t.Fatalf("whitespace: %v", err)
	}
	if body.URL != "" {
		t.Fatalf("whitespace body should yield zero struct, got %+v", body)
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

	// Oversized body is rejected before JSON parsing.
	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("x", int(maxRESTBodyBytes)+1)))
	if _, err = readBody(req); err == nil || !strings.Contains(err.Error(), "request body too large") {
		t.Fatalf("expected body-size error, got %v", err)
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
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/click: got %d want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("GET /v1/click Allow = %q want %q", got, http.MethodPost)
	}
	assertRESTErrorEnvelope(t, rec, "Method not allowed")
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
	assertRESTErrorEnvelope(t, rec, "invalid JSON")
}

func assertRESTErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantError string) {
	t.Helper()
	var resp protocol.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode REST error envelope: %v\n%s", err, rec.Body.String())
	}
	if resp.ID == "" || resp.Success || !strings.Contains(resp.Error, wantError) || resp.Data != nil {
		t.Fatalf("REST error envelope = %+v, want id, success=false, error containing %q", resp, wantError)
	}
}

func TestRestJSON_InvalidViewportPreset(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/viewport", strings.NewReader(`{"preset":"moblie"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("invalid preset: got %d want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "preset must be one of") {
		t.Fatalf("invalid preset response = %s", rec.Body.String())
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
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /v1/tabs: got %d want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, POST" {
		t.Fatalf("PUT /v1/tabs Allow = %q want %q", got, "GET, POST")
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
		{"/v1/upload", `{"ref":"e1","files":["/tmp/borz-upload-test.bin"]}`},
		{"/v1/upload", `{"ref":"e1","file":"/tmp/borz-upload-test.bin"}`},
		{"/v1/press", `{"key":"Enter","modifiers":["shift"]}`},
		{"/v1/key", `{"keyType":"press","key":"A","code":"KeyA","text":"a","modifiers":["ctrl"],"activate":true}`},
		{"/v1/mouse", `{"mouseType":"click","x":1,"y":2,"button":"left","clickCount":1,"activate":true}`},
		{"/v1/clipboard-read", `{"activate":true}`},
		{"/v1/clipboard-write", `{"text":"echo hi","paste":true,"tab":"T1"}`},
		{"/v1/clipboard-write", `{"text":"plain","tab":"T1"}`},
		{"/v1/term-text", `{"tab":"T1"}`},
		{"/v1/scroll", `{"direction":"down","pixels":10}`},
		{"/v1/eval", `{"script":"1+1"}`},
		{"/v1/wait", `{"ms":1,"activate":true}`},
		{"/v1/viewport", `{"preset":"mobile"}`},
		{"/v1/snapshot", `{"interactive":true,"compact":true,"maxDepth":2,"selector":"main","role":"button","mode":"text","activate":true}`},
		{"/v1/snapshot", `{"diff":true}`},
		{"/v1/screenshot", `{"path":"/tmp/shot.png","activate":true}`},
		{"/v1/get", `{"attribute":"text","ref":"e1","activate":true}`},
		{"/v1/network", `{"command":"requests","filter":"api","withBody":true,"method":"GET","status":"200","since":"last_action","limit":10,"activate":true}`},
		{"/v1/console", `{"command":"clear","filter":"x","since":3,"limit":10,"activate":true}`},
		{"/v1/errors", `{"command":"clear","filter":"x","since":"4","limit":10,"activate":true}`},
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

func TestRestBody_UploadFiles(t *testing.T) {
	if got := (restBody{Files: []string{"/a", "  ", "/b"}}).uploadFiles(); len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Errorf("Files: %v", got)
	}
	if got := (restBody{File: "  /c  "}).uploadFiles(); len(got) != 1 || got[0] != "/c" {
		t.Errorf("File single: %v", got)
	}
	if got := (restBody{File: "   "}).uploadFiles(); got != nil {
		t.Errorf("blank File: %v", got)
	}
	if got := (restBody{}).uploadFiles(); got != nil {
		t.Errorf("empty: %v", got)
	}
}

func TestRESTUpload_MissingFiles(t *testing.T) {
	s, _ := serverWithFakeCDP(t)
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/upload", strings.NewReader(`{"ref":"e1"}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("missing files: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "files") {
		t.Errorf("expected error about files; got %s", rec.Body.String())
	}
}

// Smoke the read loop didn't accidentally close the body reader.
var _ io.Reader = (*errReader)(nil)

func TestRestFileChooser_AcceptRequiresFiles(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/filechooser", strings.NewReader(`{"command":"accept"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("accept without files: got %d want 400", rec.Code)
	}
	assertRESTErrorEnvelope(t, rec, "files (or file) is required")
}

// The new routes must be registered (a GET yields 405, not 404) — a missing
// mux entry would otherwise surface as a confusing "404 page not found".
func TestRestRoutes_NewEndpointsRegistered(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerRESTRoutes(mux)

	for _, path := range []string{"/v1/tabs/front", "/v1/page/visibility", "/v1/filechooser"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: got %d want 405 (route must exist and be POST-only)", path, rec.Code)
		}
	}
}

func TestRestBody_VisibilityAndCommands(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"visibility":"visible","commands":["selectAll"],"key":"a","modifiers":["meta"]}`))
	body, err := readBody(req)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if body.Visibility != "visible" {
		t.Errorf("visibility = %q", body.Visibility)
	}
	if len(body.Commands) != 1 || body.Commands[0] != "selectAll" {
		t.Errorf("commands = %v", body.Commands)
	}
}
