package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestEmitNetworkTail_Human(t *testing.T) {
	status200 := 200
	resp := &protocol.Response{
		Success: true,
		Data: &protocol.ResponseData{
			NetworkRequests: []protocol.NetworkRequestInfo{
				{Method: "GET", URL: "https://x/y", Type: "xhr", Status: &status200},
				{Method: "POST", URL: "https://x/z", Type: "fetch"},
			},
		},
	}
	out := withCapturedStdout(t, func() {
		n := emitNetworkTail(resp, false)
		if n != 2 {
			t.Errorf("expected 2 emitted, got %d", n)
		}
	})
	if !strings.Contains(out, "[200] GET https://x/y") {
		t.Errorf("missing first line: %s", out)
	}
	if !strings.Contains(out, "[-] POST https://x/z") {
		t.Errorf("missing second line (no status): %s", out)
	}
}

func TestEmitNetworkTail_JSONL(t *testing.T) {
	resp := &protocol.Response{
		Success: true,
		Data: &protocol.ResponseData{
			NetworkRequests: []protocol.NetworkRequestInfo{
				{Method: "GET", URL: "a"},
				{Method: "POST", URL: "b"},
			},
		},
	}
	out := withCapturedStdout(t, func() {
		emitNetworkTail(resp, true)
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), out)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			t.Errorf("line is not a JSON object: %q", line)
		}
	}
}

func TestEmitNetworkTail_Empty(t *testing.T) {
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{}}
	out := withCapturedStdout(t, func() {
		if n := emitNetworkTail(resp, false); n != 0 {
			t.Errorf("expected 0 emitted, got %d", n)
		}
	})
	if out != "" {
		t.Errorf("expected no output, got %q", out)
	}
}

func TestParseTailInterval_Default(t *testing.T) {
	if got := parseTailInterval([]string{"network", "--tail"}); got != defaultTailInterval {
		t.Errorf("default mismatch: got %v want %v", got, defaultTailInterval)
	}
}

func TestParseTailInterval_Override(t *testing.T) {
	got := parseTailInterval([]string{"--interval", "1500"})
	if got != 1500*time.Millisecond {
		t.Errorf("override mismatch: got %v", got)
	}
}

func TestParseTailInterval_BadValue(t *testing.T) {
	// Capture stderr-ish: parseTailInterval uses fmt.Fprintf to stderr but
	// the contract is that we fall back to default — assert that.
	for _, bad := range []string{"abc", "0", "-1"} {
		got := parseTailInterval([]string{"--interval", bad})
		if got != defaultTailInterval {
			t.Errorf("bad %q: got %v, want default", bad, got)
		}
	}
}

func TestRunTailPollsAndStopsOnSignal(t *testing.T) {
	newFakeDaemon(t)
	defer client.ResetForTests()

	req := &protocol.Request{ID: "tail-1", Action: protocol.ActionNetwork}
	emitted := make(chan *protocol.Response, 1)
	done := make(chan struct{})

	go func() {
		runTail(req, false, time.Hour, func(resp *protocol.Response, jsonOutput bool) int {
			emitted <- resp
			proc, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Errorf("FindProcess: %v", err)
				return 0
			}
			if err := proc.Signal(os.Interrupt); err != nil {
				t.Errorf("signal interrupt: %v", err)
			}
			return 1
		})
		close(done)
	}()

	select {
	case resp := <-emitted:
		if resp == nil || !resp.Success {
			t.Fatalf("unexpected tail response: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tail did not emit")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tail did not stop after interrupt")
	}
	if req.Since == nil {
		t.Fatal("tail did not maintain a cursor")
	}
}
