package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecordingPauseResumeRequirePost(t *testing.T) {
	s := newTestServer(t, "")
	s.recordings = newRecordingManager(s.cdp, s.extHub)

	mux := http.NewServeMux()
	s.registerRecordingRoutes(mux)

	for _, path := range []string{
		"/v1/recordings/missing/pause",
		"/v1/recordings/missing/resume",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s got %d body=%s, want %d", path, rec.Code, rec.Body.String(), http.StatusMethodNotAllowed)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Fatalf("GET %s Allow = %q, want %q", path, got, http.MethodPost)
		}
	}
}

func TestRecordingRoutesMethodNotAllowedSetsAllow(t *testing.T) {
	s := newTestServer(t, "")
	s.recordings = newRecordingManager(s.cdp, s.extHub)

	mux := http.NewServeMux()
	s.registerRecordingRoutes(mux)

	tests := []struct {
		method string
		path   string
		allow  string
	}{
		{http.MethodPut, "/v1/recordings", "GET, POST"},
		{http.MethodPost, "/v1/recordings/missing", http.MethodGet},
		{http.MethodGet, "/v1/recordings/missing/stop", http.MethodPost},
		{http.MethodGet, "/v1/recordings/missing/redact", http.MethodPost},
	}

	for _, tc := range tests {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s got %d body=%s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), http.StatusMethodNotAllowed)
		}
		if got := rec.Header().Get("Allow"); got != tc.allow {
			t.Fatalf("%s %s Allow = %q, want %q", tc.method, tc.path, got, tc.allow)
		}
	}
}
