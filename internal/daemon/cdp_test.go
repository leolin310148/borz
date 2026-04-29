package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

// rawMsg builds a map[string]json.RawMessage from typed fields.
func rawMsg(t *testing.T, fields map[string]interface{}) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", k, err)
		}
		out[k] = b
	}
	return out
}

func TestCdp_Connected_Default(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	if c.Connected() {
		t.Fatal("default should be disconnected")
	}
}

func TestCdp_HasSession(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	if c.HasSession("nope") {
		t.Fatal("no sessions yet")
	}
	c.sessions.Store("t1", "s1")
	if !c.HasSession("t1") {
		t.Fatal("should report existing session")
	}
}

func TestCdp_WaitUntilReady(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())

	// Timeout path.
	start := time.Now()
	err := c.WaitUntilReady(50 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("timeout took too long: %v", time.Since(start))
	}

	// readyCh closed path.
	c2 := NewCdpConnection("h", 1, NewTabStateManager())
	c2.readyOnce.Do(func() { close(c2.readyCh) })
	if err := c2.WaitUntilReady(time.Second); err != nil {
		t.Fatalf("ready path: %v", err)
	}

	// Already connected path.
	c3 := NewCdpConnection("h", 1, NewTabStateManager())
	c3.connected.Store(true)
	if err := c3.WaitUntilReady(time.Second); err != nil {
		t.Fatalf("connected path: %v", err)
	}
}

func TestCdp_Disconnect_RejectsPending(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())

	// Seed a pending command and ensure Disconnect rejects it.
	pc := &pendingCommand{
		ch:     make(chan json.RawMessage, 1),
		errCh:  make(chan error, 1),
		method: "Test",
	}
	c.pending.Store(int64(42), pc)

	c.Disconnect()

	select {
	case err := <-pc.errCh:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("errCh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending command was not rejected")
	}

	if c.Connected() {
		t.Fatal("connected should be false after Disconnect")
	}
}

func TestHandleAttached_StoresSession(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())

	msg := rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"sessionId":  "S1",
			"targetInfo": map[string]interface{}{"targetId": "T1"},
		},
	})
	c.handleAttached(msg)

	if v, ok := c.sessions.Load("T1"); !ok || v.(string) != "S1" {
		t.Fatalf("session not stored: ok=%v v=%v", ok, v)
	}
	if v, ok := c.attached.Load("S1"); !ok || v.(string) != "T1" {
		t.Fatalf("attached not stored: ok=%v v=%v", ok, v)
	}

	// Empty values are ignored.
	c2 := NewCdpConnection("h", 1, NewTabStateManager())
	c2.handleAttached(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"sessionId": "", "targetInfo": map[string]interface{}{"targetId": ""}},
	}))
	if _, ok := c2.sessions.Load(""); ok {
		t.Fatal("empty should not be stored")
	}

	// Missing params: no panic.
	c2.handleAttached(map[string]json.RawMessage{})
}

func TestHandleDetached_CleansUp(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	c.TabManager.AddTab("T1")
	c.sessions.Store("T1", "S1")
	c.attached.Store("S1", "T1")
	c.CurrentTargetID = "T1"

	c.handleDetached(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"sessionId": "S1"},
	}))

	if _, ok := c.sessions.Load("T1"); ok {
		t.Fatal("session should be cleared")
	}
	if _, ok := c.attached.Load("S1"); ok {
		t.Fatal("attached should be cleared")
	}
	if c.TabManager.GetTab("T1") != nil {
		t.Fatal("tab should be removed")
	}
	if c.CurrentTargetID != "" {
		t.Fatalf("current should be cleared: %q", c.CurrentTargetID)
	}

	// Empty session id: no-op.
	c.handleDetached(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"sessionId": ""},
	}))
	// Unknown session id: no-op, no panic.
	c.handleDetached(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"sessionId": "unknown"},
	}))
}

func TestHandleTargetCreated_NonPageIgnored(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	// background_page should be ignored — no AttachAndEnable goroutine spawned.
	c.handleTargetCreated(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"targetInfo": map[string]interface{}{"targetId": "T1", "type": "iframe"},
		},
	}))
	// Nothing to assert directly; just ensure no panic. AttachAndEnable on a
	// disconnected cdp would error asynchronously — acceptable for this test.
}

func TestHandleTargetDestroyed_CleansUp(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	c.TabManager.AddTab("T1")
	c.sessions.Store("T1", "S1")
	c.attached.Store("S1", "T1")
	c.CurrentTargetID = "T1"

	c.handleTargetDestroyed(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"targetId": "T1"},
	}))

	if _, ok := c.sessions.Load("T1"); ok {
		t.Fatal("session should be cleared")
	}
	if _, ok := c.attached.Load("S1"); ok {
		t.Fatal("attached should be cleared")
	}
	if c.TabManager.GetTab("T1") != nil {
		t.Fatal("tab should be removed")
	}
	if c.CurrentTargetID != "" {
		t.Fatal("current should be cleared")
	}

	// Empty target id: no-op.
	c.handleTargetDestroyed(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"targetId": ""},
	}))
	// Unknown target id: still removes via TabManager (which handles missing).
	c.handleTargetDestroyed(rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"targetId": "nope"},
	}))
}

func TestHandleSessionResponse_Success(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	listener := sessionListener{sessionID: "S1", ch: make(chan json.RawMessage, 1), errCh: make(chan error, 1)}
	c.sessionListeners[int64(7)] = listener

	msg := rawMsg(t, map[string]interface{}{
		"id":        int64(7),
		"sessionId": "S1",
		"result":    map[string]interface{}{"ok": true},
	})
	c.handleSessionResponse(nil, msg)

	select {
	case v := <-listener.ch:
		if !strings.Contains(string(v), "true") {
			t.Fatalf("result: got %s", v)
		}
	case <-time.After(time.Second):
		t.Fatal("no result delivered")
	}
}

func TestHandleSessionResponse_Error(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	listener := sessionListener{sessionID: "S1", ch: make(chan json.RawMessage, 1), errCh: make(chan error, 1)}
	c.sessionListeners[int64(8)] = listener

	msg := rawMsg(t, map[string]interface{}{
		"id":        int64(8),
		"sessionId": "S1",
		"error":     map[string]interface{}{"message": "kapow"},
	})
	c.handleSessionResponse(nil, msg)

	select {
	case err := <-listener.errCh:
		if err == nil || !strings.Contains(err.Error(), "kapow") {
			t.Fatalf("errCh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no error delivered")
	}
}

func TestHandleSessionResponse_EmptyResultSentinel(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	listener := sessionListener{sessionID: "S1", ch: make(chan json.RawMessage, 1), errCh: make(chan error, 1)}
	c.sessionListeners[int64(9)] = listener

	// No error, no result -> "{}" sentinel.
	msg := rawMsg(t, map[string]interface{}{
		"id":        int64(9),
		"sessionId": "S1",
	})
	c.handleSessionResponse(nil, msg)

	select {
	case v := <-listener.ch:
		if string(v) != "{}" {
			t.Fatalf("expected {} sentinel, got %s", v)
		}
	case <-time.After(time.Second):
		t.Fatal("no sentinel delivered")
	}
}

func TestHandleSessionResponse_IgnoresUnknown(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())

	// No sessionId -> early return.
	c.handleSessionResponse(nil, rawMsg(t, map[string]interface{}{"id": int64(1), "result": "x"}))
	// No id -> early return.
	c.handleSessionResponse(nil, rawMsg(t, map[string]interface{}{"sessionId": "S1", "result": "x"}))
	// Bad id type (string instead of number) -> early return.
	c.handleSessionResponse(nil, rawMsg(t, map[string]interface{}{"id": "not-num", "sessionId": "S1"}))
	// Listener missing -> early return.
	c.handleSessionResponse(nil, rawMsg(t, map[string]interface{}{
		"id": int64(999), "sessionId": "S1", "result": "x",
	}))
	// Listener sessionID mismatch -> early return.
	listener := sessionListener{sessionID: "S-OTHER", ch: make(chan json.RawMessage, 1), errCh: make(chan error, 1)}
	c.sessionListeners[int64(1000)] = listener
	c.handleSessionResponse(nil, rawMsg(t, map[string]interface{}{
		"id": int64(1000), "sessionId": "S1", "result": "x",
	}))
}

func TestHandleSessionEvent_NetworkFlow(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	// requestWillBeSent.
	params := map[string]interface{}{
		"requestId": "R1",
		"request": map[string]interface{}{
			"url":     "https://ex.test/a",
			"method":  "GET",
			"headers": map[string]interface{}{"X-Foo": "bar"},
		},
		"type":      "xhr",
		"timestamp": 1.5,
	}
	c.handleSessionEvent("T1", "Network.requestWillBeSent", rawMsg(t, map[string]interface{}{"params": params}))
	if len(tab.GetNetworkRequests(QueryOptions{}).Items) != 1 {
		t.Fatal("request not recorded")
	}

	// responseReceived updates status/headers.
	c.handleSessionEvent("T1", "Network.responseReceived", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"requestId": "R1",
			"response": map[string]interface{}{
				"status":     200,
				"statusText": "OK",
				"headers":    map[string]interface{}{"Content-Type": "application/json"},
				"mimeType":   "application/json",
			},
		},
	}))

	// loadingFailed marks failure.
	c.handleSessionEvent("T1", "Network.loadingFailed", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"requestId": "R1", "errorText": "net::ERR_FAIL"},
	}))
}

func TestHandleSessionEvent_ConsoleAndExceptions(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")

	// consoleAPICalled — mixed arg types, warning type mapped to warn.
	c.handleSessionEvent("T1", "Runtime.consoleAPICalled", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"type": "warning",
			"args": []map[string]interface{}{
				{"value": "first"},
				{"value": 42},
				{"description": "[object Window]"},
			},
			"timestamp": 1000.0,
			"stackTrace": map[string]interface{}{
				"callFrames": []map[string]interface{}{
					{"url": "https://ex.test/s.js", "lineNumber": 3},
				},
			},
		},
	}))
	msgs := tab.GetConsoleMessages(QueryOptions{}).Items
	if len(msgs) != 1 {
		t.Fatalf("console: expected 1 got %d", len(msgs))
	}
	if msgs[0].Type != "warn" {
		t.Fatalf("warning should map to warn: %q", msgs[0].Type)
	}
	if !strings.Contains(msgs[0].Text, "first 42 [object Window]") {
		t.Fatalf("text: %q", msgs[0].Text)
	}

	// Unknown console type falls back to "log".
	c.handleSessionEvent("T1", "Runtime.consoleAPICalled", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"type": "weird", "args": []interface{}{}},
	}))
	last := tab.GetConsoleMessages(QueryOptions{}).Items
	if last[len(last)-1].Type != "log" {
		t.Fatalf("unknown type should fall back to log: %q", last[len(last)-1].Type)
	}

	// exceptionThrown.
	c.handleSessionEvent("T1", "Runtime.exceptionThrown", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"exceptionDetails": map[string]interface{}{
				"text":         "Uncaught",
				"url":          "https://ex.test/s.js",
				"lineNumber":   10,
				"columnNumber": 5,
				"exception":    map[string]interface{}{"description": "TypeError: x is not a fn"},
				"stackTrace": map[string]interface{}{
					"callFrames": []map[string]interface{}{
						{"functionName": "foo", "url": "u1", "lineNumber": 1, "columnNumber": 2},
						{"functionName": "", "url": "u2", "lineNumber": 3, "columnNumber": 4},
					},
				},
			},
		},
	}))
	errs := tab.GetJSErrors(QueryOptions{}).Items
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "TypeError") {
		t.Fatalf("errors: %+v", errs)
	}
	if errs[0].StackTrace == "" || !strings.Contains(errs[0].StackTrace, "<anonymous>") {
		t.Fatalf("stack trace missing <anonymous>: %q", errs[0].StackTrace)
	}
	if errs[0].URL != "https://ex.test/s.js" {
		t.Fatalf("url: %q", errs[0].URL)
	}
	if errs[0].LineNumber == nil || *errs[0].LineNumber != 10 {
		t.Fatalf("line: %+v", errs[0])
	}

	// Exception with no URL but with stack frames pulls from first frame.
	c.handleSessionEvent("T1", "Runtime.exceptionThrown", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"exceptionDetails": map[string]interface{}{
				"exception": map[string]interface{}{"description": "Err2"},
				"stackTrace": map[string]interface{}{
					"callFrames": []map[string]interface{}{{"url": "frame-url"}},
				},
			},
		},
	}))
	errs = tab.GetJSErrors(QueryOptions{}).Items
	if errs[len(errs)-1].URL != "frame-url" {
		t.Fatalf("expected url from stack: %+v", errs[len(errs)-1])
	}

	// Exception with neither text nor description falls back to "JavaScript exception".
	c.handleSessionEvent("T1", "Runtime.exceptionThrown", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"exceptionDetails": map[string]interface{}{},
		},
	}))
	errs = tab.GetJSErrors(QueryOptions{}).Items
	if errs[len(errs)-1].Message != "JavaScript exception" {
		t.Fatalf("fallback message: %q", errs[len(errs)-1].Message)
	}
}

func TestHandleSessionEvent_DialogHandler(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")
	tab.DialogHandler = &DialogHandler{Accept: true, PromptText: "hello"}

	// Without a live socket, SessionCommand will fail, but it's called in a
	// goroutine so the handler itself returns immediately. No panic is the win.
	c.handleSessionEvent("T1", "Page.javascriptDialogOpening", rawMsg(t, map[string]interface{}{"params": map[string]interface{}{}}))
}

func TestHandleSessionEvent_UnknownTab(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	// Missing tab short-circuits — no panic.
	c.handleSessionEvent("missing", "Network.loadingFailed", rawMsg(t, map[string]interface{}{"params": map[string]interface{}{}}))
}

// Disconnected paths: all CDP command methods should return "CDP not connected".
func TestCdp_CommandsWhenDisconnected(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())

	if _, err := c.BrowserCommand("X.y", nil); err == nil || !strings.Contains(err.Error(), "CDP not connected") {
		t.Fatalf("BrowserCommand err: %v", err)
	}
	if _, err := c.SessionCommand("T1", "X.y", nil); err == nil || !strings.Contains(err.Error(), "CDP not connected") {
		t.Fatalf("SessionCommand (no session): %v", err)
	}

	// With a session but no socket, SessionCommand still errors.
	c.sessions.Store("T1", "S1")
	if _, err := c.SessionCommand("T1", "X.y", map[string]interface{}{"k": "v"}); err == nil {
		t.Fatalf("SessionCommand (with session) should still fail with no socket")
	}

	if _, err := c.GetTargets(); err == nil {
		t.Fatal("GetTargets should fail when disconnected")
	}
	if findTargetByExactURL(c, "https://x") != nil {
		t.Fatal("findTargetByExactURL should be nil on error")
	}
	if _, err := c.EnsurePageTarget(""); err == nil {
		t.Fatal("EnsurePageTarget should fail when disconnected")
	}
	if err := c.AttachAndEnable("T2"); err == nil {
		t.Fatal("AttachAndEnable should fail when disconnected")
	}
	// AttachAndEnable idempotent branch: already-attached short-circuits.
	c.sessions.Store("T3", "S3")
	if err := c.AttachAndEnable("T3"); err != nil {
		t.Fatalf("AttachAndEnable idempotent: %v", err)
	}
	// Verify PageCommand wrapper exists and propagates.
	if _, err := c.PageCommand("T1", "Page.navigate", map[string]interface{}{"url": "x"}); err == nil {
		t.Fatal("PageCommand should fail when disconnected")
	}
	if _, err := c.Evaluate("T1", "1+1", true); err == nil {
		t.Fatal("Evaluate should fail when disconnected")
	}
}

// Dispatch paths that don't require a real CDP: tab_list returns empty on error.
func TestDispatch_TabList_Disconnected(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabList})
	if resp == nil || !resp.Success {
		t.Fatalf("tab_list should succeed with empty list: %+v", resp)
	}
	if len(resp.Data.Tabs) != 0 {
		t.Fatalf("expected empty tabs, got %+v", resp.Data.Tabs)
	}
}

func TestDispatch_TabNew_Disconnected(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabNew, URL: ""})
	// With no URL, the dispatcher substitutes about:blank and then calls
	// BrowserCommand which fails with "CDP not connected".
	if resp == nil || resp.Success {
		t.Fatalf("tab_new should fail when disconnected: %+v", resp)
	}
	if !strings.Contains(resp.Error, "CDP") {
		t.Fatalf("error: %q", resp.Error)
	}
}

func TestDispatch_OpenNoTab_MissingURL(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen})
	if resp.Success {
		t.Fatalf("open without url should fail: %+v", resp)
	}
	if !strings.Contains(resp.Error, "missing url") {
		t.Fatalf("error: %q", resp.Error)
	}
}

func TestDispatch_OpenNoTab_Disconnected(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test/",
	})
	if resp.Success {
		t.Fatalf("open should fail when disconnected: %+v", resp)
	}
}

// Smoke compile-time check so future refactors stay aware of these exports.
var _ = fmt.Sprintf
