// Package observability provides privacy-safe, bounded local operational logs.
package observability

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

const (
	defaultMaxBytes = int64(10 << 20)
	defaultBackups  = 5
)

// Entry is one JSONL operational event. It intentionally contains metadata,
// never raw tool arguments, page content, scripts, clipboard text, or headers.
type Entry struct {
	Time        time.Time `json:"time"`
	Level       string    `json:"level"`
	Component   string    `json:"component"`
	Event       string    `json:"event"`
	Version     string    `json:"version,omitempty"`
	PID         int       `json:"pid"`
	Profile     string    `json:"profile"`
	SessionID   string    `json:"session_id,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	Surface     string    `json:"surface,omitempty"`
	Action      string    `json:"action,omitempty"`
	Tool        string    `json:"tool,omitempty"`
	Tab         string    `json:"tab,omitempty"`
	URLHost     string    `json:"url_host,omitempty"`
	DurationMS  int64     `json:"duration_ms,omitempty"`
	Success     *bool     `json:"success,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
	TextBytes   int       `json:"text_bytes,omitempty"`
	ScriptBytes int       `json:"script_bytes,omitempty"`
	FileCount   int       `json:"file_count,omitempty"`
	HasRef      bool      `json:"has_ref,omitempty"`
	WaitFor     bool      `json:"wait_for,omitempty"`
	TargetCount int       `json:"target_count,omitempty"`
	PageCount   int       `json:"page_count,omitempty"`
}

// Fields holds optional per-event metadata.
type Fields struct {
	SessionID   string
	RequestID   string
	Surface     string
	Action      string
	Tool        string
	Tab         string
	URLHost     string
	DurationMS  int64
	Success     *bool
	ErrorCode   string
	TextBytes   int
	ScriptBytes int
	FileCount   int
	HasRef      bool
	WaitFor     bool
	TargetCount int
	PageCount   int
}

// Logger writes one component's events to a bounded JSONL file.
type Logger struct {
	mu        sync.Mutex
	path      string
	component string
	version   string
	profile   string
	file      *os.File
	maxBytes  int64
	backups   int
	now       func() time.Time
}

// Open creates a logger for component in the active profile.
func Open(component, version string) (*Logger, error) {
	return open(component, version, defaultMaxBytes, defaultBackups)
}

func open(component, version string, maxBytes int64, backups int) (*Logger, error) {
	component = safeComponent(component)
	dir := config.LogsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure log directory: %w", err)
	}
	path := filepath.Join(dir, component+".jsonl")
	l := &Logger{
		path: path, component: component, version: version,
		profile: profileName(), maxBytes: maxBytes, backups: backups,
		now: time.Now,
	}
	if err := l.openFile(); err != nil {
		return nil, err
	}
	return l, nil
}

func safeComponent(component string) string {
	component = strings.TrimSpace(component)
	if component == "" {
		return "borz"
	}
	for _, r := range component {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "borz"
		}
	}
	return component
}

func profileName() string {
	if p := config.Profile(); p != "" {
		return p
	}
	return "default"
}

func (l *Logger) openFile() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open operational log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure operational log: %w", err)
	}
	l.file = f
	return nil
}

// Path returns the active JSONL path.
func (l *Logger) Path() string { return l.path }

// Close flushes and closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Log appends one structured event. Logging failures are returned so callers
// can report them without affecting browser command execution.
func (l *Logger) Log(level, event string, f Fields) error {
	if l == nil {
		return nil
	}
	entry := Entry{
		Time: l.now().UTC(), Level: safeLabel(level, 16), Component: l.component, Event: safeLabel(event, 64),
		Version: l.version, PID: os.Getpid(), Profile: l.profile,
		SessionID: safeLabel(f.SessionID, 64), RequestID: safeLabel(f.RequestID, 64), Surface: safeLabel(f.Surface, 16),
		Action: safeLabel(f.Action, 64), Tool: safeLabel(f.Tool, 96), Tab: safeLabel(f.Tab, 16), URLHost: safeHost(f.URLHost),
		DurationMS: f.DurationMS, Success: f.Success, ErrorCode: safeLabel(f.ErrorCode, 64),
		TextBytes: f.TextBytes, ScriptBytes: f.ScriptBytes, FileCount: f.FileCount,
		HasRef: f.HasRef, WaitFor: f.WaitFor, TargetCount: f.TargetCount,
		PageCount: f.PageCount,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("operational log is closed")
	}
	if err := l.rotateIfNeeded(int64(len(raw))); err != nil {
		return err
	}
	_, err = l.file.Write(raw)
	return err
}

func safeLabel(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLen {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return ""
		}
	}
	return value
}

func safeHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "/\\@?#") {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}

func (l *Logger) rotateIfNeeded(incoming int64) error {
	st, err := l.file.Stat()
	if err != nil {
		return err
	}
	if st.Size()+incoming <= l.maxBytes {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	if l.backups > 0 {
		_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.backups))
		for i := l.backups - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
		}
		_ = os.Rename(l.path, l.path+".1")
	} else {
		_ = os.Remove(l.path)
	}
	return l.openFile()
}

// Files returns current and rotated JSONL files for the active profile, oldest
// first. It is used by the local logs CLI.
func Files() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(config.LogsDir(), "*.jsonl*"))
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		a, aerr := os.Stat(matches[i])
		b, berr := os.Stat(matches[j])
		if aerr != nil || berr != nil {
			return matches[i] < matches[j]
		}
		return a.ModTime().Before(b.ModTime())
	})
	return matches, nil
}

// ReadEntries reads valid JSONL events at or after since. A partially written
// final line is ignored so inspection remains safe while a daemon is active.
func ReadEntries(since time.Time) ([]Entry, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1<<20)
		for scanner.Scan() {
			var entry Entry
			if json.Unmarshal(scanner.Bytes(), &entry) == nil && !entry.Time.Before(since) {
				entries = append(entries, entry)
			}
		}
		scanErr := scanner.Err()
		_ = f.Close()
		if scanErr != nil && !errors.Is(scanErr, io.EOF) {
			return nil, fmt.Errorf("read %s: %w", path, scanErr)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time.Before(entries[j].Time) })
	return entries, nil
}

// URLHost returns only a URL's hostname (and optional port), excluding paths,
// credentials, query strings, and fragments.
func URLHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// ErrorCode maps unstable error text into a small aggregation-friendly set.
// The original message is deliberately not persisted.
func ErrorCode(errText string) string {
	s := strings.ToLower(errText)
	switch {
	case s == "":
		return "unknown"
	case strings.Contains(s, "ref") && (strings.Contains(s, "not found") || strings.Contains(s, "missing")):
		return "stale_ref"
	case strings.Contains(s, "tab not found") || strings.Contains(s, "target not found"):
		return "tab_not_found"
	case strings.Contains(s, "no extension connected") || strings.Contains(s, "extension") && strings.Contains(s, "not connected"):
		return "extension_unavailable"
	case strings.Contains(s, "domain guard"):
		return "site_guard"
	case strings.Contains(s, "unauthorized") || strings.Contains(s, "authorization"):
		return "auth"
	case strings.Contains(s, "timeout") || strings.Contains(s, "timed out"):
		if strings.Contains(s, "wait") || strings.Contains(s, "selector") {
			return "wait_timeout"
		}
		return "command_timeout"
	case strings.Contains(s, "cdp") || strings.Contains(s, "chrome not connected") || strings.Contains(s, "websocket") || strings.Contains(s, "connection closed"):
		return "cdp_disconnected"
	case strings.Contains(s, "cannot find") && (strings.Contains(s, "browser") || strings.Contains(s, "chromium")):
		return "browser_not_found"
	case strings.Contains(s, "daemon did not start") || strings.Contains(s, "failed to start daemon"):
		return "daemon_start_failed"
	case strings.Contains(s, "required") || strings.Contains(s, "missing") || strings.Contains(s, "invalid") || strings.Contains(s, "unknown command"):
		return "validation"
	default:
		return "command_error"
	}
}
