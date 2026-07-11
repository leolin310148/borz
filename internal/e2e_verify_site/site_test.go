package e2e_verify_site

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerServesVerifyPagesAndAPI(t *testing.T) {
	ts := httptest.NewServer(Handler())
	t.Cleanup(ts.Close)

	body := getBody(t, ts.URL+"/")
	if !strings.Contains(body, `id="ready"`) || !strings.Contains(body, "E2E Verify Site") {
		t.Fatalf("root page missing verify marker: %.200q", body)
	}

	frame := getBody(t, ts.URL+"/frame.html")
	for _, marker := range []string{
		"Frame ready",
		`id="frame-controls" aria-label="Frame controls"`,
		`aria-label="Frame text input"`,
		`aria-label="Submit frame input"`,
		`id="frame-result" role="status"`,
		`'Frame received: ' + value`,
	} {
		if !strings.Contains(frame, marker) {
			t.Errorf("frame page missing %q", marker)
		}
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(getBody(t, ts.URL+"/api/data")), &data); err != nil {
		t.Fatalf("api data JSON: %v", err)
	}
	if data["message"] != "hello from e2e verify site" {
		t.Fatalf("api data = %+v", data)
	}
}

func TestHandlerServesNetworkEndpoints(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/network/echo?status=201&response=created", strings.NewReader("request payload"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("network echo status=%d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("network echo JSON: %v", err)
	}
	if body["method"] != http.MethodPost || body["requestBody"] != "request payload" || body["response"] != "created" {
		t.Fatalf("network echo body=%+v", body)
	}

	started := time.Now()
	req = httptest.NewRequest(http.MethodGet, "/api/network/slow?status=202&response=slow", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || time.Since(started) < 250*time.Millisecond {
		t.Fatalf("network slow status=%d elapsed=%s", rec.Code, time.Since(started))
	}
}

func TestHandlerAdditionalPagesAndNotFound(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/page2", "/tab", "/frame.html"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status=%d", path, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
				t.Fatalf("%s content-type=%q", path, ct)
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	root(rec, httptest.NewRequest(http.MethodGet, "/not-root", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("root non-root status=%d", rec.Code)
	}
}

func TestHandlerServesSPARoutes(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/spa", "/spa/details"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status=%d", path, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
				t.Fatalf("%s content-type=%q", path, ct)
			}

			body := rec.Body.String()
			for _, marker := range []string{
				`id="spa-ready"`,
				`aria-label="Go to SPA home"`,
				`aria-label="Go to SPA details"`,
				`history.pushState`,
				`addEventListener('popstate'`,
			} {
				if !strings.Contains(body, marker) {
					t.Errorf("%s missing %q", path, marker)
				}
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/spa/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown SPA route status=%d", rec.Code)
	}
}

func TestHandlerServesDelayedRenderPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/delayed-render", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delayed render status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("delayed render content-type=%q", ct)
	}
	for _, marker := range []string{
		`id="delayed-page-ready"`,
		`id = 'delayed-marker'`,
		`window.setTimeout`,
		`}, 750)`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("delayed render page missing %q", marker)
		}
	}
}

func TestHandlerServesAsyncActionPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/async-action", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("async action status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("async action content-type=%q", ct)
	}
	for _, marker := range []string{
		`id="async-action-ready"`,
		`aria-label="Start async action"`,
		`id = 'async-action-result'`,
		`window.setTimeout`,
		`}, 750)`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("async action page missing %q", marker)
		}
	}
}

func TestHandlerServesFileUploadPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/file-upload", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("file upload status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("file upload content-type=%q", ct)
	}
	for _, marker := range []string{
		`id="file-upload-ready"`,
		`type="file" aria-label="Single file upload"`,
		`type="file" multiple aria-label="Multiple file upload"`,
		`id="single-upload-state" data-file-count="0"`,
		`id="multiple-upload-state" data-file-count="0"`,
		`file.name + ': ' + await file.text()`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("file upload page missing %q", marker)
		}
	}
}

func TestHandlerServesDialogsPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dialogs", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("dialogs status=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("dialogs content-type=%q", ct)
	}
	for _, marker := range []string{
		`id="dialogs-ready"`,
		`aria-label="Open alert dialog"`,
		`aria-label="Open confirm dialog"`,
		`aria-label="Open prompt dialog"`,
		`alert('E2E alert')`,
		`confirm('E2E confirm')`,
		`prompt('E2E prompt', 'default prompt')`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("dialogs page missing %q", marker)
		}
	}
}

func TestHandlerServesKeyboardPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/keyboard", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("keyboard status=%d", rec.Code)
	}
	for _, marker := range []string{
		`id="keyboard-ready"`,
		`aria-label="First focus stop"`,
		`id="enter-button"`,
		`id="space-button"`,
		`id="dismissible-panel"`,
		`role="listbox"`,
		`event.key === 'ArrowDown'`,
		`event.key === 'Escape'`,
		`shift: event.shiftKey`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("keyboard page missing %q", marker)
		}
	}
}

func TestHandlerServesClipboardPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/clipboard", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("clipboard status=%d", rec.Code)
	}
	for _, marker := range []string{
		`id="clipboard-ready"`,
		`class="xterm-helper-textarea"`,
		`aria-label="Clipboard paste input"`,
		`id="paste-event" data-count="0"`,
		`id="input-event" data-count="0"`,
		`addEventListener('paste'`,
		`event.clipboardData.getData('text/plain')`,
		`addEventListener('input'`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("clipboard page missing %q", marker)
		}
	}
}

func TestStartAndClose(t *testing.T) {
	site, err := Start("")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.HasPrefix(site.URL(), "http://") {
		t.Fatalf("URL = %q", site.URL())
	}

	body := getBody(t, site.URL()+"/page2")
	if !strings.Contains(body, "Page Two") {
		t.Fatalf("page2 missing marker: %.200q", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := site.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(data)
}
