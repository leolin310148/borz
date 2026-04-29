package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	w.Close()
	return <-done
}

func TestPrintJSON(t *testing.T) {
	out := captureStdout(t, func() {
		printJSON(map[string]string{"key": "value"})
	})
	if !strings.Contains(out, `"key": "value"`) {
		t.Fatalf("printJSON output: %q", out)
	}
}

func TestServiceArgsAndSmallHelpers(t *testing.T) {
	opts, err := serverOptionsFromArgs([]string{
		"--host", "127.0.0.1",
		"--port", "19999",
		"--cdp-host", "127.0.0.2",
		"--cdp-port", "29999",
		"--token", "secret",
		"--idle-tab-timeout", "7",
	}, "0.0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	args := serviceRunArgs("custom", opts)
	joined := strings.Join(args, " ")
	for _, want := range []string{"service run", "--name custom", "--host 127.0.0.1", "--port 19999", "--token secret", "--idle-tab-timeout 7"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("service args missing %q: %v", want, args)
		}
	}
	if firstNonEmpty("", "", "x", "y") != "x" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty returned unexpected value")
	}
	if isRemoteBind("127.0.0.1") || isRemoteBind("localhost") || isRemoteBind("::1") {
		t.Fatal("loopback hosts should not be remote binds")
	}
	if !isRemoteBind("0.0.0.0") || !isRemoteBind("192.0.2.10") {
		t.Fatal("non-loopback hosts should be remote binds")
	}
}

func TestSaveScreenshotDataURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "shot.png")
	resp := &protocol.Response{Data: &protocol.ResponseData{DataURL: "data:image/png;base64,aGVsbG8="}}
	if err := saveScreenshotDataURL(path, resp); err != nil {
		t.Fatalf("saveScreenshotDataURL: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" || resp.Data.ScreenshotPath != path {
		t.Fatalf("data=%q screenshotPath=%q", data, resp.Data.ScreenshotPath)
	}

	errorCases := []*protocol.Response{
		nil,
		{},
		{Data: &protocol.ResponseData{}},
		{Data: &protocol.ResponseData{DataURL: "not-a-data-url"}},
		{Data: &protocol.ResponseData{DataURL: "data:image/png;base64,not-base64"}},
		{Data: &protocol.ResponseData{DataURL: "data:image/png;base64,"}},
	}
	for _, tc := range errorCases {
		if err := saveScreenshotDataURL(path, tc); err == nil {
			t.Fatalf("expected error for response %+v", tc)
		}
	}
}

func TestPrintEvalPrettyAndUnwrap(t *testing.T) {
	req := &protocol.Request{ID: "eval-1", Action: protocol.ActionEval, Script: "({ok:true})"}
	out, requests := runEvalWithResult(t, req, map[string]any{"ok": true})
	if !strings.Contains(out, `"ok": true`) || len(requests) != 1 || requests[0].Action != protocol.ActionEval {
		t.Fatalf("pretty output=%q requests=%+v", out, requests)
	}
	out, _ = runEvalWithResult(t, req, map[string]any{"ok": true}, true)
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("unwrap object output=%q", out)
	}
	out, _ = runEvalWithResult(t, req, "plain result", true)
	if strings.TrimSpace(out) != "plain result" {
		t.Fatalf("unwrap string output=%q", out)
	}
	out, _ = runEvalWithResult(t, req, nil)
	if out != "" {
		t.Fatalf("nil eval result should print nothing, got %q", out)
	}
}

func runEvalWithResult(t *testing.T, req *protocol.Request, result any, unwrap ...bool) (string, []protocol.Request) {
	t.Helper()
	fd := newFakeDaemon(t)
	fd.server.Config.Handler = httpHandlerFunc(t, fd, func(w io.Writer, request protocol.Request) {
		resp := protocol.Response{ID: request.ID, Success: true}
		if result != nil {
			resp.Data = &protocol.ResponseData{Result: result}
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	})
	useUnwrap := len(unwrap) > 0 && unwrap[0]
	out := captureStdout(t, func() { printEval(req, false, useUnwrap) })
	return out, fd.requests
}

func httpHandlerFunc(t *testing.T, fd *cliFakeDaemon, command func(io.Writer, protocol.Request)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			writeJSON(t, w, map[string]any{"running": true})
			return
		}
		if r.URL.Path != "/command" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req protocol.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode command: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fd.requests = append(fd.requests, req)
		w.Header().Set("Content-Type", "application/json")
		command(w, req)
	}
}

func TestPrintHelp(t *testing.T) {
	out := captureStdout(t, func() {
		printHelp()
	})
	if !strings.Contains(out, "borz") {
		t.Fatalf("printHelp should mention borz: %q", out[:min(200, len(out))])
	}
	// Spot-check a few sections.
	for _, want := range []string{"Navigation:", "Interaction:", "Observation:"} {
		if !strings.Contains(out, want) {
			t.Errorf("printHelp missing section %q", want)
		}
	}
}

func TestHandleClient_StatusSetupDisableJSON(t *testing.T) {
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_SERVER_URL", "http://127.0.0.1:19824/base?ignored=1")
	t.Setenv("BORZ_TOKEN", "env-token")

	out := captureStdout(t, func() { handleClient([]string{"status"}, nil, false) })
	if !strings.Contains(out, "Remote client is not configured") || !strings.Contains(out, "client.json") {
		t.Fatalf("unconfigured status output = %q", out)
	}
	out = captureStdout(t, func() { handleClient([]string{"status"}, nil, true) })
	if !strings.Contains(out, `"configured": false`) {
		t.Fatalf("unconfigured status JSON = %q", out)
	}

	out = captureStdout(t, func() { handleClient([]string{"setup"}, []string{"--no-check"}, true) })
	if !strings.Contains(out, `"configured": true`) || !strings.Contains(out, `"tokenConfigured": true`) || strings.Contains(out, "ignored") {
		t.Fatalf("setup JSON = %q", out)
	}
	client.SetRemoteRouting(true)
	out = captureStdout(t, func() { handleClient([]string{"status"}, nil, false) })
	if !strings.Contains(out, "Remote routing: active") || !strings.Contains(out, "Token: configured") {
		t.Fatalf("configured status output = %q", out)
	}
	out = captureStdout(t, func() { handleClient([]string{"disable"}, nil, true) })
	if !strings.Contains(out, `"enabled": false`) {
		t.Fatalf("disable JSON = %q", out)
	}
}

func TestHandleClient_SetupTextAndEnableJSON(t *testing.T) {
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	t.Setenv("BORZ_HOME", t.TempDir())
	checks := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		checks++
		if r.Header.Get("Authorization") != "Bearer token-from-flag" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"running":true}`))
	}))
	defer ts.Close()

	out := captureStdout(t, func() {
		handleClient([]string{"setup", ts.URL}, []string{"--token", "token-from-flag"}, false)
	})
	if !strings.Contains(out, "Remote client configured") || !strings.Contains(out, "--remote") {
		t.Fatalf("setup text output = %q", out)
	}
	out = captureStdout(t, func() {
		handleClient([]string{"enable"}, []string{"--no-check"}, true)
	})
	if !strings.Contains(out, `"enabled": true`) {
		t.Fatalf("enable JSON output = %q", out)
	}
	if checks != 1 {
		t.Fatalf("remote check count = %d, want 1", checks)
	}
}

func TestHandleClient_DisableWithoutConfig(t *testing.T) {
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	t.Setenv("BORZ_HOME", t.TempDir())
	out := captureStdout(t, func() { handleClient([]string{"disable"}, nil, true) })
	if !strings.Contains(out, `"configured": false`) {
		t.Fatalf("disable without config JSON = %q", out)
	}
}

func TestHandleDaemonAndServerStatusWhenStopped(t *testing.T) {
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	t.Setenv("BORZ_HOME", t.TempDir())
	out := captureStdout(t, func() { handleDaemon([]string{"status"}, nil) })
	if !strings.Contains(out, "Daemon is not running") {
		t.Fatalf("daemon status output = %q", out)
	}
	out = captureStdout(t, func() { handleServer([]string{"status"}, nil) })
	if !strings.Contains(out, "Server is not running") {
		t.Fatalf("server status output = %q", out)
	}
}

func TestSendPrepareAndPrintPrepareBranches(t *testing.T) {
	newFakeDaemon(t)
	req := &protocol.Request{ID: "get-1", Action: protocol.ActionGet}
	out := captureStdout(t, func() {
		sendPrepareAndPrint(req, false, func(resp *protocol.Response) error {
			resp.Data.Value = "prepared"
			return nil
		}, func(resp *protocol.Response) {
			if resp.Data.Value != "prepared" {
				t.Fatalf("prepare did not mutate response: %+v", resp.Data)
			}
			fmt.Println(resp.Data.Value)
		})
	})
	if strings.TrimSpace(out) != "prepared" {
		t.Fatalf("pretty output = %q", out)
	}

	out = captureStdout(t, func() {
		sendPrepareAndPrint(req, true, func(*protocol.Response) error {
			return errors.New("prepare failed")
		}, nil)
	})
	if !strings.Contains(out, `"success": false`) || !strings.Contains(out, "prepare failed") {
		t.Fatalf("prepare error JSON = %q", out)
	}
}

func TestHandleSite_List_JSON(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"list"}, true, "")
	})
	// JSON output should look like a JSON array.
	out = strings.TrimSpace(out)
	if !(strings.HasPrefix(out, "[") || strings.HasPrefix(out, "null")) {
		t.Fatalf("expected JSON array or null, got: %q", out[:min(100, len(out))])
	}
}

func TestHandleSite_List_DefaultSub(t *testing.T) {
	// Empty args -> defaults to "list".
	out := captureStdout(t, func() {
		handleSite([]string{}, true, "")
	})
	if len(out) == 0 {
		t.Fatal("expected some output for default list")
	}
}

func TestHandleSite_Search_JSON(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"search", "twitter"}, true, "")
	})
	out = strings.TrimSpace(out)
	if !(strings.HasPrefix(out, "[") || strings.HasPrefix(out, "null")) {
		t.Fatalf("search JSON: %q", out[:min(100, len(out))])
	}
}

func TestHandleSite_Search_Text(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"search", "anything"}, false, "")
	})
	if !strings.Contains(out, "results") {
		t.Fatalf("search text output missing 'results': %q", out)
	}
}

func TestHandleSite_List_Text(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"list"}, false, "")
	})
	if !strings.Contains(out, "Total:") {
		t.Fatalf("list text output missing 'Total:': %q", out)
	}
}
