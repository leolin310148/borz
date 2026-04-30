package recorder

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundleLifecycleVerifyExportAndRedactions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow.borzrec")
	w, err := Create(dir, CaptureOptions{
		ID: "rec-test", Mode: "cdp", URL: "https://example.test",
		FPS: 12, MaskSelectors: []string{".secret"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	img1 := testPNG(t, color.RGBA{255, 0, 0, 255})
	img2 := testPNG(t, color.RGBA{0, 255, 0, 255})
	if _, err := w.AddFrame(0, img1, "png", Viewport{Width: 8, Height: 6, DPR: 1}, "https://example.test/a", "A", 0, 0); err != nil {
		t.Fatalf("AddFrame 1: %v", err)
	}
	if _, err := w.AddFrame(int64(500*time.Millisecond), img2, "png", Viewport{Width: 10, Height: 6, DPR: 2}, "https://example.test/b", "B", 1, 2); err != nil {
		t.Fatalf("AddFrame 2: %v", err)
	}
	x, y := 3.0, 4.0
	if err := w.AddEvent(Event{Timestamp: int64(100 * time.Millisecond), Type: "pointerdown", X: &x, Y: &y, Button: "0"}); err != nil {
		t.Fatalf("AddEvent pointer: %v", err)
	}
	if err := w.AddEvent(Event{Timestamp: int64(200 * time.Millisecond), Type: "keydown", Key: "s", Text: "s", Selector: "input[type=password]"}); err != nil {
		t.Fatalf("AddEvent secret: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	b, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if b.Manifest.FrameCount != 2 || b.Manifest.EventCount != 2 || len(b.Manifest.Scenes) != 2 {
		t.Fatalf("manifest counts/scenes: %+v", b.Manifest)
	}
	if got := b.Events[1].Text; got != "<redacted>" || !b.Events[1].Redacted {
		t.Fatalf("secret event not redacted: %+v", b.Events[1])
	}

	if err := AddSelectorRedaction(dir, ".token"); err != nil {
		t.Fatalf("AddSelectorRedaction: %v", err)
	}
	if err := AddRedaction(dir, RedactionMask{X: 1, Y: 2, W: 3, H: 4}); err != nil {
		t.Fatalf("AddRedaction: %v", err)
	}
	b, err = Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(b.Redaction.Selectors) != 2 || len(b.Redaction.Masks) != 1 {
		t.Fatalf("redactions: %+v", b.Redaction)
	}

	out := filepath.Join(t.TempDir(), "trace.json")
	if err := ExportTrace(dir, out); err != nil {
		t.Fatalf("ExportTrace: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), `"manifest"`) || !strings.Contains(string(data), `"events"`) {
		t.Fatalf("export missing sections: %s", data)
	}
}

func TestDecodeDataURLAndPrivacyRedaction(t *testing.T) {
	raw := []byte("hello")
	data, ext, err := DecodeDataURL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw))
	if err != nil || ext != "jpg" || string(data) != "hello" {
		t.Fatalf("DecodeDataURL = %q %s %v", data, ext, err)
	}
	if _, _, err := DecodeDataURL("not-a-data-url"); err == nil {
		t.Fatal("DecodeDataURL should reject invalid input")
	}
	ev := RedactEvent(Event{Type: "network.request", Data: json.RawMessage(`{"headers":{"Authorization":"bearer x","ok":"1"},"token":"abc"}`)})
	if strings.Contains(string(ev.Data), "bearer x") || strings.Contains(string(ev.Data), "abc") {
		t.Fatalf("network secret leaked: %s", ev.Data)
	}
	if err := checkSchema("2.0"); err == nil {
		t.Fatal("unknown major schema should fail")
	}
}

func TestRenderWithFakeFFmpegAndCompositor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow.borzrec")
	w, err := Create(dir, CaptureOptions{ID: "rec-render", Mode: "cdp", FPS: 2})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	img := testPNG(t, color.RGBA{240, 240, 240, 255})
	if _, err := w.AddFrame(0, img, "png", Viewport{Width: 20, Height: 10, DPR: 1}, "https://example.test", "T", 0, 0); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	x, y := 5.0, 5.0
	if err := w.AddEvent(Event{Timestamp: 0, Type: "pointerdown", X: &x, Y: &y}); err != nil {
		t.Fatalf("AddEvent pointer: %v", err)
	}
	if err := w.AddEvent(Event{Timestamp: int64(10 * time.Millisecond), Type: "keydown", Key: "A", Text: "A"}); err != nil {
		t.Fatalf("AddEvent key: %v", err)
	}
	if err := w.AddEvent(Event{Timestamp: int64(20 * time.Millisecond), Type: "focus", Selector: "input#secret", FocusRect: &Rect{X: 2, Y: 2, W: 4, H: 4}}); err != nil {
		t.Fatalf("AddEvent focus: %v", err)
	}
	if err := AddRedaction(dir, RedactionMask{X: 1, Y: 1, W: 3, H: 3}); err != nil {
		t.Fatalf("AddRedaction: %v", err)
	}
	if err := AddSelectorRedaction(dir, "#secret"); err != nil {
		t.Fatalf("AddSelectorRedaction: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	if err := Render(dir, RenderOptions{
		FFmpegPath: ffmpeg, Out: filepath.Join(t.TempDir(), "out.mp4"),
		Preset: "share", FPS: 2, Width: 40, Height: 20, Trim: "0:00-0:01",
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !selectorMatchesAny("input#secret", []string{"#secret"}) {
		t.Fatal("selectorMatchesAny should match suffix")
	}
}

func testPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return buf.Bytes()
}
