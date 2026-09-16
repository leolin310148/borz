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

// pinDaemon is fakeDaemon's sibling for the extension-backed pin endpoint,
// which `tab pin`/`tab unpin` hit instead of /command. It records every pin
// body it receives and replies with the given JSON.
func pinDaemon(t *testing.T, reply string) *[]map[string]any {
	t.Helper()
	var bodies []map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/v1/ext/tabs/pin":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies = append(bodies, body)
			w.Write([]byte(reply))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	b, _ := json.Marshal(protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port})
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), b, 0o644); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
	return &bodies
}

func TestHandleTab_Pin(t *testing.T) {
	bodies := pinDaemon(t, `{"ok":true,"pinned":true,"url":"https://teams.test/","title":"Teams","tab":"ab1c"}`)

	out := captureStdout(t, func() {
		handleTab([]string{"pin", "--id", "ab1c"}, false, "", []string{"tab", "pin", "--id", "ab1c"})
	})
	if !strings.Contains(out, "Pinned: https://teams.test/ - Teams (tab: ab1c)") {
		t.Fatalf("tab pin: %q", out)
	}
	if len(*bodies) != 1 || (*bodies)[0]["tab"] != "ab1c" || (*bodies)[0]["pinned"] != true {
		t.Fatalf("pin body: %+v", *bodies)
	}
}

func TestHandleTab_Unpin(t *testing.T) {
	bodies := pinDaemon(t, `{"ok":true,"pinned":false,"url":"https://teams.test/","title":"Teams","tab":"ab1c"}`)

	out := captureStdout(t, func() {
		handleTab([]string{"unpin", "2"}, false, "", []string{"tab", "unpin", "2"})
	})
	if !strings.Contains(out, "Unpinned: https://teams.test/") {
		t.Fatalf("tab unpin: %q", out)
	}
	if (*bodies)[0]["tab"] != "2" || (*bodies)[0]["pinned"] != false {
		t.Fatalf("unpin body: %+v", *bodies)
	}
}

func TestHandleTab_PinUsesGlobalTabFlag(t *testing.T) {
	bodies := pinDaemon(t, `{"ok":true,"pinned":true,"url":"https://a.test/","tab":"zz99"}`)

	out := captureStdout(t, func() {
		handleTab([]string{"pin"}, false, "zz99", []string{"tab", "pin"})
	})
	// No title in the response: the untitled placeholder keeps the line shaped
	// the same as the tab list.
	if !strings.Contains(out, "Pinned: https://a.test/ - (untitled) (tab: zz99)") {
		t.Fatalf("tab pin: %q", out)
	}
	if (*bodies)[0]["tab"] != "zz99" {
		t.Fatalf("pin body: %+v", *bodies)
	}
}

func TestHandleTab_PinJSONPassThrough(t *testing.T) {
	pinDaemon(t, `{"ok":true,"pinned":true,"id":42}`)

	out := captureStdout(t, func() {
		handleTab([]string{"pin"}, true, "", []string{"tab", "pin", "--json"})
	})
	if !strings.Contains(out, `"id":42`) {
		t.Fatalf("tab pin --json: %q", out)
	}
}

func TestHandleTab_PinFallsBackWhenResponseHasNoURL(t *testing.T) {
	pinDaemon(t, `{"ok":true}`)

	out := captureStdout(t, func() {
		handleTab([]string{"pin"}, false, "", []string{"tab", "pin"})
	})
	if !strings.Contains(out, "Pinned tab") {
		t.Fatalf("tab pin fallback: %q", out)
	}
}
