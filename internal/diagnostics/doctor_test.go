package diagnostics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderText_AllOK(t *testing.T) {
	out := RenderText([]Check{
		{Name: "A", Status: "ok", Detail: "fine"},
		{Name: "B", Status: "ok"},
	})
	if !strings.Contains(out, "[OK]") {
		t.Errorf("expected [OK] marker; got: %s", out)
	}
	if !strings.Contains(out, "All checks passed") {
		t.Errorf("expected success summary; got: %s", out)
	}
}

func TestRenderText_WithFail(t *testing.T) {
	out := RenderText([]Check{
		{Name: "A", Status: "ok"},
		{Name: "B", Status: "fail", Detail: "broken"},
		{Name: "C", Status: "warn", Detail: "stale"},
	})
	if !strings.Contains(out, "[FAIL]") || !strings.Contains(out, "[WARN]") {
		t.Errorf("expected FAIL+WARN markers; got: %s", out)
	}
	if !strings.Contains(out, "1 failed, 1 warning") {
		t.Errorf("expected fail+warn summary; got: %s", out)
	}
}

func TestRenderJSON_Shape(t *testing.T) {
	out := RenderJSON([]Check{
		{Name: "A", Status: "ok"},
		{Name: "B", Status: "fail", Detail: "broken"},
	})
	var decoded struct {
		OK     bool    `json:"ok"`
		Checks []Check `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if decoded.OK {
		t.Errorf("expected ok=false when a check fails")
	}
	if len(decoded.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(decoded.Checks))
	}
}

func TestCheckHomeDir_Exists(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", tmp)
	c := checkHomeDir()
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckHomeDir_Missing(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "nope")
	t.Setenv("BB_BROWSER_HOME", missing)
	c := checkHomeDir()
	if c.Status != "warn" {
		t.Errorf("expected warn for missing home, got %s", c.Status)
	}
}

func TestCheckHomeDir_File(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "homefile")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BB_BROWSER_HOME", target)
	c := checkHomeDir()
	if c.Status != "fail" {
		t.Errorf("expected fail when home path is a file, got %s", c.Status)
	}
}

func TestCheckDaemonJSON_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", tmp)
	info, c := checkDaemonJSON()
	if info != nil {
		t.Errorf("expected nil info when daemon.json missing")
	}
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}
