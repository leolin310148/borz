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
	second := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	entries := []observability.Entry{
		{Time: second, Component: "daemon", Event: "command_completed", Action: "click", Success: &ok, DurationMS: 10},
		{Time: second, Component: "daemon", Event: "command_completed", Action: "click", Success: &failed, ErrorCode: "stale_ref", DurationMS: 90},
		{Component: "mcp", Event: "tool_completed", Tool: "browser_click", Success: &failed, ErrorCode: "stale_ref", DurationMS: 100},
	}
	for i := 0; i < 5; i++ {
		entries = append(entries, observability.Entry{
			Time: second, Component: "daemon", Event: "command_completed", Action: "tab_list",
			SessionID: "tmux-7", Success: &ok, DurationMS: int64(i),
		})
	}
	stats := calculateLogStats(entries, time.Time{})
	if stats.Commands != 7 || stats.CommandFailures != 1 || stats.Tools != 1 || stats.ToolFailures != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.ByError["daemon:stale_ref"] != 1 || stats.ByError["mcp:stale_ref"] != 1 || stats.CommandP95MS != 90 || stats.ToolP95MS != 100 {
		t.Fatalf("stats = %+v", stats)
	}
	click := stats.ActionStats["click"]
	if click.Count != 2 || click.Failures != 1 || click.P50MS != 10 || click.P95MS != 90 || click.MaxMS != 90 {
		t.Fatalf("click stats = %+v", click)
	}
	if len(stats.Bursts) != 1 || stats.Bursts[0].Name != "tab_list" || stats.Bursts[0].Count != 5 || stats.Bursts[0].SessionID != "tmux-7" {
		t.Fatalf("bursts = %+v", stats.Bursts)
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
	if !strings.Contains(out, "Commands: 1") || !strings.Contains(out, "Action performance") || !strings.Contains(out, "snapshot") {
		t.Fatalf("stats output = %q", out)
	}
}
