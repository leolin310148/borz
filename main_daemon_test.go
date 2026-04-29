package main

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

	"github.com/leolin310148/borz/internal/protocol"
)

// fakeDaemon starts an httptest server that replies to /status and /command,
// and writes a matching daemon.json into a tempdir pointed at by
// BORZ_HOME. Returns the server for handler registration overrides.
func fakeDaemon(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/command":
			var req protocol.Request
			json.NewDecoder(r.Body).Decode(&req)
			resp := protocol.Response{
				ID:      req.ID,
				Success: true,
				Data: &protocol.ResponseData{
					URL:   "https://out",
					Title: "out",
					Tabs:  []protocol.TabInfo{{Index: 0, URL: "https://a", Title: "A", Tab: "aaaa"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case "/shutdown":
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, _ := net.SplitHostPort(u)
	port, _ := strconv.Atoi(portStr)
	info := protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port}
	b, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), b, 0o644); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}

	return ts
}

func TestHandleDaemon_Status(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleDaemon([]string{"status"}, nil)
	})
	if !strings.Contains(out, "running") {
		t.Fatalf("status output: %q", out)
	}
}

func TestHandleDaemon_Shutdown(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleDaemon([]string{"shutdown"}, nil)
	})
	if !strings.Contains(out, "stopped") {
		t.Fatalf("shutdown output: %q", out)
	}
}

func TestHandleTab_List(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab([]string{"list"}, false, "", []string{"tab", "list"})
	})
	if !strings.Contains(out, "Tabs") {
		t.Fatalf("tab list: %q", out)
	}
}

func TestHandleTab_Default_EmptyArgs(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab(nil, false, "", nil)
	})
	if !strings.Contains(out, "Tabs") {
		t.Fatalf("tab default: %q", out)
	}
}

func TestHandleTab_SelectByIndex(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab([]string{"select", "0"}, false, "", []string{"tab", "select", "0"})
	})
	if !strings.Contains(out, "Selected") {
		t.Fatalf("tab select: %q", out)
	}
}

func TestHandleTab_Close(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab([]string{"close"}, false, "", []string{"tab", "close"})
	})
	if !strings.Contains(out, "closed") {
		t.Fatalf("tab close: %q", out)
	}
}

func TestHandleTab_BareIndex(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab([]string{"2"}, false, "", []string{"tab", "2"})
	})
	if !strings.Contains(out, "Selected") {
		t.Fatalf("tab bare index: %q", out)
	}
}

func TestHandleTab_New(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleTab([]string{"new", "https://x"}, false, "", []string{"tab", "new", "https://x"})
	})
	if !strings.Contains(out, "Created tab") {
		t.Fatalf("tab new: %q", out)
	}
}

func TestHandleNetwork_Requests(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleNetwork([]string{"requests"}, true, "", "", []string{"network"})
	})
	if len(out) == 0 {
		t.Fatal("network requests produced no output")
	}
}

func TestHandleFetch(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleFetch([]string{"https://ex"}, true, "", []string{"fetch", "https://ex"})
	})
	if len(out) == 0 {
		t.Fatal("fetch produced no output")
	}
}

func TestHandleNetwork_Console(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleNetwork([]string{"console"}, true, "", "", []string{"network", "console"})
	})
	if len(out) == 0 {
		t.Fatal("no output")
	}
}

func TestHandleNetwork_Errors(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleNetwork([]string{"errors"}, true, "", "", []string{"network", "errors"})
	})
	if len(out) == 0 {
		t.Fatal("no output")
	}
}

func TestHandleNetwork_Clear(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleNetwork([]string{"clear"}, true, "", "", []string{"network", "clear"})
	})
	_ = out
}

func TestHandleServer_Status(t *testing.T) {
	fakeDaemon(t)
	out := captureStdout(t, func() {
		handleServer([]string{"status"}, nil)
	})
	if !strings.Contains(out, "running") {
		t.Fatalf("server status: %q", out)
	}
}
