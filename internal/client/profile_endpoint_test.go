package client

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestDaemonPortForTargetUsesConfiguredPort(t *testing.T) {
	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })
	got, err := daemonPortForTarget(borzprofile.Target{Kind: borzprofile.TransportManaged, DaemonPort: 19827})
	if err != nil || got != 19827 {
		t.Fatalf("daemonPortForTarget = %d, %v", got, err)
	}
}

func TestGetJSONForProfileDoesNotAutostart(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ext/capabilities" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"version":"1.2.3"}`))
	}))
	t.Cleanup(ts.Close)
	host, portText, _ := net.SplitHostPort(ts.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	info, _ := json.Marshal(protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port})
	runtimeDir := filepath.Join(home, "profiles", "clean")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "daemon.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}

	raw, err := GetJSONForProfile("clean", "/v1/ext/capabilities", time.Second)
	if err != nil || string(raw) != `{"version":"1.2.3"}` {
		t.Fatalf("GetJSONForProfile = %s, %v", raw, err)
	}
	if cachedInfo != nil || daemonReady {
		t.Fatalf("aggregate read changed active cache: info=%+v ready=%v", cachedInfo, daemonReady)
	}
	if _, err := GetJSONForProfile("offline", "/v1/ext/capabilities", time.Second); err == nil {
		t.Fatal("offline profile should not be auto-started")
	}
}
