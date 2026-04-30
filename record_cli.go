package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/recorder"
)

type recordInfo struct {
	ID         string             `json:"id"`
	Path       string             `json:"path"`
	Mode       string             `json:"mode"`
	URL        string             `json:"url"`
	Status     string             `json:"status"`
	FrameCount int                `json:"frame_count"`
	EventCount int                `json:"event_count"`
	DurationNS int64              `json:"duration_ns"`
	Manifest   *recorder.Manifest `json:"manifest,omitempty"`
	Error      string             `json:"error,omitempty"`
}

func handleRecord(cmdArgs []string, rawArgs []string, jsonOutput bool) {
	sub := "list"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "start":
		opts := recorder.CaptureOptions{
			ID:            getArgValue(rawArgs, "--id"),
			Mode:          firstNonEmpty(getArgValue(rawArgs, "--mode"), "cdp"),
			URL:           getArgValue(rawArgs, "--url"),
			Out:           getArgValue(rawArgs, "--out"),
			Viewport:      getArgValue(rawArgs, "--viewport"),
			MaskSelectors: splitCSV(getArgValue(rawArgs, "--mask-selectors")),
			MaskByDefault: hasFlag(rawArgs, "--mask-by-default"),
			Lossless:      hasFlag(rawArgs, "--lossless"),
			Audio:         splitCSV(getArgValue(rawArgs, "--audio")),
		}
		if opts.URL == "" && len(cmdArgs) > 1 && strings.Contains(cmdArgs[1], "://") {
			opts.URL = cmdArgs[1]
		}
		if tab := getArgValue(rawArgs, "--tab"); tab != "" {
			opts.Tab = tab
		}
		if fps := getArgValue(rawArgs, "--fps"); fps != "" {
			if n, err := strconv.Atoi(fps); err == nil {
				opts.FPS = n
			}
		}
		if dpr := getArgValue(rawArgs, "--dpr"); dpr != "" {
			if n, err := strconv.ParseFloat(dpr, 64); err == nil {
				opts.DPR = n
			}
		}
		if maxSize := getArgValue(rawArgs, "--max-size"); maxSize != "" {
			opts.MaxSizeBytes = parseBytes(maxSize)
		}
		raw := postRecordJSON("/v1/recordings", opts, 2*time.Minute)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var info recordInfo
		_ = json.Unmarshal(raw, &info)
		fmt.Printf("Recording started: %s\nBundle: %s\n", info.ID, info.Path)

	case "stop":
		id := "current"
		if len(cmdArgs) > 1 {
			id = cmdArgs[1]
		}
		q := ""
		if hasFlag(rawArgs, "--recover") {
			q = "?recover=true"
		}
		raw := postRecordJSON("/v1/recordings/"+url.PathEscape(id)+"/stop"+q, nil, 2*time.Minute)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var info recordInfo
		_ = json.Unmarshal(raw, &info)
		fmt.Printf("Recording stopped: %s\nFrames: %d  Events: %d\nBundle: %s\n", info.ID, info.FrameCount, info.EventCount, info.Path)

	case "pause", "resume":
		id := "current"
		if len(cmdArgs) > 1 {
			id = cmdArgs[1]
		}
		raw := postRecordJSON("/v1/recordings/"+url.PathEscape(id)+"/"+sub, nil, 30*time.Second)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var info recordInfo
		_ = json.Unmarshal(raw, &info)
		fmt.Printf("Recording %s: %s\n", sub+"d", info.ID)

	case "list":
		raw := getRecordJSON("/v1/recordings", 30*time.Second)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var resp struct {
			Recordings []recordInfo `json:"recordings"`
		}
		_ = json.Unmarshal(raw, &resp)
		if len(resp.Recordings) == 0 {
			fmt.Println("No recordings")
			return
		}
		for _, r := range resp.Recordings {
			fmt.Printf("%s  %-9s  frames=%d events=%d  %s\n", r.ID, r.Status, r.FrameCount, r.EventCount, r.Path)
		}

	case "info", "verify":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz record " + sub + " <bundle|recording-id>")
		}
		if looksLikeBundle(cmdArgs[1]) {
			b, err := recorder.Verify(cmdArgs[1])
			if err != nil {
				fatal(err.Error())
			}
			if jsonOutput {
				printJSON(b.Manifest)
				return
			}
			if sub == "verify" {
				fmt.Printf("Bundle verified: %s\nFrames: %d  Events: %d\n", cmdArgs[1], b.Manifest.FrameCount, b.Manifest.EventCount)
			} else {
				fmt.Printf("Recording: %s\nMode: %s\nFrames: %d\nEvents: %d\nDuration: %.2fs\n", b.Manifest.ID, b.Manifest.CaptureMode, b.Manifest.FrameCount, b.Manifest.EventCount, float64(b.Manifest.DurationNS)/1e9)
			}
			return
		}
		raw := getRecordJSON("/v1/recordings/"+url.PathEscape(cmdArgs[1])+"/info", 30*time.Second)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var info recordInfo
		_ = json.Unmarshal(raw, &info)
		fmt.Printf("Recording: %s\nStatus: %s\nFrames: %d\nEvents: %d\nBundle: %s\n", info.ID, info.Status, info.FrameCount, info.EventCount, info.Path)

	case "render":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz record render <bundle> [--preset share] [--out demo.mp4]")
		}
		opts := recorder.RenderOptions{
			Preset:      firstNonEmpty(getArgValue(rawArgs, "--preset"), "share"),
			Out:         getArgValue(rawArgs, "--out"),
			Format:      getArgValue(rawArgs, "--format"),
			Annotations: splitCSV(getArgValue(rawArgs, "--annotations")),
			Trim:        getArgValue(rawArgs, "--trim"),
			Speed:       getArgValue(rawArgs, "--speed"),
			Watermark:   getArgValue(rawArgs, "--watermark"),
			FFmpegPath:  getArgValue(rawArgs, "--ffmpeg"),
			Smooth:      hasFlag(rawArgs, "--smooth"),
			Chapters:    getArgValue(rawArgs, "--chapters"),
		}
		if v := getArgValue(rawArgs, "--fps"); v != "" {
			opts.FPS, _ = strconv.Atoi(v)
		}
		if v := getArgValue(rawArgs, "--width"); v != "" {
			opts.Width, _ = strconv.Atoi(v)
		}
		if v := getArgValue(rawArgs, "--height"); v != "" {
			opts.Height, _ = strconv.Atoi(v)
		}
		if err := recorder.Render(cmdArgs[1], opts); err != nil {
			fatal(err.Error())
		}
		if opts.Out == "" {
			opts.Out = "out." + firstNonEmpty(opts.Format, "mp4")
		}
		if jsonOutput {
			printJSON(map[string]any{"ok": true, "out": opts.Out})
			return
		}
		fmt.Printf("Rendered: %s\n", opts.Out)

	case "export":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz record export <bundle> --format trace.json [--out trace.json]")
		}
		format := firstNonEmpty(getArgValue(rawArgs, "--format"), "trace.json")
		if format != "trace.json" && format != "json" {
			fatal("record export currently supports --format trace.json")
		}
		if err := recorder.ExportTrace(cmdArgs[1], getArgValue(rawArgs, "--out")); err != nil {
			fatal(err.Error())
		}

	case "redact":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz record redact <bundle> --selector <css> | --rect x,y,w,h")
		}
		if selector := getArgValue(rawArgs, "--selector"); selector != "" {
			if err := recorder.AddSelectorRedaction(cmdArgs[1], selector); err != nil {
				fatal(err.Error())
			}
			fmt.Printf("Added selector redaction: %s\n", selector)
			return
		}
		rect := getArgValue(rawArgs, "--rect")
		if rect == "" {
			fatal("record redact requires --selector or --rect x,y,w,h")
		}
		mask, err := parseMask(rect)
		if err != nil {
			fatal(err.Error())
		}
		if err := recorder.AddRedaction(cmdArgs[1], mask); err != nil {
			fatal(err.Error())
		}
		fmt.Println("Added rectangle redaction")

	case "edit", "play":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz record " + sub + " <bundle|recording-id>")
		}
		if looksLikeBundle(cmdArgs[1]) {
			b, err := recorder.Verify(cmdArgs[1])
			if err != nil {
				fatal(err.Error())
			}
			fmt.Printf("Bundle: %s\nRecording: %s\nFrames: %d  Events: %d\n", cmdArgs[1], b.Manifest.ID, b.Manifest.FrameCount, b.Manifest.EventCount)
			return
		}
		raw := getRecordJSON("/v1/recordings/"+url.PathEscape(cmdArgs[1])+"/info", 30*time.Second)
		var info recordInfo
		_ = json.Unmarshal(raw, &info)
		fmt.Printf("Preview: http://127.0.0.1:19824/recordings/%s\nBundle: %s\n", info.ID, info.Path)

	default:
		fatal(unknownSubcommandHint("record", sub))
	}
}

func getRecordJSON(path string, timeout time.Duration) json.RawMessage {
	raw, err := client.GetJSON(path, timeout)
	if err != nil {
		fatal(err.Error())
	}
	return raw
}

func postRecordJSON(path string, body any, timeout time.Duration) json.RawMessage {
	raw, err := client.PostJSON(path, body, timeout)
	if err != nil {
		fatal(err.Error())
	}
	return raw
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func looksLikeBundle(s string) bool {
	return strings.HasSuffix(s, ".borzrec") || strings.Contains(s, string(os.PathSeparator))
}

func parseBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	for _, suffix := range []struct {
		s string
		m int64
	}{{"GB", 1 << 30}, {"G", 1 << 30}, {"MB", 1 << 20}, {"M", 1 << 20}, {"KB", 1 << 10}, {"K", 1 << 10}} {
		if strings.HasSuffix(s, suffix.s) {
			mult = suffix.m
			s = strings.TrimSuffix(s, suffix.s)
			break
		}
	}
	n, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return int64(n * float64(mult))
}

func parseMask(s string) (recorder.RedactionMask, error) {
	parts := splitCSV(s)
	if len(parts) != 4 {
		return recorder.RedactionMask{}, fmt.Errorf("--rect must be x,y,w,h")
	}
	vals := make([]float64, 4)
	for i, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return recorder.RedactionMask{}, err
		}
		vals[i] = v
	}
	return recorder.RedactionMask{X: vals[0], Y: vals[1], W: vals[2], H: vals[3]}, nil
}
