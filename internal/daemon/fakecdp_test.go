package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeCDP runs an in-process CDP server that speaks the CDP JSON-RPC flat
// protocol (id, method, sessionId, result/error) over a WebSocket. Tests
// register method handlers and then drive DispatchRequest against a
// CdpConnection connected to this server.
type fakeCDP struct {
	t        *testing.T
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu       sync.Mutex
	handlers map[string]fakeHandler
	ws       *websocket.Conn
	sessions map[string]bool // session ids we've issued via attachToTarget

	calls  []fakeCall
	closed atomic.Bool
}

type fakeHandler func(params json.RawMessage) (interface{}, error)

type fakeCall struct {
	Method    string
	SessionID string
	Params    json.RawMessage
}

func newFakeCDP(t *testing.T) *fakeCDP {
	t.Helper()
	f := &fakeCDP{
		t:        t,
		handlers: make(map[string]fakeHandler),
		sessions: make(map[string]bool),
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		wsURL := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/ws"
		fmt.Fprintf(w, `{"webSocketDebuggerUrl":%q}`, wsURL)
	})
	mux.HandleFunc("/ws", f.handleWS)

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.Close)

	// Built-in responses for boot-time commands.
	f.On("Target.setDiscoverTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetInfos": []interface{}{}}, nil
	})
	// Every attachToTarget synthesizes a fresh sessionId.
	var sessionCounter int32
	f.On("Target.attachToTarget", func(json.RawMessage) (interface{}, error) {
		sid := fmt.Sprintf("S%d", atomic.AddInt32(&sessionCounter, 1))
		f.mu.Lock()
		f.sessions[sid] = true
		f.mu.Unlock()
		return map[string]interface{}{"sessionId": sid}, nil
	})
	// Domain enable commands succeed with empty result.
	for _, m := range []string{"Page.enable", "Runtime.enable", "Network.enable", "DOM.enable", "Accessibility.enable"} {
		f.On(m, func(json.RawMessage) (interface{}, error) { return map[string]interface{}{}, nil })
	}
	// Default Runtime.evaluate returns a ready document on a non-blank URL so
	// the post-tab_new readiness probe (waitForTabNavigated) returns
	// immediately instead of polling its full timeout. Tests that exercise
	// real Runtime.evaluate behavior override this with their own handler.
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{
				"type":  "string",
				"value": `{"readyState":"complete","href":"https://ready.test/"}`,
			},
		}, nil
	})

	return f
}

func (f *fakeCDP) Close() {
	if f.closed.Swap(true) {
		return
	}
	f.mu.Lock()
	if f.ws != nil {
		f.ws.Close()
	}
	f.mu.Unlock()
	f.server.Close()
}

// On registers a handler for a CDP method. Overrides the default if any.
func (f *fakeCDP) On(method string, h fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

// Host/Port extracted from the httptest server URL.
func (f *fakeCDP) Host() string {
	host, _, _ := splitHostPort(strings.TrimPrefix(f.server.URL, "http://"))
	return host
}

func (f *fakeCDP) Port() int {
	_, port, _ := splitHostPort(strings.TrimPrefix(f.server.URL, "http://"))
	return port
}

func (f *fakeCDP) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]fakeCall, len(f.calls))
	copy(cp, f.calls)
	return cp
}

func splitHostPort(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, 0, fmt.Errorf("bad addr")
	}
	host := s[:i]
	var port int
	fmt.Sscanf(s[i+1:], "%d", &port)
	return host, port, nil
}

func (f *fakeCDP) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.ws = conn
	f.mu.Unlock()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			SessionID string          `json:"sessionId"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{Method: msg.Method, SessionID: msg.SessionID, Params: msg.Params})
		h, ok := f.handlers[msg.Method]
		f.mu.Unlock()

		resp := map[string]interface{}{"id": msg.ID}
		if msg.SessionID != "" {
			resp["sessionId"] = msg.SessionID
		}

		if !ok {
			resp["result"] = map[string]interface{}{}
		} else {
			result, herr := h(msg.Params)
			if herr != nil {
				resp["error"] = map[string]interface{}{"message": herr.Error()}
			} else {
				resp["result"] = result
			}
		}

		raw, _ := json.Marshal(resp)
		f.mu.Lock()
		err = conn.WriteMessage(websocket.TextMessage, raw)
		f.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// connectCdp builds a CdpConnection, points it at the fake, and waits for
// Connect() to finish. Returns the connection plus a cleanup.
func connectCdp(t *testing.T, f *fakeCDP) *CdpConnection {
	t.Helper()
	tabs := NewTabStateManager()
	c := NewCdpConnection(f.Host(), f.Port(), tabs)

	if err := c.Connect(); err != nil {
		t.Fatalf("fake CDP connect: %v", err)
	}
	t.Cleanup(func() { c.Disconnect() })

	// Give the reader loop a tick to settle, mostly for tests that send
	// unsolicited events immediately after connecting.
	time.Sleep(10 * time.Millisecond)
	return c
}
