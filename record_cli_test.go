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
	if got, err := parseBytes("1.5MB"); err != nil || got != 1572864 {
		t.Fatalf("parseBytes: got %d err=%v", got, err)
	}
	if got := parsePositiveFloatFlag("--dpr", " 1.5 "); got != 1.5 {
		t.Fatalf("parsePositiveFloatFlag = %v, want 1.5", got)
	}
	if _, err := parseBytes("9223372036854775808B"); err == nil {
		t.Fatal("parseBytes should reject values larger than int64")
	}
	if _, err := parseMask("1,2,3"); err == nil {
		t.Fatal("parseMask should reject short rect")
	}
	if _, err := parseMask("1,2,0,4"); err == nil {
		t.Fatal("parseMask should reject non-positive width")
	}
	if _, err := parseMask("1,2,3,-4"); err == nil {
		t.Fatal("parseMask should reject non-positive height")
	}
	for _, bad := range []string{"NaN,2,3,4", "1,+Inf,3,4", "1,2,3,-Inf"} {
		if _, err := parseMask(bad); err == nil {
			t.Fatalf("parseMask should reject non-finite rect %q", bad)
		}
	}
}

func TestRecordStartRejectsInvalidDPR(t *testing.T) {
	for _, dpr := range []string{"bad", "0", "-1", "NaN", "+Inf", "-Inf"} {
		t.Run(dpr, func(t *testing.T) {
			expectExit(t, 1, func() {
				handleRecord([]string{"start"}, []string{"record", "start", "--dpr", dpr}, false)
			})
		})
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
