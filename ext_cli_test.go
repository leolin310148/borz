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
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

// extDaemon is fakeDaemon's analogue for the extension-bridge endpoints.
// Resets the client package's cachedInfo on entry and exit so a closed
// httptest server can't bleed into a later test that uses a different
// daemon (e.g. fakeDaemon → handleDaemon → StopDaemon).
func extDaemon(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	ts := httptest.NewServer(handler)
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

func TestTruncate(t *testing.T) {
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("short: got %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("long: got %q", got)
	}
}

func TestEmitTabEvents_Human(t *testing.T) {
	evs := []tabEvent{
		{Seq: 1, Name: "tabs.created", Data: json.RawMessage(`{"id":7}`)},
		{Seq: 2, Name: "tabs.updated", Data: json.RawMessage(`{"id":7,"status":"complete"}`)},
	}
	out := withCapturedStdout(t, func() { emitTabEvents(evs, false) })
	if !strings.Contains(out, "tabs.created") || !strings.Contains(out, `"id":7`) {
		t.Errorf("missing human line: %s", out)
	}
	if strings.Count(out, "\n") != 2 {
		t.Errorf("expected 2 lines, got %s", out)
	}
}

func TestEmitTabEvents_JSON(t *testing.T) {
	evs := []tabEvent{{Seq: 1, Name: "tabs.activated"}}
	out := withCapturedStdout(t, func() { emitTabEvents(evs, true) })
	line := strings.TrimSpace(out)
	if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"tabs.activated"`) {
		t.Errorf("not JSONL: %q", line)
	}
}

func TestFetchTabEvents_Roundtrip(t *testing.T) {
	var seenSince string
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/tabs/events" {
			w.WriteHeader(404)
			return
		}
		seenSince = r.URL.Query().Get("since")
		w.Write([]byte(`{"events":[{"seq":3,"name":"tabs.created","data":{}}],"latest_seq":3,"connected":true}`))
	})

	evs, latest, err := fetchTabEvents(2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if seenSince != "2" {
		t.Errorf("since query = %q, want \"2\"", seenSince)
	}
	if len(evs) != 1 || evs[0].Seq != 3 || evs[0].Name != "tabs.created" {
		t.Errorf("events = %+v", evs)
	}
	if latest != 3 {
		t.Errorf("latest = %d", latest)
	}
}

func TestFetchTabEvents_OmitsSinceWhenZero(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Query().Has("since") {
			t.Errorf("since should not be set when 0")
		}
		w.Write([]byte(`{"events":[],"latest_seq":0,"connected":false}`))
	})
	if _, _, err := fetchTabEvents(0); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestHandleTabEvents_NoTail(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`{"events":[{"seq":1,"name":"tabs.created","data":{}}],"latest_seq":1,"connected":true}`))
	})
	out := withCapturedStdout(t, func() {
		handleTabEvents([]string{"events"}, false)
	})
	if !strings.Contains(out, "tabs.created") {
		t.Errorf("missing event in output: %q", out)
	}
}

func TestHandleTabEvents_TailStopsOnSignal(t *testing.T) {
	requests := 0
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/tabs/events" {
			w.WriteHeader(404)
			return
		}
		requests++
		if requests >= 2 {
			proc, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Errorf("FindProcess: %v", err)
			} else if err := proc.Signal(os.Interrupt); err != nil {
				t.Errorf("signal interrupt: %v", err)
			}
		}
		w.Write([]byte(`{"events":[{"seq":8,"name":"tabs.updated","data":{"id":1}}],"latest_seq":8,"connected":true}`))
	})

	done := make(chan string, 1)
	go func() {
		done <- withCapturedStdout(t, func() {
			handleTabEvents([]string{"events", "--tail", "--interval", "1"}, false)
		})
	}()

	select {
	case out := <-done:
		if !strings.Contains(out, "tabs.updated") {
			t.Fatalf("tail output = %q", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail mode did not stop")
	}
}

func TestHandleCookies_All_InvalidJSONFallsBackToRaw(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`not-json`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, false)
	})
	if !strings.Contains(out, "not-json") {
		t.Fatalf("expected raw fallback, got %q", out)
	}
}

func TestFetchTabEvents_InvalidJSON(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`not-json`))
	})
	if _, _, err := fetchTabEvents(1); err == nil {
		t.Fatal("expected JSON decode error")
	}
}

func TestHandleCookies_All_Human(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		if r.URL.Path != "/v1/cookies/all" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`[{"name":"sid","value":"abc","domain":".example.com","path":"/"}]`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, false)
	})
	if !strings.Contains(out, "Cookies (1 total)") || !strings.Contains(out, "sid = abc") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestHandleCookies_All_JSON(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Write([]byte(`[{"name":"sid","value":"abc","domain":".x"}]`))
	})
	out := withCapturedStdout(t, func() {
		handleCookies([]string{"all"}, true)
	})
	if !strings.Contains(out, `"name":"sid"`) {
		t.Errorf("expected raw JSON, got %q", out)
	}
}
