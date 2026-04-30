package daemon

import (
	"encoding/json"
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
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "invalid CDP") || c.LastError == "" {
		t.Fatalf("bad json Connect err=%v last=%q", err, c.LastError)
	}

	missingWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer missingWS.Close()
	host, port, _ = splitHostPort(strings.TrimPrefix(missingWS.URL, "http://"))
	c = NewCdpConnection(host, port, tabs)
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "missing") || c.LastError == "" {
		t.Fatalf("missing ws Connect err=%v last=%q", err, c.LastError)
	}

	dialFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"webSocketDebuggerUrl":"ws://127.0.0.1:1/ws"}`))
	}))
	defer dialFail.Close()
	host, port, _ = splitHostPort(strings.TrimPrefix(dialFail.URL, "http://"))
	c = NewCdpConnection(host, port, tabs)
	if err := c.Connect(); err == nil || !strings.Contains(err.Error(), "WebSocket") || c.LastError == "" {
		t.Fatalf("dial fail Connect err=%v last=%q", err, c.LastError)
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
	tab.ActiveFrameID = "FRAME1"
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
