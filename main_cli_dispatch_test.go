package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/client"
	"github.com/leolin310148/bb-browser-go/internal/daemon"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

type cliFakeDaemon struct {
	t        *testing.T
	server   *httptest.Server
	requests []protocol.Request
}

func newFakeDaemon(t *testing.T) *cliFakeDaemon {
	t.Helper()
	fd := &cliFakeDaemon{t: t}
	fd.server = httptest.NewServer(http.HandlerFunc(fd.serveHTTP))
	t.Cleanup(func() {
		fd.server.Close()
		client.ResetForTests()
		jqExpression = ""
	})

	host, port := splitTestServerAddr(t, fd.server)
	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	info := protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port, Token: "test-token"}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal daemon info: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
	client.ResetForTests()
	return fd
}

func splitTestServerAddr(t *testing.T, ts *httptest.Server) (string, int) {
	t.Helper()
	hostPort := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("split test server URL: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func (fd *cliFakeDaemon) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		fd.t.Errorf("Authorization = %q", got)
	}
	switch r.URL.Path {
	case "/status":
		writeJSON(fd.t, w, map[string]any{
			"running":      true,
			"cdpConnected": true,
			"uptime":       12,
		})
	case "/command":
		var req protocol.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fd.t.Errorf("decode command: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fd.requests = append(fd.requests, req)
		writeJSON(fd.t, w, protocol.Response{ID: req.ID, Success: true, Data: responseDataFor(req)})
	case "/v1/cookies/all":
		writeJSON(fd.t, w, []extCookie{{Name: "sid", Value: strings.Repeat("x", 70), Domain: "example.test", Path: "/app"}})
	case "/v1/tabs/events":
		writeJSON(fd.t, w, tabEventsResponse{
			Events: []tabEvent{{
				Seq:  7,
				Time: time.Date(2026, 4, 28, 11, 40, 0, 0, time.UTC),
				Name: "created",
				Data: json.RawMessage(`{"tab":"tab-1"}`),
			}},
			LatestSeq: 7,
			Connected: true,
		})
	case "/shutdown":
		writeJSON(fd.t, w, map[string]any{"ok": true})
	default:
		fd.t.Errorf("unexpected path %s", r.URL.String())
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func responseDataFor(req protocol.Request) *protocol.ResponseData {
	status := 200
	cursor := 99
	active := 0
	return &protocol.ResponseData{
		Title: "Example title",
		URL:   "https://example.test",
		Tab:   "tab-1",
		TabID: "target-1",
		SnapshotData: &protocol.SnapshotData{
			Snapshot: "Page snapshot",
			Refs:     map[string]*protocol.RefInfo{"e1": {Role: "button", Name: "Save"}},
		},
		Value:   "value text",
		DataURL: "data:image/png;base64,abcd",
		Result:  map[string]any{"ok": true, "echo": req.Script},
		Tabs: []protocol.TabInfo{{
			Index: 0, URL: "https://example.test", Title: "Example title", Active: true, Tab: "tab-1", TabID: "target-1",
		}},
		ActiveIndex: &active,
		NetworkRequests: []protocol.NetworkRequestInfo{{
			RequestID: "r1", URL: "https://example.test/api", Method: "GET", Type: "fetch", Status: &status,
		}},
		ConsoleMessages: []protocol.ConsoleMessageInfo{{Type: "log", Text: "hello"}},
		JSErrors:        []protocol.JSErrorInfo{{Message: "boom"}},
		TraceStatus:     &protocol.TraceStatus{Recording: true, EventCount: 3},
		Cursor:          &cursor,
	}
}

func runMainWithFakeDaemon(t *testing.T, args ...string) (string, []protocol.Request) {
	t.Helper()
	fd := newFakeDaemon(t)
	oldArgs := os.Args
	os.Args = append([]string{"bb-browser"}, args...)
	defer func() { os.Args = oldArgs }()
	jqExpression = ""

	out := captureStdout(t, main)
	return out, fd.requests
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	return <-done
}

func TestMainDispatchesBrowserCommands(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		action protocol.ActionType
		check  func(t *testing.T, req protocol.Request, out string)
	}{
		{
			name:   "open with flags",
			args:   []string{"open", "https://example.test", "--new", "--wait-for", "#ready", "--timeout", "25", "--tab", "tab-a"},
			action: protocol.ActionOpen,
			check: func(t *testing.T, req protocol.Request, out string) {
				if req.URL != "https://example.test" || !req.New || req.WaitFor != "#ready" || req.TimeoutMs == nil || *req.TimeoutMs != 25 || req.TabID != "tab-a" {
					t.Fatalf("open request = %+v", req)
				}
				if !strings.Contains(out, "Opened: https://example.test") {
					t.Fatalf("open output = %q", out)
				}
			},
		},
		{
			name:   "snapshot options",
			args:   []string{"snapshot", "--text", "-i", "-c", "-d", "3", "-s", "main"},
			action: protocol.ActionSnapshot,
			check: func(t *testing.T, req protocol.Request, out string) {
				if req.Mode != "text" || !req.Interactive || !req.Compact || req.MaxDepth == nil || *req.MaxDepth != 3 || req.Selector != "main" {
					t.Fatalf("snapshot request = %+v", req)
				}
				if !strings.Contains(out, "Page snapshot") {
					t.Fatalf("snapshot output = %q", out)
				}
			},
		},
		{name: "click", args: []string{"click", "@e1"}, action: protocol.ActionClick, check: expectRefAndOutput("e1", "Clicked")},
		{name: "hover", args: []string{"hover", "@e1"}, action: protocol.ActionHover, check: expectRefAndOutput("e1", "Hovered")},
		{
			name:   "fill joins text",
			args:   []string{"fill", "@name", "hello", "world"},
			action: protocol.ActionFill,
			check: func(t *testing.T, req protocol.Request, out string) {
				if req.Ref != "name" || req.Text != "hello world" {
					t.Fatalf("fill request = %+v", req)
				}
				if !strings.Contains(out, "Filled with: hello world") {
					t.Fatalf("fill output = %q", out)
				}
			},
		},
		{name: "type", args: []string{"type", "@name", "hi"}, action: protocol.ActionType_, check: expectRefTextAndOutput("name", "hi", "Typed: hi")},
		{name: "check", args: []string{"check", "@agree"}, action: protocol.ActionCheck, check: expectRefAndOutput("agree", "Checked")},
		{name: "uncheck", args: []string{"uncheck", "@agree"}, action: protocol.ActionUncheck, check: expectRefAndOutput("agree", "Unchecked")},
		{
			name:   "select",
			args:   []string{"select", "@country", "TW"},
			action: protocol.ActionSelect,
			check: func(t *testing.T, req protocol.Request, out string) {
				if req.Ref != "country" || req.Value != "TW" {
					t.Fatalf("select request = %+v", req)
				}
				if !strings.Contains(out, "Selected: TW") {
					t.Fatalf("select output = %q", out)
				}
			},
		},
		{
			name:   "eval unwrap",
			args:   []string{"eval", "--unwrap", "--no-auto-await", "1 + 1"},
			action: protocol.ActionEval,
			check: func(t *testing.T, req protocol.Request, out string) {
				if req.Script != "1 + 1" {
					t.Fatalf("eval script = %q", req.Script)
				}
				if !strings.Contains(out, `"ok": true`) {
					t.Fatalf("eval output = %q", out)
				}
			},
		},
		{name: "get", args: []string{"get", "text", "@title"}, action: protocol.ActionGet, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Attribute != "text" || req.Ref != "title" {
				t.Fatalf("get request = %+v", req)
			}
			if !strings.Contains(out, "value text") {
				t.Fatalf("get output = %q", out)
			}
		}},
		{name: "screenshot", args: []string{"screenshot"}, action: protocol.ActionScreenshot, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Path != "" {
				t.Fatalf("screenshot request = %+v", req)
			}
			if !strings.Contains(out, "Screenshot captured") {
				t.Fatalf("screenshot output = %q", out)
			}
		}},
		{name: "close", args: []string{"close"}, action: protocol.ActionClose, check: expectOutput("Tab closed")},
		{name: "back", args: []string{"back"}, action: protocol.ActionBack, check: expectOutput("Back")},
		{name: "forward", args: []string{"forward"}, action: protocol.ActionForward, check: expectOutput("Forward")},
		{name: "refresh", args: []string{"refresh"}, action: protocol.ActionRefresh, check: expectOutput("Refreshed")},
		{name: "press", args: []string{"press", "Enter"}, action: protocol.ActionPress, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Key != "Enter" {
				t.Fatalf("press request = %+v", req)
			}
			if !strings.Contains(out, "Pressed: Enter") {
				t.Fatalf("press output = %q", out)
			}
		}},
		{name: "scroll", args: []string{"scroll", "up", "42"}, action: protocol.ActionScroll, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Direction != "up" || req.Pixels == nil || *req.Pixels != 42 {
				t.Fatalf("scroll request = %+v", req)
			}
			if !strings.Contains(out, "Scrolled up 42 pixels") {
				t.Fatalf("scroll output = %q", out)
			}
		}},
		{name: "wait", args: []string{"wait", "7"}, action: protocol.ActionWait, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Ms == nil || *req.Ms != 7 {
				t.Fatalf("wait request = %+v", req)
			}
			if !strings.Contains(out, "Waited 7 ms") {
				t.Fatalf("wait output = %q", out)
			}
		}},
		{name: "tab list", args: []string{"tab", "list"}, action: protocol.ActionTabList, check: expectOutput("Tabs (1 total):")},
		{name: "tab new", args: []string{"tab", "new", "https://new.test"}, action: protocol.ActionTabNew, check: func(t *testing.T, req protocol.Request, out string) {
			if req.URL != "https://new.test" {
				t.Fatalf("tab new request = %+v", req)
			}
			if !strings.Contains(out, "Created tab:") {
				t.Fatalf("tab new output = %q", out)
			}
		}},
		{name: "tab select index", args: []string{"tab", "select", "2"}, action: protocol.ActionTabSelect, check: func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "2" {
				t.Fatalf("tab select request = %+v", req)
			}
			if !strings.Contains(out, "Selected: https://example.test") {
				t.Fatalf("tab select output = %q", out)
			}
		}},
		{name: "tab close id", args: []string{"tab", "close", "tab-1"}, action: protocol.ActionTabClose, check: func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "tab-1" {
				t.Fatalf("tab close request = %+v", req)
			}
			if !strings.Contains(out, "Tab closed") {
				t.Fatalf("tab close output = %q", out)
			}
		}},
		{name: "frame main", args: []string{"frame", "main"}, action: protocol.ActionFrameMain, check: expectOutput("Switched to main frame")},
		{name: "frame selector", args: []string{"frame", "iframe[name=app]"}, action: protocol.ActionFrame, check: func(t *testing.T, req protocol.Request, out string) {
			if req.Selector != "iframe[name=app]" {
				t.Fatalf("frame request = %+v", req)
			}
			if !strings.Contains(out, "Switched to frame: iframe[name=app]") {
				t.Fatalf("frame output = %q", out)
			}
		}},
		{name: "dialog prompt", args: []string{"dialog", "accept", "ok"}, action: protocol.ActionDialog, check: func(t *testing.T, req protocol.Request, out string) {
			if req.DialogResponse != "accept" || req.PromptText != "ok" {
				t.Fatalf("dialog request = %+v", req)
			}
			if !strings.Contains(out, "Dialog handler armed: accept") {
				t.Fatalf("dialog output = %q", out)
			}
		}},
		{name: "network requests", args: []string{"network", "requests", "--filter", "api", "--method", "GET", "--status", "200", "--with-body", "--since", "last_action"}, action: protocol.ActionNetwork, check: func(t *testing.T, req protocol.Request, out string) {
			if req.NetworkCommand != "requests" || req.Filter != "api" || req.Method != "GET" || req.Status != "200" || !req.WithBody || req.Since != "last_action" {
				t.Fatalf("network request = %+v", req)
			}
			if !strings.Contains(out, "[200] GET https://example.test/api fetch") {
				t.Fatalf("network output = %q", out)
			}
		}},
		{name: "network clear", args: []string{"network", "clear"}, action: protocol.ActionNetwork, check: func(t *testing.T, req protocol.Request, out string) {
			if req.NetworkCommand != "clear" {
				t.Fatalf("network clear request = %+v", req)
			}
		}},
		{name: "console clear", args: []string{"console", "--clear"}, action: protocol.ActionConsole, check: func(t *testing.T, req protocol.Request, out string) {
			if req.ConsoleCommand != "clear" {
				t.Fatalf("console request = %+v", req)
			}
			if !strings.Contains(out, "[log] hello") {
				t.Fatalf("console output = %q", out)
			}
		}},
		{name: "errors get", args: []string{"errors", "--filter", "boom", "--since", "4"}, action: protocol.ActionErrors, check: func(t *testing.T, req protocol.Request, out string) {
			if req.ErrorsCommand != "get" || req.Filter != "boom" || numericSince(req.Since) != 4 {
				t.Fatalf("errors request = %+v", req)
			}
			if !strings.Contains(out, "[error] boom") {
				t.Fatalf("errors output = %q", out)
			}
		}},
		{name: "trace status", args: []string{"trace", "status"}, action: protocol.ActionTrace, check: func(t *testing.T, req protocol.Request, out string) {
			if req.TraceCommand != "status" {
				t.Fatalf("trace request = %+v", req)
			}
			if !strings.Contains(out, "Recording: true, Events: 3") {
				t.Fatalf("trace output = %q", out)
			}
		}},
		{name: "fetch", args: []string{"fetch", "https://api.test", "--method", "post"}, action: protocol.ActionEval, check: func(t *testing.T, req protocol.Request, out string) {
			if !strings.Contains(req.Script, `fetch("https://api.test"`) || !strings.Contains(req.Script, `method: "POST"`) {
				t.Fatalf("fetch script = %q", req.Script)
			}
			if !strings.Contains(out, `"ok": true`) {
				t.Fatalf("fetch output = %q", out)
			}
		}},
		{name: "history", args: []string{"history"}, action: protocol.ActionHistory, check: func(t *testing.T, req protocol.Request, out string) {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, reqs := runMainWithFakeDaemon(t, tt.args...)
			if len(reqs) != 1 {
				t.Fatalf("captured %d requests, want 1; output=%q", len(reqs), out)
			}
			if reqs[0].Action != tt.action {
				t.Fatalf("action = %s, want %s; request=%+v", reqs[0].Action, tt.action, reqs[0])
			}
			tt.check(t, reqs[0], out)
		})
	}
}

func TestScreenshotSavesPathLocally(t *testing.T) {
	shotPath := filepath.Join(t.TempDir(), "nested", "shot.png")
	out, reqs := runMainWithFakeDaemon(t, "screenshot", shotPath)
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionScreenshot {
		t.Fatalf("requests = %+v", reqs)
	}
	if reqs[0].Path != "" {
		t.Fatalf("screenshot path should not be sent to daemon: %+v", reqs[0])
	}
	if !strings.Contains(out, "Screenshot saved: "+shotPath) {
		t.Fatalf("screenshot output = %q", out)
	}
	data, err := os.ReadFile(shotPath)
	if err != nil {
		t.Fatalf("read saved screenshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved screenshot is empty")
	}
}

func expectOutput(want string) func(*testing.T, protocol.Request, string) {
	return func(t *testing.T, _ protocol.Request, out string) {
		t.Helper()
		if !strings.Contains(out, want) {
			t.Fatalf("output = %q, want substring %q", out, want)
		}
	}
}

func expectRefAndOutput(wantRef, wantOut string) func(*testing.T, protocol.Request, string) {
	return func(t *testing.T, req protocol.Request, out string) {
		t.Helper()
		if req.Ref != wantRef {
			t.Fatalf("ref = %q, want %q; request=%+v", req.Ref, wantRef, req)
		}
		if !strings.Contains(out, wantOut) {
			t.Fatalf("output = %q, want substring %q", out, wantOut)
		}
	}
}

func expectRefTextAndOutput(wantRef, wantText, wantOut string) func(*testing.T, protocol.Request, string) {
	return func(t *testing.T, req protocol.Request, out string) {
		t.Helper()
		if req.Ref != wantRef || req.Text != wantText {
			t.Fatalf("request = %+v, want ref=%q text=%q", req, wantRef, wantText)
		}
		if !strings.Contains(out, wantOut) {
			t.Fatalf("output = %q, want substring %q", out, wantOut)
		}
	}
}

func numericSince(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func TestMainDispatchesRESTBackedCommands(t *testing.T) {
	out, reqs := runMainWithFakeDaemon(t, "cookies", "all")
	if len(reqs) != 0 {
		t.Fatalf("cookies should use REST endpoint, got command requests: %+v", reqs)
	}
	if !strings.Contains(out, "Cookies (1 total):") || !strings.Contains(out, "example.test/app") || !strings.Contains(out, "…") {
		t.Fatalf("cookies output = %q", out)
	}

	out, reqs = runMainWithFakeDaemon(t, "tab", "events", "--since", "6")
	if len(reqs) != 0 {
		t.Fatalf("tab events should use REST endpoint, got command requests: %+v", reqs)
	}
	if !strings.Contains(out, "[7]") || !strings.Contains(out, "created") {
		t.Fatalf("tab events output = %q", out)
	}
}

func TestMainStatusAndHelpCommands(t *testing.T) {
	out, reqs := runMainWithFakeDaemon(t, "status")
	if len(reqs) != 0 {
		t.Fatalf("status should not post /command, got %+v", reqs)
	}
	if !strings.Contains(out, `"running": true`) {
		t.Fatalf("status output = %q", out)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "version"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "bb-browser-go") {
		t.Fatalf("version output = %q", out)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "help", "tab", "new"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "tab new") {
		t.Fatalf("help output = %q", out)
	}
}

func TestMainClientCommandsAndRemoteRouting(t *testing.T) {
	fd := newFakeDaemon(t)
	client.ResetForTests()

	var commands []protocol.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer remote-token" {
			t.Errorf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/status":
			writeJSON(t, w, map[string]any{"running": true, "cdpConnected": true})
		case "/command":
			var req protocol.Request
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode command: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			commands = append(commands, req)
			writeJSON(t, w, protocol.Response{ID: req.ID, Success: true, Data: responseDataFor(req)})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	out := captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "client", "setup", "--url", ts.URL, "--token", "remote-token"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "Remote client configured") {
		t.Fatalf("setup output = %q", out)
	}
	if !strings.Contains(out, "--remote") {
		t.Fatalf("setup output should mention --remote, got %q", out)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "--remote", "open", "https://remote-after-setup.test", "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, `"success": true`) {
		t.Fatalf("remote open after setup output = %q", out)
	}
	if len(commands) != 1 || commands[0].Action != protocol.ActionOpen || commands[0].URL != "https://remote-after-setup.test" {
		t.Fatalf("remote commands after setup = %+v", commands)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "client", "enable"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "legacy config") || !strings.Contains(out, "--remote") {
		t.Fatalf("enable output = %q", out)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "open", "https://remote.test", "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, `"success": true`) {
		t.Fatalf("open output = %q", out)
	}
	if len(commands) != 1 {
		t.Fatalf("open without --remote should stay local, remote commands = %+v", commands)
	}
	if len(fd.requests) != 1 || fd.requests[0].Action != protocol.ActionOpen || fd.requests[0].URL != "https://remote.test" {
		t.Fatalf("local requests = %+v", fd.requests)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "--remote", "open", "https://remote.test", "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, `"success": true`) {
		t.Fatalf("remote open output = %q", out)
	}
	if len(commands) != 2 || commands[1].Action != protocol.ActionOpen || commands[1].URL != "https://remote.test" {
		t.Fatalf("remote commands = %+v", commands)
	}

	shotPath := filepath.Join(t.TempDir(), "remote-shot.png")
	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "--remote", "screenshot", shotPath, "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, `"screenshotPath": "`+shotPath+`"`) {
		t.Fatalf("remote screenshot output = %q", out)
	}
	if len(commands) != 3 || commands[2].Action != protocol.ActionScreenshot || commands[2].Path != "" {
		t.Fatalf("remote screenshot command should not send host path, commands = %+v", commands)
	}
	if data, err := os.ReadFile(shotPath); err != nil || len(data) == 0 {
		t.Fatalf("remote screenshot file data=%d err=%v", len(data), err)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "client", "status", "--json"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, `"enabled": true`) || strings.Contains(out, "remote-token") {
		t.Fatalf("status output = %q", out)
	}

	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"bb-browser", "client", "disable"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "legacy config") {
		t.Fatalf("disable output = %q", out)
	}
}

func TestMainDaemonAndServerStatusCommands(t *testing.T) {
	for _, args := range [][]string{
		{"daemon", "status"},
		{"server", "status"},
		{"daemon", "stop"},
		{"server", "stop"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, reqs := runMainWithFakeDaemon(t, args...)
			if len(reqs) != 0 {
				t.Fatalf("%v should not post /command, got %+v", args, reqs)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatalf("empty output for %v", args)
			}
		})
	}
}

func TestRunDoctorOutput(t *testing.T) {
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected CDP path %s", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer cdp.Close()
	t.Setenv("BB_BROWSER_CDP_URL", cdp.URL)

	out, reqs := runMainWithFakeDaemon(t, "doctor")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionTabList {
		t.Fatalf("doctor requests = %+v", reqs)
	}
	if !strings.Contains(out, "All checks passed") {
		t.Fatalf("doctor text output = %q", out)
	}

	t.Setenv("BB_BROWSER_CDP_URL", cdp.URL)
	out, reqs = runMainWithFakeDaemon(t, "doctor", "--json")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionTabList {
		t.Fatalf("doctor json requests = %+v", reqs)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("doctor JSON output = %q", out)
	}
}

func TestMainJSONAndJQOutputBranches(t *testing.T) {
	out, reqs := runMainWithFakeDaemon(t, "get", "text", "--json")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionGet {
		t.Fatalf("json get requests = %+v", reqs)
	}
	if !strings.Contains(out, `"success": true`) {
		t.Fatalf("json output = %q", out)
	}

	out, reqs = runMainWithFakeDaemon(t, "get", "text", "--jq", ".value")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionGet {
		t.Fatalf("jq get requests = %+v", reqs)
	}
	if strings.TrimSpace(out) != "value text" {
		t.Fatalf("jq output = %q", out)
	}
}

func TestSendAndPrintJSONErrorBranch(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.Write([]byte(`{"running":true}`))
		case "/command":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("boom"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	out := captureStdout(t, func() {
		sendAndPrint(&protocol.Request{ID: "x", Action: protocol.ActionGet}, true, nil)
	})
	if !strings.Contains(out, `"success": false`) || !strings.Contains(out, `"error"`) {
		t.Fatalf("json error output = %q", out)
	}
}

func TestStartDaemonForegroundReturnsWhenAlreadyRunning(t *testing.T) {
	fd := newFakeDaemon(t)
	host, port := splitTestServerAddr(t, fd.server)

	errOut := captureStderr(t, func() {
		startDaemonForeground([]string{"--host", host, "--port", strconv.Itoa(port), "--cdp-host", "127.0.0.1", "--cdp-port", "1"})
	})
	if !strings.Contains(errOut, "already running") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestStopDaemonAfterUpdateBranches(t *testing.T) {
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	t.Setenv("BB_BROWSER_HOME", t.TempDir())
	stopDaemonAfterUpdate()

	fd := newFakeDaemon(t)
	host, port := splitTestServerAddr(t, fd.server)
	errOut := captureStderr(t, stopDaemonAfterUpdate)
	if !strings.Contains(errOut, "Stopped running daemon") {
		t.Fatalf("local stop stderr = %q (host=%s port=%d)", errOut, host, port)
	}

	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
	client.ResetForTests()
	info := protocol.DaemonInfo{PID: os.Getpid(), Host: "0.0.0.0", Port: 19824}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatalf("write remote daemon.json: %v", err)
	}
	errOut = captureStderr(t, stopDaemonAfterUpdate)
	if !strings.Contains(errOut, "server running on 0.0.0.0:19824") {
		t.Fatalf("remote note stderr = %q", errOut)
	}
}

func TestHandleSiteRunWithLocalAdapter(t *testing.T) {
	newFakeDaemon(t)
	sitesDir := filepath.Join(os.Getenv("BB_BROWSER_HOME"), "sites", "demo")
	if err := os.MkdirAll(sitesDir, 0o755); err != nil {
		t.Fatalf("mkdir sites: %v", err)
	}
	adapter := `/* @meta
{
  "name": "demo/search",
  "description": "Demo search",
  "domain": "example.test",
  "args": {
    "q": {"required": true, "description": "query"}
  },
  "example": "bb-browser demo/search cats"
}
*/
async function(args) { return args.q; }`
	if err := os.WriteFile(filepath.Join(sitesDir, "search.js"), []byte(adapter), 0o644); err != nil {
		t.Fatalf("write adapter: %v", err)
	}

	out := captureStdout(t, func() {
		handleSite([]string{"info", "demo/search"}, false, "")
	})
	if !strings.Contains(out, "Demo search") || !strings.Contains(out, "Args:") {
		t.Fatalf("site info output = %q", out)
	}

	out = captureStdout(t, func() {
		handleSiteRun("demo/search", []string{"kittens"}, false, "tab-1")
	})
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("site run output = %q", out)
	}
}

type stubDaemonRunner struct {
	run func() error
}

func (s stubDaemonRunner) Run() error {
	if s.run != nil {
		return s.run()
	}
	return nil
}

func TestStartDaemonForegroundBuildsServerOptions(t *testing.T) {
	old := newDaemonServer
	t.Cleanup(func() { newDaemonServer = old })

	var got daemon.ServerOptions
	newDaemonServer = func(opts daemon.ServerOptions) daemonRunner {
		got = opts
		return stubDaemonRunner{}
	}

	out := captureStderr(t, func() {
		startDaemonForeground([]string{
			"--host", "127.0.0.1",
			"--port", "21111",
			"--cdp-host", "chrome.test",
			"--cdp-port", "9223",
			"--idle-tab-timeout", "4",
		})
	})
	if out != "" {
		t.Fatalf("unexpected stderr = %q", out)
	}
	if got.Host != "127.0.0.1" || got.Port != 21111 || got.CDPHost != "chrome.test" || got.CDPPort != 9223 || got.IdleTabCloseMinutes != 4 || got.Token == "" {
		t.Fatalf("server options = %+v", got)
	}
}

func TestHandleServerBuildsServerOptions(t *testing.T) {
	old := newDaemonServer
	t.Cleanup(func() { newDaemonServer = old })

	var got daemon.ServerOptions
	newDaemonServer = func(opts daemon.ServerOptions) daemonRunner {
		got = opts
		return stubDaemonRunner{}
	}

	errOut := captureStderr(t, func() {
		handleServer(nil, []string{
			"server",
			"--host", "127.0.0.1",
			"--port", "21112",
			"--token", "secret",
			"--cdp-host", "chrome.test",
			"--cdp-port", "9224",
			"--idle-tab-timeout", "8",
		})
	})
	if !strings.Contains(errOut, "server starting on 127.0.0.1:21112") || !strings.Contains(errOut, "Authorization required") {
		t.Fatalf("stderr = %q", errOut)
	}
	if got.Host != "127.0.0.1" || got.Port != 21112 || got.Token != "secret" || got.CDPHost != "chrome.test" || got.CDPPort != 9224 || got.IdleTabCloseMinutes != 8 || got.Version != version {
		t.Fatalf("server options = %+v", got)
	}
}
