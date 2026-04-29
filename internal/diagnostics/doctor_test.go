package diagnostics

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
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
	t.Setenv("BORZ_HOME", tmp)
	c := checkHomeDir()
	if c.Status != "ok" {
		t.Errorf("expected ok, got %s (%s)", c.Status, c.Detail)
	}
}

func TestCheckHomeDir_Missing(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "nope")
	t.Setenv("BORZ_HOME", missing)
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
	t.Setenv("BORZ_HOME", target)
	c := checkHomeDir()
	if c.Status != "fail" {
		t.Errorf("expected fail when home path is a file, got %s", c.Status)
	}
}

func TestCheckDaemonJSON_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BORZ_HOME", tmp)
	info, c := checkDaemonJSON()
	if info != nil {
		t.Errorf("expected nil info when daemon.json missing")
	}
	if c.Status != "warn" {
		t.Errorf("expected warn, got %s", c.Status)
	}
}

func TestRun_WithHealthyFakeDaemonAndCDP(t *testing.T) {
	setupFakeDaemon(t, protocol.Response{
		ID:      "tabs",
		Success: true,
		Data: &protocol.ResponseData{Tabs: []protocol.TabInfo{{
			Index: 0,
			URL:   "https://example.test",
			Title: "Example",
			Tab:   "tab-1",
		}}},
	})
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected CDP path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer cdp.Close()
	t.Setenv("BORZ_CDP_URL", cdp.URL)

	checks, ok := Run("test-version")
	if !ok {
		t.Fatalf("expected ok, checks=%+v", checks)
	}
	assertCheckStatus(t, checks, "Binary", "ok")
	assertCheckStatus(t, checks, "Daemon JSON", "ok")
	assertCheckStatus(t, checks, "Daemon process", "ok")
	assertCheckStatus(t, checks, "Daemon HTTP", "ok")
	assertCheckStatus(t, checks, "CDP connected", "ok")
	assertCheckStatus(t, checks, "Tabs", "ok")
	assertCheckStatus(t, checks, "CDP discovery", "ok")
}

func TestRun_FailsWhenDaemonReportsDisconnectedCDP(t *testing.T) {
	setupFakeDaemonWithStatus(t, `{"running":true,"cdpConnected":false}`, protocol.Response{Success: true})
	t.Setenv("BORZ_CDP_URL", "http://127.0.0.1:1")

	checks, ok := Run("test-version")
	if ok {
		t.Fatalf("expected Run to fail, checks=%+v", checks)
	}
	assertCheckStatus(t, checks, "CDP connected", "fail")
}

func TestCheckCDPConnectedVariants(t *testing.T) {
	if c := checkCDPConnected(json.RawMessage(`not-json`)); c.Status != "warn" {
		t.Fatalf("invalid JSON status = %s", c.Status)
	}
	if c := checkCDPConnected(json.RawMessage(`{"cdpConnected":false}`)); c.Status != "fail" {
		t.Fatalf("disconnected status = %s", c.Status)
	}
	if c := checkCDPConnected(json.RawMessage(`{"cdpConnected":true}`)); c.Status != "ok" {
		t.Fatalf("connected status = %s", c.Status)
	}
}

func TestCheckTabsVariants(t *testing.T) {
	setupFakeDaemon(t, protocol.Response{Success: true, Data: &protocol.ResponseData{}})
	if c := checkTabs(); c.Status != "warn" || !strings.Contains(c.Detail, "no open tabs") {
		t.Fatalf("empty tabs check = %+v", c)
	}

	setupFakeDaemon(t, protocol.Response{Success: false, Error: "tab list failed"})
	if c := checkTabs(); c.Status != "warn" || c.Detail != "tab list failed" {
		t.Fatalf("failed tabs check = %+v", c)
	}
}

func TestCheckDaemonProcessVariants(t *testing.T) {
	if c := checkDaemonProcess(&protocol.DaemonInfo{PID: os.Getpid()}); c.Status != "ok" {
		t.Fatalf("self process check = %+v", c)
	}
	if c := checkDaemonProcess(&protocol.DaemonInfo{PID: 999999}); c.Status != "fail" {
		t.Fatalf("missing process check = %+v", c)
	}
}

func TestCheckDaemonHTTPFailure(t *testing.T) {
	t.Setenv("BORZ_HOME", t.TempDir())
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	raw, c := checkDaemonHTTP()
	if raw != nil {
		t.Fatalf("expected nil raw status, got %s", raw)
	}
	if c.Status != "fail" {
		t.Fatalf("expected fail, got %+v", c)
	}
}

func TestNewIDShape(t *testing.T) {
	id := newID()
	if len(id) != 16 {
		t.Fatalf("id length = %d", len(id))
	}
	if _, err := strconv.ParseUint(id, 16, 64); err != nil {
		t.Fatalf("id is not hex: %q", id)
	}
}

func setupFakeDaemon(t *testing.T, commandResp protocol.Response) {
	t.Helper()
	setupFakeDaemonWithStatus(t, `{"running":true,"cdpConnected":true}`, commandResp)
}

func setupFakeDaemonWithStatus(t *testing.T, status string, commandResp protocol.Response) {
	t.Helper()
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(status))
		case "/command":
			json.NewEncoder(w).Encode(commandResp)
		default:
			t.Fatalf("unexpected daemon path %s", r.URL.Path)
		}
	}))
	t.Cleanup(ts.Close)

	hostPort := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse daemon port: %v", err)
	}

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	info := protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal daemon info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
}

func assertCheckStatus(t *testing.T, checks []Check, name, want string) {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			if c.Status != want {
				t.Fatalf("%s status = %s, want %s (detail=%q)", name, c.Status, want, c.Detail)
			}
			return
		}
	}
	t.Fatalf("missing check %q in %+v", name, checks)
}
