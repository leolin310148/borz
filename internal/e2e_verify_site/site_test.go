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

func TestHandlerServesRedirectChain(t *testing.T) {
	h := Handler()
	for _, step := range []struct {
		path     string
		status   int
		location string
	}{
		{path: "/redirect/start", status: http.StatusFound, location: "/redirect/middle"},
		{path: "/redirect/middle", status: http.StatusTemporaryRedirect, location: "/redirect/final"},
	} {
		req := httptest.NewRequest(http.MethodGet, step.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != step.status || rec.Header().Get("Location") != step.location {
			t.Errorf("%s response = status %d location %q, want %d %q", step.path, rec.Code, rec.Header().Get("Location"), step.status, step.location)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/redirect/final", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="redirect-ready"`) || !strings.Contains(rec.Body.String(), "E2E Redirect Final") {
		t.Fatalf("redirect final response = status %d body %.200q", rec.Code, rec.Body.String())
	}
}

func TestHandlerServesURLFidelityRoutes(t *testing.T) {
	h := Handler()
	for _, target := range []string{
		"/url-fidelity?name=%E6%B8%AC%E8%A9%A6",
		"/url-fidelity/%E8%B7%AF%E5%BE%91?name=%E6%B8%AC%E8%A9%A6#%E7%89%87%E6%AE%B5",
	} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s status=%d", target, rec.Code)
			}
			for _, marker := range []string{
				`id="url-fidelity-ready"`,
				`id="url-path"`,
				`id="url-query"`,
				`id="url-fragment"`,
				`currentURL.searchParams.get('name')`,
			} {
				if !strings.Contains(rec.Body.String(), marker) {
					t.Errorf("%s missing %q", target, marker)
				}
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/url-fidelity/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown URL fidelity route status=%d", rec.Code)
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

func TestHandlerServesShadowDOMPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/shadow-dom", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("shadow DOM status=%d", rec.Code)
	}
	for _, marker := range []string{
		`id="shadow-host"`,
		`attachShadow({ mode: 'open' })`,
		`id="nested-shadow-controls"`,
		`aria-label="Shadow action button"`,
		`aria-label="Shadow text input"`,
		`id="shadow-result" role="status"`,
		`host.dataset.shadowReady = 'true'`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("shadow DOM page missing %q", marker)
		}
	}
}

func TestHandlerServesAccessibilityStatePage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/accessibility-state", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("accessibility state status=%d", rec.Code)
	}
	for _, marker := range []string{
		`id="accessibility-state-ready"`,
		`id="disabled-action" type="button" disabled`,
		`aria-expanded="false"`,
		`id="disclosure-panel" hidden`,
		`aria-checked="false"`,
		`role="option" aria-selected="true"`,
		`role="status" aria-live="polite"`,
		`aria-label="Mutate accessibility state"`,
		`live.textContent = changed ? 'Accessibility state updated'`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("accessibility state page missing %q", marker)
		}
	}
}

func TestHandlerServesScrollingPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/scrolling", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("scrolling status=%d", rec.Code)
	}
	for _, marker := range []string{
		`id="scrolling-ready"`,
		`id="outer-scroll" aria-label="Outer scrolling container"`,
		`id="inner-scroll" aria-label="Inner scrolling container"`,
		`id="nested-end-marker"`,
		`id="viewport-end-marker"`,
		`outer.scrollTo(40, 50)`,
		`inner.scrollTo(60, 70)`,
		`dataset.initialized = 'true'`,
	} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("scrolling page missing %q", marker)
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
