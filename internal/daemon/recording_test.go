package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/daemon/extbridge"
	"github.com/leolin310148/borz/internal/recorder"
)

func TestRecordingManagerCDPLifecycle(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://example.test", "Example")
	pngData := daemonTestPNG(t)
	f.On("Page.captureScreenshot", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"data": base64.StdEncoding.EncodeToString(pngData)}, nil
	})
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "__borzRecorderInstalled") {
			payload := `{"events":[{"type":"pointerdown","x":2,"y":3,"button":"0"}],"width":20,"height":10,"dpr":1,"url":"https://example.test","title":"Example","scrollX":0,"scrollY":0}`
			return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": payload}}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": `{"readyState":"complete","href":"https://example.test"}`}}, nil
	})
	c := connectCdp(t, f)
	m := newRecordingManager(c, nil)

	info, err := m.Start(recorder.CaptureOptions{
		ID: "rec-cdp", Mode: "cdp", Tab: "T1", Out: filepath.Join(t.TempDir(), "rec-cdp.borzrec"), FPS: 20,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.Status != "recording" || info.ID != "rec-cdp" {
		t.Fatalf("start info: %+v", info)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := m.Pause("rec-cdp"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if _, err := m.Resume("rec-cdp"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	stopped, err := m.Stop("rec-cdp", false)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stopped.FrameCount == 0 || stopped.EventCount == 0 || stopped.Status != "stopped" {
		t.Fatalf("stopped info: %+v", stopped)
	}
	if len(m.List()) != 1 {
		t.Fatalf("completed list: %+v", m.List())
	}
	loaded, err := m.Info("rec-cdp")
	if err != nil {
		t.Fatalf("Info completed: %v", err)
	}
	if loaded.Manifest == nil || loaded.Manifest.FrameCount == 0 {
		t.Fatalf("loaded info missing manifest: %+v", loaded)
	}
	if _, err := recorder.Verify(stopped.Path); err != nil {
		t.Fatalf("Verify stopped bundle: %v", err)
	}
}

func TestRecordingRoutesListInfoRedactAndPreview(t *testing.T) {
	s := newTestServer(t, "")
	dir := filepath.Join(t.TempDir(), "route.borzrec")
	w, err := recorder.Create(dir, recorder.CaptureOptions{ID: "route", Mode: "cdp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.AddFrame(0, daemonTestPNG(t), "png", recorder.Viewport{Width: 20, Height: 10, DPR: 1}, "https://example.test", "T", 0, 0); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	s.recordings = newRecordingManager(s.cdp, s.extHub)
	s.recordings.completed["route"] = RecordingInfo{ID: "route", Path: dir, Status: "stopped", StartedAt: time.Now()}

	mux := http.NewServeMux()
	s.registerRecordingRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recordings", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "route") {
		t.Fatalf("list route: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recordings/route/info", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "frame_count") {
		t.Fatalf("info route: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"x":1,"y":2,"w":3,"h":4}`)
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/recordings/route/redact", body))
	if rec.Code != 200 {
		t.Fatalf("redact route: code=%d body=%s", rec.Code, rec.Body.String())
	}

	root := http.NewServeMux()
	s.registerRecordingPreviewRoutes(root)
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/recordings/route", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "borz recording") {
		t.Fatalf("preview: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRecordingManagerClientLifecycle(t *testing.T) {
	hub := extbridge.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial hub: %v", err)
	}
	defer conn.Close()
	done := make(chan struct{})
	pngData := daemonTestPNG(t)
	go func() {
		defer close(done)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Type   string          `json:"type"`
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			_ = json.Unmarshal(data, &msg)
			var result any = map[string]any{"ok": true}
			if msg.Method == "recording.captureVisible" {
				result = map[string]any{
					"ok": true, "dataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData),
					"width": 20, "height": 10, "dpr": 1, "url": "https://client.test", "title": "Client",
				}
			}
			raw, _ := json.Marshal(map[string]any{"type": "response", "id": msg.ID, "result": result})
			_ = conn.WriteMessage(websocket.TextMessage, raw)
			if msg.Method == "recording.start" {
				ev, _ := json.Marshal(map[string]any{"type": "pointermove", "x": 4, "y": 5})
				raw, _ := json.Marshal(map[string]any{"type": "event", "name": "recording.input", "data": json.RawMessage(ev)})
				_ = conn.WriteMessage(websocket.TextMessage, raw)
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	m := newRecordingManager(nil, hub)
	info, err := m.Start(recorder.CaptureOptions{ID: "rec-client", Mode: "client", Out: filepath.Join(t.TempDir(), "client.borzrec"), FPS: 20})
	if err != nil {
		t.Fatalf("Start client: %v", err)
	}
	if info.Status != "recording" {
		t.Fatalf("client info: %+v", info)
	}
	time.Sleep(80 * time.Millisecond)
	stopped, err := m.Stop("rec-client", false)
	if err != nil {
		t.Fatalf("Stop client: %v", err)
	}
	if stopped.FrameCount == 0 || stopped.EventCount == 0 {
		t.Fatalf("client stopped: %+v", stopped)
	}
}

func daemonTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, color.RGBA{200, 200, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}
