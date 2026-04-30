package recorder

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundleErrorAndEdgeBranches(t *testing.T) {
	if _, err := Create("", CaptureOptions{}); err == nil {
		t.Fatal("Create should reject an empty bundle path")
	}
	if id := NewID(); !strings.HasPrefix(id, "rec-") || len(id) < len("rec-20060102-150405-00000000") {
		t.Fatalf("NewID format = %q", id)
	}

	dir := filepath.Join(t.TempDir(), "edges.borzrec")
	w, err := Create(dir, CaptureOptions{})
	if err != nil {
		t.Fatalf("Create defaults: %v", err)
	}
	if w.manifest.ID == "" || w.manifest.CaptureMode != "cdp" || w.manifest.Options.FPS != 10 {
		t.Fatalf("defaults not applied: %+v", w.manifest)
	}
	if _, err := w.AddFrame(0, nil, "png", Viewport{}, "", "", 0, 0); err == nil {
		t.Fatal("AddFrame should reject empty data")
	}
	if _, err := w.AddFrame(0, []byte("not-an-image"), "gif", Viewport{}, "", "", 0, 0); err == nil {
		t.Fatal("AddFrame should reject unsupported extensions")
	}
	jpegLikePNG := testPNG(t, color.RGBA{1, 2, 3, 255})
	fr, err := w.AddFrame(10, jpegLikePNG, ".jpeg", Viewport{}, "https://a.test", "A", 0, 0)
	if err != nil {
		t.Fatalf("AddFrame jpeg alias: %v", err)
	}
	if fr.Path != filepath.ToSlash(filepath.Join(framesDir, "000001.jpg")) || fr.Width == 0 || fr.Height == 0 || fr.DPR != 1 {
		t.Fatalf("frame defaults/extension = %+v", fr)
	}
	fr, err = w.AddFrame(20, jpegLikePNG, "png", Viewport{Width: 20, Height: 10, DPR: 1}, "https://a.test", "A", 0, 0)
	if err != nil {
		t.Fatalf("AddFrame same scene: %v", err)
	}
	if fr.SceneID != 1 {
		t.Fatalf("same scene should reuse id, got %+v", fr)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.AddFrame(30, jpegLikePNG, "png", Viewport{}, "", "", 0, 0); err == nil {
		t.Fatal("closed writer should reject frames")
	}
	if err := w.AddEvent(Event{Timestamp: 30, Type: "click"}); err == nil {
		t.Fatal("closed writer should reject events")
	}
}

func TestBundleVerifyFailureBranches(t *testing.T) {
	t.Run("open failures", func(t *testing.T) {
		if _, err := Open(filepath.Join(t.TempDir(), "missing.borzrec")); err == nil {
			t.Fatal("Open should fail without manifest")
		}
		dir := writeMinimalBundle(t)
		writeJSONForTest(t, filepath.Join(dir, "manifest.json"), Manifest{SchemaVersion: "2.0"})
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("schema error = %v", err)
		}
		writeJSONForTest(t, filepath.Join(dir, "manifest.json"), Manifest{SchemaVersion: "1"})
		if err := os.Remove(filepath.Join(dir, "frames.cbor")); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("Open should fail when frames index is missing")
		}
	})

	cases := []struct {
		name   string
		mutate func(string)
		want   string
	}{
		{
			name: "frame count mismatch",
			mutate: func(dir string) {
				var m Manifest
				mustReadJSONForTest(t, filepath.Join(dir, "manifest.json"), &m)
				m.FrameCount = 2
				writeJSONForTest(t, filepath.Join(dir, "manifest.json"), m)
			},
			want: "frame_count",
		},
		{
			name: "event count mismatch",
			mutate: func(dir string) {
				var m Manifest
				mustReadJSONForTest(t, filepath.Join(dir, "manifest.json"), &m)
				m.EventCount = 1
				writeJSONForTest(t, filepath.Join(dir, "manifest.json"), m)
			},
			want: "event_count",
		},
		{
			name: "frame sequence",
			mutate: func(dir string) {
				rewriteFramesForTest(t, dir, func(fr *FrameRecord) { fr.Seq = 9 })
			},
			want: "frame seq",
		},
		{
			name: "frame timestamp",
			mutate: func(dir string) {
				var fr FrameRecord
				mustReadFirstLineForTest(t, filepath.Join(dir, "frames.cbor"), &fr)
				fr.Seq = 2
				fr.Timestamp = -1
				f, err := os.OpenFile(filepath.Join(dir, "frames.cbor"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := appendJSONLine(f, fr); err != nil {
					t.Fatal(err)
				}
				_ = f.Close()
				var m Manifest
				mustReadJSONForTest(t, filepath.Join(dir, "manifest.json"), &m)
				m.FrameCount = 2
				writeJSONForTest(t, filepath.Join(dir, "manifest.json"), m)
			},
			want: "timestamps",
		},
		{
			name: "missing frame file",
			mutate: func(dir string) {
				rewriteFramesForTest(t, dir, func(fr *FrameRecord) { fr.Path = "frames/missing.png" })
			},
			want: "no such file",
		},
		{
			name: "checksum mismatch",
			mutate: func(dir string) {
				rewriteFramesForTest(t, dir, func(fr *FrameRecord) { fr.SHA256 = strings.Repeat("0", 64) })
			},
			want: "checksum",
		},
		{
			name: "event sequence",
			mutate: func(dir string) {
				appendEventForVerifyTest(t, dir, Event{Seq: 7, Timestamp: 1, Type: "click"})
			},
			want: "event seq",
		},
		{
			name: "event timestamp",
			mutate: func(dir string) {
				appendEventForVerifyTest(t, dir, Event{Seq: 1, Timestamp: 10, Type: "a"}, Event{Seq: 2, Timestamp: 1, Type: "b"})
			},
			want: "event timestamps",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeMinimalBundle(t)
			tc.mutate(dir)
			if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Verify error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRenderHelperBranches(t *testing.T) {
	b := &Bundle{Frames: []FrameRecord{{Width: 100, Height: 50}}}
	opts := fillRenderDefaults(b, RenderOptions{Preset: "loop", Width: 200, Height: 0})
	if opts.Height != 100 || opts.Format != "webp" || opts.FPS != 24 {
		t.Fatalf("loop defaults = %+v", opts)
	}
	opts = fillRenderDefaults(b, RenderOptions{Preset: "archive", Width: 0, Height: 25, FPS: 12, Format: "gif", Annotations: []string{"cursor"}})
	if opts.Width != 50 || opts.Height != 25 || opts.Format != "gif" || opts.FPS != 12 || len(opts.Annotations) != 1 {
		t.Fatalf("height-derived defaults = %+v", opts)
	}
	if got := targetFrameCount(10, 5, 30); got != 1 {
		t.Fatalf("targetFrameCount invalid = %d", got)
	}
	start, end := parseTrim("", 123)
	if start != 0 || end != 123 {
		t.Fatalf("parseTrim empty = %d %d", start, end)
	}
	start, end = parseTrim("bad", 123)
	if start != 0 || end != 123 {
		t.Fatalf("parseTrim bad = %d %d", start, end)
	}
	start, end = parseTrim("0:02-0:01", 5_000_000_000)
	if start != 2_000_000_000 || end != 5_000_000_000 {
		t.Fatalf("parseTrim reversed = %d %d", start, end)
	}
	if annotationEnabled([]string{" keys "}, "cursor") {
		t.Fatal("unexpected annotation match")
	}
	if _, ok := latestPointer([]Event{{Timestamp: 1, Type: "keydown"}}, 2); ok {
		t.Fatal("latestPointer should ignore keys")
	}
	if frameAt([]FrameRecord{{Seq: 1, Timestamp: 10}}, 0).Seq != 1 {
		t.Fatal("frameAt before first should return first")
	}
	if min(3, 1) != 1 {
		t.Fatal("min branch mismatch")
	}
}

func TestRenderDrawingAndFFmpegBranches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 30, 30))
	opts := RenderOptions{Width: 30, Height: 30}
	fr := FrameRecord{Width: 30, Height: 30, SceneID: 2}
	drawRedactions(img, []RedactionMask{
		{SceneID: 1, X: 1, Y: 1, W: 5, H: 5},
		{SceneID: 2, StartNS: 10, X: 1, Y: 1, W: 5, H: 5},
		{SceneID: 2, EndNS: 1, X: 1, Y: 1, W: 5, H: 5},
		{SceneID: 2, X: 2, Y: 2, W: 5, H: 5},
	}, 2, 5, opts, fr)
	if _, _, _, a := img.At(3, 3).RGBA(); a == 0 {
		t.Fatal("redaction did not draw")
	}
	drawSelectorRedactions(img, nil, nil, 0, opts, fr)
	drawSelectorRedactions(img, []string{".secret"}, []Event{
		{Timestamp: 10, Selector: ".secret", FocusRect: &Rect{X: 1, Y: 1, W: 3, H: 3}},
	}, 1, opts, fr)
	drawSelectorRedactions(img, []string{".secret"}, []Event{
		{Timestamp: 0, Selector: ".other", FocusRect: &Rect{X: 1, Y: 1, W: 3, H: 3}},
		{Timestamp: 0, Selector: "input.secret", FocusRect: &Rect{X: 4, Y: 4, W: 3, H: 3}},
	}, 1, opts, fr)
	if selectorMatchesAny("", []string{".secret"}) || selectorMatchesAny("input", []string{" "}) {
		t.Fatal("empty selector matching should be false")
	}
	if _, err := loadFrame(t.TempDir(), FrameRecord{Path: "missing.png"}); err == nil {
		t.Fatal("loadFrame should fail on missing file")
	}
	if err := writePNG(filepath.Join(t.TempDir(), "missing", "x.png"), img); err == nil {
		t.Fatal("writePNG should fail when parent is missing")
	}
	if _, err := (RenderOptions{Format: "gif"}).MarshalJSON(); err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	temp := t.TempDir()
	ffmpeg := filepath.Join(temp, "ffmpeg")
	logPath := filepath.Join(temp, "args.log")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$FFMPEG_LOG\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FFMPEG_LOG", logPath)
	for _, format := range []string{"gif", "webp", "apng", "mov", "webm", "mp4"} {
		if err := runFFmpeg(ffmpeg, temp, RenderOptions{FPS: 3, Format: format, Out: filepath.Join(temp, "out."+format)}); err != nil {
			t.Fatalf("runFFmpeg %s: %v", format, err)
		}
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"palettegen", "libwebp", "apng", "prores_ks", "libvpx-vp9", "libx264"} {
		if !bytes.Contains(logData, []byte(want)) {
			t.Fatalf("ffmpeg args missing %q in\n%s", want, logData)
		}
	}
	bad := filepath.Join(temp, "bad-ffmpeg")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho nope\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runFFmpeg(bad, temp, RenderOptions{FPS: 1, Format: "mp4", Out: filepath.Join(temp, "bad.mp4")}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("runFFmpeg error = %v", err)
	}
}

func TestRenderFailureBranches(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty.borzrec")
	w, err := Create(dir, CaptureOptions{ID: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := Render(dir, RenderOptions{FFmpegPath: "ffmpeg"}); err == nil || !strings.Contains(err.Error(), "no frames") {
		t.Fatalf("Render empty error = %v", err)
	}

	dir = writeMinimalBundle(t)
	if err := Render(dir, RenderOptions{FFmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg")}); err == nil {
		t.Fatal("Render should report ffmpeg execution failure")
	}
}

func writeMinimalBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "min.borzrec")
	w, err := Create(dir, CaptureOptions{ID: "min", Mode: "cdp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFrame(int64(time.Millisecond), testPNG(t, color.RGBA{9, 9, 9, 255}), "png", Viewport{Width: 20, Height: 10, DPR: 1}, "https://min.test", "Min", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeJSONForTest(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadJSONForTest(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

func mustReadFirstLineForTest[T any](t *testing.T, path string, out *T) {
	t.Helper()
	lines, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(lines), "\n")
	if err := json.Unmarshal([]byte(line), out); err != nil {
		t.Fatal(err)
	}
}

func rewriteFramesForTest(t *testing.T, dir string, mutate func(*FrameRecord)) {
	t.Helper()
	var fr FrameRecord
	mustReadFirstLineForTest(t, filepath.Join(dir, "frames.cbor"), &fr)
	mutate(&fr)
	f, err := os.Create(filepath.Join(dir, "frames.cbor"))
	if err != nil {
		t.Fatal(err)
	}
	if err := appendJSONLine(f, fr); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendEventForVerifyTest(t *testing.T, dir string, events ...Event) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, "events.cbor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if err := appendJSONLine(f, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var m Manifest
	mustReadJSONForTest(t, filepath.Join(dir, "manifest.json"), &m)
	m.EventCount = len(events)
	writeJSONForTest(t, filepath.Join(dir, "manifest.json"), m)
}
