package main

import (
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/observability"
)

func TestParseLogSince(t *testing.T) {
	now := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	got, err := parseLogSince("7d", now)
	if err != nil || !got.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("7d = %v err=%v", got, err)
	}
	if _, err := parseLogSince("bad", now); err == nil {
		t.Fatal("invalid duration accepted")
	}
}

func TestCalculateLogStats(t *testing.T) {
	ok, failed := true, false
	entries := []observability.Entry{
		{Component: "daemon", Event: "command_completed", Action: "click", Success: &ok, DurationMS: 10},
		{Component: "daemon", Event: "command_completed", Action: "click", Success: &failed, ErrorCode: "stale_ref", DurationMS: 90},
		{Component: "mcp", Event: "tool_completed", Tool: "browser_click", Success: &failed, ErrorCode: "stale_ref", DurationMS: 100},
	}
	stats := calculateLogStats(entries, time.Time{})
	if stats.Commands != 2 || stats.CommandFailures != 1 || stats.Tools != 1 || stats.ToolFailures != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.ByError["daemon:stale_ref"] != 1 || stats.ByError["mcp:stale_ref"] != 1 || stats.CommandP95MS != 90 || stats.ToolP95MS != 100 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestHandleLogsPathTailAndStats(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	if err := config.SetProfile(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
	l, err := observability.Open("daemon", "test")
	if err != nil {
		t.Fatal(err)
	}
	ok := true
	_ = l.Log("info", "command_completed", observability.Fields{Action: "snapshot", Success: &ok})
	_ = l.Close()

	out := captureStdout(t, func() { handleLogs([]string{"path"}, nil, "", false) })
	if !strings.Contains(out, "logs/default") {
		t.Fatalf("path output = %q", out)
	}
	out = captureStdout(t, func() { handleLogs([]string{"tail"}, []string{"logs", "tail", "--lines", "1"}, "", false) })
	if !strings.Contains(out, "snapshot") {
		t.Fatalf("tail output = %q", out)
	}
	out = captureStdout(t, func() { handleLogs([]string{"stats"}, nil, "7d", false) })
	if !strings.Contains(out, "Commands: 1") {
		t.Fatalf("stats output = %q", out)
	}
}
