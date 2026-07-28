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

	t.Run("spec documents snapshot --diff", func(t *testing.T) {
		body := string(openAPISpec)
		// The flag must show up on the request schema for /v1/snapshot.
		if !strings.Contains(body, "diff:") {
			t.Fatal("/v1/snapshot request schema must include `diff` field")
		}
		// And the response schema must surface the diff data.
		if !strings.Contains(body, "snapshotDiffData") {
			t.Fatal("response schema must include `snapshotDiffData`")
		}
		if !strings.Contains(body, "SnapshotDiffData:") {
			t.Fatal("components/schemas must define SnapshotDiffData")
		}
		if !strings.Contains(body, "DiffEntry:") || !strings.Contains(body, "DiffChange:") {
			t.Fatal("components/schemas must define DiffEntry and DiffChange")
		}
	})

	t.Run("spec documents observation limits", func(t *testing.T) {
		body := string(openAPISpec)
		for _, summary := range []string{
			"Return at most this many newest matching requests",
			"Return at most this many newest matching messages",
			"Return at most this many newest matching errors",
		} {
			if !strings.Contains(body, summary) {
				t.Fatalf("observation limit field missing from OpenAPI spec: %q", summary)
			}
		}
	})

	t.Run("spec documents site adapter startUrl", func(t *testing.T) {
		body := string(openAPISpec)
		if !strings.Contains(body, "startUrl:") {
			t.Fatal("SiteMeta schema must include startUrl")
		}
	})

	t.Run("spec documents typed WebAuthn lifecycle", func(t *testing.T) {
		body := string(openAPISpec)
		for _, want := range []string{
			"/v1/webauthn:",
			"set-user-verified",
			"set-automatic-presence",
			"hasResidentKey:",
			"automaticPresence:",
			"this is not a raw CDP",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("WebAuthn OpenAPI missing %q", want)
			}
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
			if got := rec.Header().Get("Allow"); got != http.MethodGet {
				t.Fatalf("%s POST Allow = %q, want %q", path, got, http.MethodGet)
			}
		}
	})
}
