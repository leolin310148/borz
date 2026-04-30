package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/recorder"
)

func TestRecordCLIHTTPCommands(t *testing.T) {
	out, _ := runMainWithFakeDaemon(t, "record", "list")
	if !strings.Contains(out, "rec-1") {
		t.Fatalf("record list output: %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "start", "--url", "https://example.test", "--mode", "cdp", "--out", "/tmp/x.borzrec", "--fps", "5")
	if !strings.Contains(out, "Recording started: rec-start") {
		t.Fatalf("record start output: %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "stop")
	if !strings.Contains(out, "Recording stopped: rec-1") {
		t.Fatalf("record stop output: %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "pause")
	if !strings.Contains(out, "Recording paused: rec-1") {
		t.Fatalf("record pause output: %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "resume")
	if !strings.Contains(out, "Recording resumed: rec-1") {
		t.Fatalf("record resume output: %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "info", "rec-1")
	if !strings.Contains(out, "Frames: 2") {
		t.Fatalf("record info output: %q", out)
	}
}

func TestRecordCLILocalBundleCommandsAndHelpers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "local.borzrec")
	w, err := recorder.Create(dir, recorder.CaptureOptions{ID: "local", Mode: "cdp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.AddFrame(0, cliTestPNG(t), "png", recorder.Viewport{Width: 10, Height: 10, DPR: 1}, "https://example.test", "T", 0, 0); err != nil {
		t.Fatalf("AddFrame: %v", err)
	}
	if err := w.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	out := captureStdout(t, func() { handleRecord([]string{"verify", dir}, []string{"record", "verify", dir}, false) })
	if !strings.Contains(out, "Bundle verified") {
		t.Fatalf("verify output: %q", out)
	}
	out = captureStdout(t, func() { handleRecord([]string{"info", dir}, []string{"record", "info", dir}, false) })
	if !strings.Contains(out, "Recording: local") {
		t.Fatalf("info output: %q", out)
	}
	out = captureStdout(t, func() {
		handleRecord([]string{"redact", dir}, []string{"record", "redact", dir, "--selector", ".secret"}, false)
	})
	if !strings.Contains(out, "Added selector redaction") {
		t.Fatalf("redact selector output: %q", out)
	}
	out = captureStdout(t, func() {
		handleRecord([]string{"redact", dir}, []string{"record", "redact", dir, "--rect", "1,2,3,4"}, false)
	})
	if !strings.Contains(out, "Added rectangle redaction") {
		t.Fatalf("redact rect output: %q", out)
	}
	exportPath := filepath.Join(t.TempDir(), "trace.json")
	handleRecord([]string{"export", dir}, []string{"record", "export", dir, "--format", "trace.json", "--out", exportPath}, false)
	if data, err := os.ReadFile(exportPath); err != nil || !strings.Contains(string(data), `"manifest"`) {
		t.Fatalf("export data=%q err=%v", data, err)
	}
	fakeFFmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("fake ffmpeg: %v", err)
	}
	out = captureStdout(t, func() {
		handleRecord([]string{"render", dir}, []string{"record", "render", dir, "--ffmpeg", fakeFFmpeg, "--out", filepath.Join(t.TempDir(), "out.mp4"), "--fps", "1", "--width", "20", "--height", "20"}, false)
	})
	if !strings.Contains(out, "Rendered:") {
		t.Fatalf("render output: %q", out)
	}

	if got := splitCSV("a, b,,c"); len(got) != 3 {
		t.Fatalf("splitCSV: %+v", got)
	}
	if !looksLikeBundle(dir) || looksLikeBundle("rec-1") {
		t.Fatal("looksLikeBundle mismatch")
	}
	if got := parseBytes("1.5MB"); got != 1572864 {
		t.Fatalf("parseBytes: %d", got)
	}
	if _, err := parseMask("1,2,3"); err == nil {
		t.Fatal("parseMask should reject short rect")
	}
}

func cliTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}
