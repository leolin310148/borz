package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/daemon/extbridge"
	"github.com/leolin310148/borz/internal/recorder"
)

func TestRecordingManagerErrorAndInfoBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	m := newRecordingManager(nil, extbridge.NewHub())

	if _, err := m.Start(recorder.CaptureOptions{ID: "bad", Mode: "bogus", Out: filepath.Join(home, "bad.borzrec")}); err == nil || !strings.Contains(err.Error(), "unknown recording mode") {
		t.Fatalf("invalid mode error = %v", err)
	}
	if _, err := m.Start(recorder.CaptureOptions{ID: "client", Mode: "client", Out: filepath.Join(home, "client.borzrec")}); err == nil || !strings.Contains(err.Error(), "connected borz extension") {
		t.Fatalf("client without extension error = %v", err)
	}

	oldMkdir := osMkdirAll
	osMkdirAll = func(string, uint32) error { return errors.New("mkdir failed") }
	if _, err := m.Start(recorder.CaptureOptions{ID: "mkdir", Mode: "client", Out: filepath.Join(home, "mkdir.borzrec")}); err == nil || !strings.Contains(err.Error(), "mkdir failed") {
		t.Fatalf("ensureParent error = %v", err)
	}
	osMkdirAll = oldMkdir

	if _, err := m.pickActive(""); err == nil || !strings.Contains(err.Error(), "when 0 recordings") {
		t.Fatalf("pickActive none error = %v", err)
	}
	if _, err := m.pickActive("missing"); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("pickActive missing error = %v", err)
	}
	m.active["a"] = &activeRecording{info: RecordingInfo{ID: "a", StartedAt: time.Now()}, done: make(chan struct{})}
	m.active["b"] = &activeRecording{info: RecordingInfo{ID: "b", StartedAt: time.Now().Add(time.Second)}, done: make(chan struct{})}
	if list := m.List(); len(list) != 2 || list[0].ID != "b" {
		t.Fatalf("List sorting = %+v", list)
	}
	if _, err := m.pickActive(""); err == nil || !strings.Contains(err.Error(), "when 2 recordings") {
		t.Fatalf("pickActive multiple error = %v", err)
	}
	if _, err := m.Pause("missing"); err == nil {
		t.Fatal("Pause should fail for missing active recording")
	}
	if _, err := m.Resume("missing"); err == nil {
		t.Fatal("Resume should fail for missing active recording")
	}

	active := m.active["a"]
	active.lastErr = "old"
	if info, err := m.Info("a"); err != nil || info.Error != "old" {
		t.Fatalf("Info active = %+v err=%v", info, err)
	}
	m.completed["done"] = RecordingInfo{ID: "done", Status: "stopped"}
	if info, err := m.Info("done"); err != nil || info.ID != "done" {
		t.Fatalf("Info completed = %+v err=%v", info, err)
	}
	if _, err := m.Info("missing"); err == nil {
		t.Fatal("Info should fail for missing path/default recording")
	}
}

func TestRecordingManagerRecoverAndUtilityBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	m := newRecordingManager(nil, nil)
	if _, err := m.Stop("missing", false); err == nil {
		t.Fatal("Stop missing should fail without recover")
	}
	if _, err := m.Stop("missing", true); err == nil {
		t.Fatal("Stop recover should fail when bundle does not exist")
	}

	dir := filepath.Join(home, "recordings", "recover.borzrec")
	writeDaemonRecordingBundle(t, dir, "recover")
	info, err := m.Stop("recover", true)
	if err != nil {
		t.Fatalf("Stop recover: %v", err)
	}
	if info.ID != "recover" || info.Manifest == nil || info.Manifest.Partial {
		t.Fatalf("recovered info = %+v", info)
	}

	start := time.Now().Add(-time.Second)
	x, y := 1.0, 2.0
	pe := pageEvent{Type: "click", Timestamp: float64(time.Now().UnixMilli()), X: &x, Y: &y, Button: "0", Key: "A", Text: "a", Redacted: true, Selector: "button", Cursor: "pointer", FocusRect: &recorder.Rect{X: 1, Y: 1, W: 2, H: 2}, Data: json.RawMessage(`{"ok":true}`)}
	ev := pe.toRecorder(start, "https://example.test")
	if ev.Timestamp <= 0 || ev.URL != "https://example.test" || ev.X == nil || ev.FocusRect == nil || len(ev.Data) == 0 {
		t.Fatalf("toRecorder after-start = %+v", ev)
	}
	pe.Timestamp = float64(start.Add(-time.Second).UnixMilli())
	if ev := pe.toRecorder(start, ""); ev.Timestamp <= 0 {
		t.Fatalf("toRecorder before-start should use relative now: %+v", ev)
	}

	ar := &activeRecording{info: RecordingInfo{FrameCount: 5, EventCount: 1, DurationNS: 10}}
	ar.setError(nil)
	ar.updateCounts(3, 2, 5)
	if ar.info.FrameCount != 5 || ar.info.EventCount != 3 || ar.info.DurationNS != 10 {
		t.Fatalf("updateCounts lower values = %+v", ar.info)
	}
	ar.updateCounts(8, 1, 20)
	if ar.info.FrameCount != 8 || ar.info.EventCount != 4 || ar.info.DurationNS != 20 {
		t.Fatalf("updateCounts higher values = %+v", ar.info)
	}
	ar.setError(errors.New("boom"))
	if ar.snapshotInfo().Error != "boom" {
		t.Fatalf("setError snapshot = %+v", ar.snapshotInfo())
	}
	ar.paused = true
	if !ar.isPaused() {
		t.Fatal("isPaused should report true")
	}
	if maxInt(1, 3) != 3 || maxInt(5, 2) != 5 {
		t.Fatal("maxInt mismatch")
	}
	script := maskScript([]string{".secret"}, true)
	for _, want := range []string{".secret", "textarea", "contenteditable"} {
		if !strings.Contains(script, want) {
			t.Fatalf("maskScript missing %q in %s", want, script)
		}
	}
	if !strings.Contains(eventTapScript([]string{".secret"}), ".secret") {
		t.Fatal("eventTapScript should include custom selector")
	}
}

func TestRecordingRoutesErrorBranches(t *testing.T) {
	s := newTestServer(t, "")
	s.recordings = newRecordingManager(s.cdp, s.extHub)
	mux := http.NewServeMux()
	s.registerRecordingRoutes(mux)

	recordingRouteCases := []struct {
		method string
		path   string
		body   string
		code   int
	}{
		{"PUT", "/v1/recordings", "", 405},
		{"POST", "/v1/recordings", "{bad", 400},
		{"POST", "/v1/recordings", `{"id":"client","mode":"client","out":"` + filepath.ToSlash(filepath.Join(t.TempDir(), "client.borzrec")) + `"}`, 400},
		{"GET", "/v1/recordings/", "", 404},
		{"POST", "/v1/recordings/missing/info", "", 405},
		{"GET", "/v1/recordings/missing/info", "", 404},
		{"GET", "/v1/recordings/missing/stop", "", 405},
		{"POST", "/v1/recordings/missing/stop", "", 400},
		{"POST", "/v1/recordings/missing/pause", "", 400},
		{"POST", "/v1/recordings/missing/resume", "", 400},
		{"GET", "/v1/recordings/missing/redact", "", 405},
		{"POST", "/v1/recordings/missing/redact", "{bad", 400},
		{"POST", "/v1/recordings/missing/redact", `{"x":1,"y":1,"w":1,"h":1}`, 404},
		{"GET", "/v1/recordings/missing/unknown", "", 404},
	}
	for _, tc := range recordingRouteCases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("%s %s got %d body=%s want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.code)
		}
	}

	preview := http.NewServeMux()
	s.registerRecordingPreviewRoutes(preview)
	for _, path := range []string{"/recordings/", "/recordings/missing"} {
		rec := httptest.NewRecorder()
		preview.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 404 {
			t.Fatalf("preview %s got %d", path, rec.Code)
		}
	}
	if got := htmlEscape(`<>&"`); got != "&lt;&gt;&amp;&#34;" {
		t.Fatalf("htmlEscape = %q", got)
	}
}

func writeDaemonRecordingBundle(t *testing.T, dir, id string) {
	t.Helper()
	w, err := recorder.Create(dir, recorder.CaptureOptions{ID: id, Mode: "cdp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFrame(0, daemonTestPNG(t), "png", recorder.Viewport{Width: 20, Height: 10, DPR: 1}, "", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
