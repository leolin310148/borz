package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

func setupLogTest(t *testing.T) {
	t.Helper()
	t.Setenv(config.HomeEnv, t.TempDir())
	if err := config.SetProfile(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
}

func TestLoggerWritesPrivacySafeJSONL(t *testing.T) {
	setupLogTest(t)
	l, err := Open("daemon", "test")
	if err != nil {
		t.Fatal(err)
	}
	success := false
	if err := l.Log("warn", "command_completed", Fields{
		RequestID: "r1", Action: "fill", URLHost: URLHost("https://user:pass@example.com/a?token=secret"),
		TextBytes: len("super-secret"), Success: &success, ErrorCode: "stale_ref",
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret") || strings.Contains(string(raw), "token=") || strings.Contains(string(raw), "user:pass") {
		t.Fatalf("sensitive data leaked: %s", raw)
	}
	var got Entry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.URLHost != "example.com" || got.TextBytes != 12 || got.ErrorCode != "stale_ref" {
		t.Fatalf("entry = %+v", got)
	}
	if st, err := os.Stat(l.Path()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %v err=%v", st.Mode().Perm(), err)
	}
}

func TestLoggerRotatesAndReadsEntries(t *testing.T) {
	setupLogTest(t)
	l, err := open("daemon", "test", 350, 2)
	if err != nil {
		t.Fatal(err)
	}
	l.now = func() time.Time { return time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) }
	for i := 0; i < 8; i++ {
		if err := l.Log("info", "command_completed", Fields{Action: "snapshot"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.Close()
	if _, err := os.Stat(l.Path() + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	entries, err := ReadEntries(time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || len(entries) > 8 {
		t.Fatalf("entries = %d", len(entries))
	}
	if dir := filepath.Dir(l.Path()); dir != config.LogsDir() {
		t.Fatalf("dir = %s want %s", dir, config.LogsDir())
	}
}

func TestErrorCode(t *testing.T) {
	tests := map[string]string{
		"ref 9 not found":            "stale_ref",
		"tab not found":              "tab_not_found",
		"Command timeout":            "command_timeout",
		"wait for selector timeout":  "wait_timeout",
		"Chrome not connected (CDP)": "cdp_disconnected",
		"no extension connected":     "extension_unavailable",
		"something odd":              "command_error",
	}
	for in, want := range tests {
		if got := ErrorCode(in); got != want {
			t.Errorf("ErrorCode(%q) = %q want %q", in, got, want)
		}
	}
}
