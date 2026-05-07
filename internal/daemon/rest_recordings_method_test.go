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
	}
}
