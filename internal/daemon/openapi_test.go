package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIRoutes(t *testing.T) {
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerDocsRoutes(mux)

	t.Run("spec served as yaml", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status: got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
			t.Fatalf("content-type: got %q", ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "openapi:") || !strings.Contains(body, "/v1/open") {
			t.Fatalf("spec body looks wrong: %q", body[:min(200, len(body))])
		}
	})

	t.Run("docs page references spec", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/docs", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status: got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("content-type: got %q", ct)
		}
		if !strings.Contains(rec.Body.String(), "/openapi.yaml") {
			t.Fatal("docs page should reference /openapi.yaml")
		}
	})

	t.Run("non-GET rejected", func(t *testing.T) {
		for _, path := range []string{"/openapi.yaml", "/docs"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, nil)
			mux.ServeHTTP(rec, req)
			if rec.Code != 405 {
				t.Fatalf("%s POST: got %d, want 405", path, rec.Code)
			}
		}
	})
}
