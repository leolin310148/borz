package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServerRunContextAuthCORSAndShutdownBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	port := freeTCPPortForTest(t)
	s := NewServer(ServerOptions{
		Host: "127.0.0.1", Port: port, Token: "secret",
		CDPHost: "127.0.0.1", CDPPort: 1, IdleTabCloseMinutes: 1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.RunContext(ctx) }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		// Try once more so a slow CI machine can still pass after the loop.
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			cancel()
			t.Fatalf("healthz did not start: %v", lastErr)
		}
		_ = resp.Body.Close()
	}

	resp, err := http.Get(base + "/status")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		cancel()
		t.Fatalf("unauthorized status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(base + "/status?token=secret")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("authorized status = %d", resp.StatusCode)
	}
	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if status["running"] != true || status["tabs"] == nil {
		cancel()
		t.Fatalf("status body = %+v", status)
	}

	req, _ := http.NewRequest(http.MethodOptions, base+"/status", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		cancel()
		t.Fatalf("cors preflight status=%d headers=%v", resp.StatusCode, resp.Header)
	}
	_ = resp.Body.Close()

	if _, err := os.Stat(filepath.Join(home, "daemon.json")); err != nil {
		cancel()
		t.Fatalf("daemon.json not written: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunContext returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "daemon.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon.json should be removed after shutdown, stat=%v", err)
	}
	if err := s.shutdown(); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

func TestServerRunContextBindAndSmallBranches(t *testing.T) {
	t.Setenv("BORZ_HOME", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	s := NewServer(ServerOptions{Host: "127.0.0.1", Port: port})
	err = s.RunContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("bind error = %v", err)
	}
	if isAddrInUse(nil) {
		t.Fatal("nil error should not be address-in-use")
	}
	if s.uptime() != 0 {
		t.Fatal("zero startTime uptime should be 0")
	}
	if err := NewServer(ServerOptions{}).shutdown(); err != nil {
		t.Fatalf("shutdown without http server: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/shutdown", nil)
	NewServer(ServerOptions{}).handleShutdown(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET shutdown = %d", rec.Code)
	}
}

func freeTCPPortForTest(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return port
}
