package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

func setupFeedbackHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	if err := config.SetProfile(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
	return home
}

func TestHandleFeedbackAddListPath(t *testing.T) {
	home := setupFeedbackHome(t)

	out := captureStdout(t, func() {
		handleFeedback(
			[]string{"snapshot", "too", "verbose", "--category", "ux", "--command", "snapshot"},
			[]string{"feedback", "snapshot", "too", "verbose", "--category", "ux", "--command", "snapshot"},
			false)
	})
	if !strings.Contains(out, "Feedback saved to") || !strings.Contains(out, "feedback.jsonl") {
		t.Fatalf("add output = %q", out)
	}

	data, err := os.ReadFile(filepath.Join(home, feedbackFileName))
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	for _, want := range []string{`"message":"snapshot too verbose"`, `"category":"ux"`, `"command":"snapshot"`} {
		if !strings.Contains(line, want) {
			t.Fatalf("feedback file missing %q; got %q", want, line)
		}
	}

	out = captureStdout(t, func() { handleFeedback([]string{"list"}, []string{"feedback", "list"}, false) })
	if !strings.Contains(out, "snapshot too verbose") || !strings.Contains(out, "[ux cmd=snapshot") {
		t.Fatalf("list output = %q", out)
	}

	out = captureStdout(t, func() { handleFeedback([]string{"path"}, nil, false) })
	if !strings.Contains(out, filepath.Join(home, feedbackFileName)) {
		t.Fatalf("path output = %q", out)
	}
	out = captureStdout(t, func() { handleFeedback([]string{"path"}, nil, true) })
	if !strings.Contains(out, `"path"`) {
		t.Fatalf("path --json output = %q", out)
	}
}

func TestHandleFeedbackExplicitAddAndJSON(t *testing.T) {
	setupFeedbackHome(t)

	out := captureStdout(t, func() {
		handleFeedback([]string{"add", "list", "output", "misaligned"}, []string{"feedback", "add", "list", "output", "misaligned"}, true)
	})
	for _, want := range []string{`"saved": true`, `"message": "list output misaligned"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("add --json output missing %q; got %q", want, out)
		}
	}

	out = captureStdout(t, func() { handleFeedback([]string{"list"}, []string{"feedback", "list"}, true) })
	if !strings.Contains(out, `"list output misaligned"`) {
		t.Fatalf("list --json output = %q", out)
	}
}

func TestHandleFeedbackListLimitAndEmpty(t *testing.T) {
	setupFeedbackHome(t)

	out := captureStdout(t, func() { handleFeedback([]string{"list"}, nil, false) })
	if !strings.Contains(out, "No feedback recorded yet") {
		t.Fatalf("empty list output = %q", out)
	}

	for _, msg := range []string{"first", "second", "third"} {
		captureStdout(t, func() { handleFeedback([]string{msg}, []string{"feedback", msg}, false) })
	}
	out = captureStdout(t, func() {
		handleFeedback([]string{"list", "--limit", "2"}, []string{"feedback", "list", "--limit", "2"}, false)
	})
	if strings.Contains(out, "first") || !strings.Contains(out, "second") || !strings.Contains(out, "third") {
		t.Fatalf("limited list output = %q", out)
	}
}

func TestHandleFeedbackErrors(t *testing.T) {
	setupFeedbackHome(t)

	for _, tc := range []struct {
		name    string
		cmdArgs []string
		rawArgs []string
	}{
		{"no args", nil, nil},
		{"flags only", []string{"--category", "ux"}, []string{"feedback", "--category", "ux"}},
		{"bad limit", []string{"list", "--limit", "0"}, []string{"feedback", "list", "--limit", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exitCode := -1
			oldExit := exitFunc
			exitFunc = func(code int) { exitCode = code }
			defer func() { exitFunc = oldExit }()
			handleFeedback(tc.cmdArgs, tc.rawArgs, false)
			if exitCode != 1 {
				t.Fatalf("expected exit code 1, got %d", exitCode)
			}
		})
	}
}

func TestReadFeedbackSkipsCorruptLines(t *testing.T) {
	home := setupFeedbackHome(t)
	content := `{"time":"2026-07-28T01:00:00Z","message":"good one"}
not-json
{"time":"2026-07-28T02:00:00Z","message":"another"}

`
	if err := os.WriteFile(filepath.Join(home, feedbackFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := readFeedback()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Message != "good one" || entries[1].Message != "another" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestFeedbackPositionals(t *testing.T) {
	got := feedbackPositionals([]string{"--category", "ux", "slow", "snapshots", "--command", "snapshot", "--json"})
	if strings.Join(got, " ") != "slow snapshots" {
		t.Fatalf("positionals = %v", got)
	}
	// A value flag at the end without a value must not panic or eat anything.
	got = feedbackPositionals([]string{"msg", "--category"})
	if strings.Join(got, " ") != "msg" {
		t.Fatalf("positionals = %v", got)
	}
}

func TestFormatFeedbackEntry(t *testing.T) {
	entry := feedbackEntry{
		Time: time.Date(2026, 7, 28, 1, 2, 3, 0, time.UTC), Category: "bug",
		Command: "open", SessionID: "tmux-1", Message: "hello",
	}
	line := formatFeedbackEntry(entry)
	for _, want := range []string{"[bug cmd=open session=tmux-1]", "hello"} {
		if !strings.Contains(line, want) {
			t.Fatalf("formatted line missing %q; got %q", want, line)
		}
	}
	plain := formatFeedbackEntry(feedbackEntry{Time: entry.Time, Message: "plain"})
	if strings.Contains(plain, "[") || !strings.Contains(plain, "plain") {
		t.Fatalf("plain line = %q", plain)
	}
}
