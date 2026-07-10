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
	if !strings.Contains(frame, "Frame ready") {
		t.Fatalf("frame page missing marker: %.200q", frame)
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(getBody(t, ts.URL+"/api/data")), &data); err != nil {
		t.Fatalf("api data JSON: %v", err)
	}
	if data["message"] != "hello from e2e verify site" {
		t.Fatalf("api data = %+v", data)
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
