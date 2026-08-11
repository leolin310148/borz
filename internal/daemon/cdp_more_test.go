package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCdpConnectAndCommandErrorBranches(t *testing.T) {
	tabs := NewTabStateManager()
	c := NewCdpConnection("127.0.0.1", 1, tabs)
	if c.Connected() {
		t.Fatal("new connection should not be connected")
	}
	if _, err := c.BrowserCommand("X", nil); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("BrowserCommand disconnected err = %v", err)
	}
	if _, err := c.SessionCommandWithTimeout("T1", "X", nil, 0); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("SessionCommand disconnected err = %v", err)
	}
	if err := c.WaitUntilReady(1 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("WaitUntilReady timeout err = %v", err)
	}

	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{bad"))
	}))
	defer badJSON.Close()
	host, port, _ := splitHostPort(strings.TrimPrefix(badJSON.URL, "http://"))
	c = NewCdpConnection(host, port, tabs)
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "invalid CDP") || c.GetLastError() == "" {
		t.Fatalf("bad json Connect err=%v last=%q", err, c.GetLastError())
	}

	missingWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer missingWS.Close()
	host, port, _ = splitHostPort(strings.TrimPrefix(missingWS.URL, "http://"))
	c = NewCdpConnection(host, port, tabs)
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "missing") || c.GetLastError() == "" {
		t.Fatalf("missing ws Connect err=%v last=%q", err, c.GetLastError())
	}

	dialFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:1/ws"}`))
	}))
	defer dialFail.Close()
	host, port, _ = splitHostPort(strings.TrimPrefix(dialFail.URL, "http://"))
	c = NewCdpConnection(host, port, tabs)
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "WebSocket") || c.GetLastError() == "" {
		t.Fatalf("dial fail Connect err=%v last=%q", err, c.GetLastError())
	}
}

func TestCdpConnectSetupFailureDisconnects(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.setDiscoverTargets", func(json.RawMessage) (interface{}, error) {
		return nil, errors.New("discover failed")
	})

	c := NewCdpConnection(f.Host(), f.Port(), NewTabStateManager())
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "discover failed") {
		t.Fatalf("Connect err=%v, want discover failure", err)
	}
	if c.Connected() {
		t.Fatal("Connect setup failure should leave CDP disconnected")
	}
	if err := c.WaitUntilReady(time.Second); err == nil || !strings.Contains(err.Error(), "discover failed") {
		t.Fatalf("WaitUntilReady err=%v, want discover failure", err)
	}
}

func TestCdpWaitUntilReadyReconnectsDroppedSocket(t *testing.T) {
	f := newFakeCDP(t)
	c := connectCdp(t, f)

	f.mu.Lock()
	if f.ws == nil {
		f.mu.Unlock()
		t.Fatal("fake CDP websocket was not connected")
	}
	_ = f.ws.Close()
	f.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for c.Connected() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Connected() {
		t.Fatal("CDP connection did not notice dropped websocket")
	}

	if err := c.WaitUntilReady(time.Second); err != nil {
		t.Fatalf("WaitUntilReady reconnect: %v", err)
	}
	if !c.Connected() {
		t.Fatal("CDP should be connected after reconnect")
	}
	if _, err := c.BrowserCommand("Target.getTargets", nil); err != nil {
		t.Fatalf("BrowserCommand after reconnect: %v", err)
	}
}

func TestCdpEvaluatePageCommandAndReadLoopBranches(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a.test", "A")
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		if strings.Contains(string(params), "throw") {
			return map[string]interface{}{
				"result": map[string]interface{}{"type": "object"},
				"exceptionDetails": map[string]interface{}{
					"text": "Uncaught",
					"exception": map[string]interface{}{
						"description": "Error: boom",
					},
				},
			}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": "ok"}}, nil
	})
	c := connectCdp(t, f)
	if _, err := c.Evaluate("T1", "throw new Error('boom')", true); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Evaluate exception err = %v", err)
	}
	raw, err := c.EvaluateWithTimeout("T1", "'ok'", true, 0)
	if err != nil || string(raw) != `"ok"` {
		t.Fatalf("Evaluate ok raw=%s err=%v", raw, err)
	}
	tab := c.TabManager.AddTab("T1")
	tab.SetActiveFrame("FRAME1")
	tab.SetFrameExecutionContext("FRAME1", 42)
	if _, err := c.Evaluate("T1", "document.title", true); err != nil {
		t.Fatalf("Evaluate in active frame: %v", err)
	}
	foundContextParam := false
	for _, call := range f.Calls() {
		if call.Method == "Runtime.evaluate" && strings.Contains(string(call.Params), `"contextId":42`) {
			foundContextParam = true
		}
	}
	if !foundContextParam {
		t.Fatalf("Evaluate did not use active frame context, calls=%+v", f.Calls())
	}
	if _, err := c.PageCommand("T1", "Runtime.evaluate", nil); err != nil {
		t.Fatalf("PageCommand nil params: %v", err)
	}
	foundFrameParam := false
	for _, call := range f.Calls() {
		if call.Method == "Runtime.evaluate" && strings.Contains(string(call.Params), "FRAME1") {
			foundFrameParam = true
		}
	}
	if !foundFrameParam {
		t.Fatalf("PageCommand did not add frameId, calls=%+v", f.Calls())
	}
	if !c.HasSession("T1") {
		t.Fatal("expected T1 session")
	}
	c.Disconnect()
	if c.Connected() {
		t.Fatal("Disconnect should clear connected state")
	}
}

func TestHandleSessionEventTracksFrameExecutionContexts(t *testing.T) {
	c := NewCdpConnection("h", 1, NewTabStateManager())
	tab := c.TabManager.AddTab("T1")
	c.handleSessionEvent("T1", "Runtime.executionContextCreated", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{
			"context": map[string]interface{}{
				"id":      77,
				"auxData": map[string]interface{}{"frameId": "F1", "isDefault": true},
			},
		},
	}))
	if got, ok := tab.FrameExecutionContext("F1"); !ok || got != 77 {
		t.Fatalf("frame context = %d, ok=%v", got, ok)
	}

	c.handleSessionEvent("T1", "Runtime.executionContextDestroyed", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{"executionContextId": 77},
	}))
	if _, ok := tab.FrameExecutionContext("F1"); ok {
		t.Fatal("destroyed execution context remained registered")
	}

	tab.SetFrameExecutionContext("F2", 88)
	c.handleSessionEvent("T1", "Runtime.executionContextsCleared", rawMsg(t, map[string]interface{}{
		"params": map[string]interface{}{},
	}))
	if _, ok := tab.FrameExecutionContext("F2"); ok {
		t.Fatal("cleared execution contexts remained registered")
	}
}

func TestEvaluateUsesSiteIsolatedFrameSession(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetInfos": []interface{}{
			map[string]interface{}{"targetId": "T1", "type": "page", "url": "https://top.test", "title": "Top"},
			map[string]interface{}{"targetId": "F-OOPIF", "type": "iframe", "url": "https://frame.test", "title": "Frame"},
		}}, nil
	})
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": "frame"}}, nil
	})
	c := connectCdp(t, f)
	parent := c.TabManager.GetTab("T1")
	parent.SetActiveFrame("F-OOPIF")

	raw, err := c.Evaluate("T1", "location.href", true)
	if err != nil || string(raw) != `"frame"` {
		t.Fatalf("OOPIF Evaluate raw=%s err=%v", raw, err)
	}
	frameSessionValue, ok := c.sessions.Load("F-OOPIF")
	if !ok {
		t.Fatal("OOPIF was not attached on demand")
	}
	frameSession := frameSessionValue.(string)
	if c.TabManager.GetTab("F-OOPIF") != nil {
		t.Fatal("OOPIF session was incorrectly registered as a page tab")
	}
	usedFrameSession := false
	for _, call := range f.Calls() {
		if call.Method == "Runtime.evaluate" && call.SessionID == frameSession {
			usedFrameSession = true
		}
	}
	if !usedFrameSession {
		t.Fatalf("Evaluate did not use OOPIF session %v, calls=%+v", frameSession, f.Calls())
	}
}

func TestCdpReadLoopDefaultResultAndSessionError(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Write([]byte(`{"webSocketDebuggerUrl":"ws` + strings.TrimPrefix(srv.URL, "http") + `/ws"}`))
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				ID        int64  `json:"id"`
				Method    string `json:"method"`
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(data, &msg)
			resp := map[string]interface{}{"id": msg.ID}
			if msg.SessionID != "" {
				resp["sessionId"] = msg.SessionID
			}
			switch msg.Method {
			case "Target.attachToTarget":
				resp["result"] = map[string]interface{}{"sessionId": "S1"}
			case "Runtime.evaluate":
				resp["error"] = map[string]interface{}{"message": "runtime failed"}
			case "Target.getTargets":
				resp["result"] = map[string]interface{}{"targetInfos": []interface{}{}}
			default:
				// No result and no error exercises the default "{}" response path.
			}
			raw, _ := json.Marshal(resp)
			_ = ws.WriteMessage(websocket.TextMessage, raw)
		}
	}))
	defer srv.Close()
	host, port, _ := splitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	c := NewCdpConnection(host, port, NewTabStateManager())
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect custom server: %v", err)
	}
	defer c.Disconnect()
	if _, err := c.SessionCommandWithTimeout("T1", "Runtime.evaluate", nil, time.Second); err == nil || !strings.Contains(err.Error(), "runtime failed") {
		t.Fatalf("session error = %v", err)
	}
}
