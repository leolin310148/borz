package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestFeedbackInputDoesNotEchoAndAcceptsFiles(t *testing.T) {
	for _, command := range []string{"fill", "type"} {
		t.Run(command, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input.txt")
			const secret = "private-value\nwith newline"
			if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
				t.Fatal(err)
			}
			out, reqs := runMainWithFakeDaemon(t, command, "12", "--file", path)
			if strings.Contains(out, "private") || len(reqs) != 1 || reqs[0].Text != secret {
				t.Fatalf("unexpected output or request")
			}
			out, reqs = runMainWithFakeDaemon(t, command, "12", secret)
			if strings.Contains(out, "private") || len(reqs) != 1 || reqs[0].Text != secret {
				t.Fatalf("positional input was echoed or changed")
			}
		})
	}
}

func TestFeedbackScreenshotOutputAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	out, _ := runMainWithFakeDaemon(t, "screenshot", "--output", path)
	if _, err := os.Stat(path); err != nil || !strings.Contains(out, path) {
		t.Fatalf("screenshot missing: %s %v", out, err)
	}
	_, reqs := runMainWithFakeDaemon(t, "reload")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionRefresh {
		t.Fatalf("reload requests: %+v", reqs)
	}
}

func TestFeedbackJQRootsAndEmptySelection(t *testing.T) {
	defer func() { jqExpression = "" }()
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: map[string]interface{}{"body": map[string]interface{}{"name": "a"}}}}
	for _, expression := range []string{`.result.body | keys`, `.data.result.body | keys`, `.["data"].result.body | keys`} {
		got := applyJQExpression(resp, expression)
		raw, _ := json.Marshal(got)
		if string(raw) != `[["name"]]` {
			t.Fatalf("%s: %s", expression, raw)
		}
	}
	if got := applyJQExpression(resp, `.result.body | select(.name == "b")`); len(got) != 0 {
		t.Fatalf("empty filter retried: %v", got)
	}
}

func TestFeedbackDownloadsJQ(t *testing.T) {
	extDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"startTime":"a","url":"private-signed-url"},{"id":2,"startTime":"b","url":"private-signed-url"}]`))
	})
	defer func() { jqExpression = "" }()
	jqExpression = `sort_by(.startTime) | reverse | .[0:1] | map({id})`
	out := captureStdout(t, func() { handleDownloads([]string{"list"}, true, nil) })
	if strings.TrimSpace(out) != `[{"id":2}]` {
		t.Fatalf("unexpected filtered downloads: %q", out)
	}
}

func TestFeedbackRedactDisplayURL(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"https://example.test/path?q=hello", "https://example.test/path?q=hello"},
		{"https://example.test/cb?code=secret&state=x&q=ok", "https://example.test/cb?code=REDACTED&state=REDACTED&q=ok"},
		{"https://example.test/#access_token=secret", "https://example.test/#access_token=REDACTED"},
		{"https://example.test/#/cb?id_token=secret", "https://example.test/#/cb?id_token=REDACTED"},
		{"https://user:pass@example.test/?X-Amz-Signature=secret", "https://REDACTED@example.test/?X-Amz-Signature=REDACTED"},
		{"https://example.test/?%63ode=secret&code=second", "https://example.test/?%63ode=REDACTED&code=REDACTED"},
	}
	for _, tt := range tests {
		if got := redactDisplayURL(tt.raw); got != tt.want {
			t.Fatalf("got %s, want %s", got, tt.want)
		}
	}
}

func TestFeedbackMouseCLI(t *testing.T) {
	_, reqs := runMainWithFakeDaemon(t, "mouse", "move", "20", "30", "--button", "left", "--tab", "abc", "--wait-for", "#done")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionMouse || *reqs[0].X != 20 || *reqs[0].Y != 30 || reqs[0].Button != "left" || reqs[0].TabID != "abc" || reqs[0].WaitFor != "#done" {
		t.Fatalf("mouse request: %+v", reqs)
	}
	for _, args := range [][]string{{}, {"click", "NaN", "0"}, {"click", "1", "-1"}, {"click", "Inf", "0"}, {"bogus", "1", "1"}} {
		if _, err := mouseCLIRequest(args, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := mouseCLIRequest([]string{"click", "1", "2"}, []string{"--button", "bad"}); err == nil {
		t.Fatal("accepted invalid button")
	}
	if _, err := mouseCLIRequest([]string{"click", "1", "2"}, []string{"--modifiers", "bad"}); err == nil {
		t.Fatal("accepted invalid modifiers")
	}
}

func TestFeedbackScreenshotRejectsFlagsBeforeWriting(t *testing.T) {
	fd := newFakeDaemon(t)
	for _, args := range [][]string{{"screenshot", "--full-page", "--output", filepath.Join(t.TempDir(), "shot.png")}, {"screenshot", "--output"}, {"screenshot", "one.png", "two.png"}} {
		old := os.Args
		os.Args = append([]string{"borz"}, args...)
		captureStderr(t, func() { expectExit(t, 1, main) })
		os.Args = old
	}
	if len(fd.requests) != 0 {
		t.Fatal("invalid screenshot invoked daemon")
	}
}

func TestFeedbackClipboardExplicitEmpty(t *testing.T) {
	_, reqs := runMainWithFakeDaemon(t, "clipboard-write", "")
	if len(reqs) != 1 || reqs[0].Action != protocol.ActionClipboardWrite || reqs[0].Text != "" {
		t.Fatalf("empty clipboard request: %+v", reqs)
	}
}

func TestFeedbackInvalidJQHasNoSideEffects(t *testing.T) {
	fd := newFakeDaemon(t)
	old := os.Args
	defer func() { os.Args = old }()
	for _, filter := range []string{"map(", "nonesuch", ""} {
		os.Args = []string{"borz", "click", "1", "--jq=" + filter}
		captureStderr(t, func() { expectExit(t, 1, main) })
	}
	if len(fd.requests) != 0 {
		t.Fatal("invalid jq still performed action")
	}
}
