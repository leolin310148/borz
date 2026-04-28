package extbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startTestHub spins up a Hub on an httptest server and returns it plus a
// helper that dials a WebSocket client.
func startTestHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.ServeWS))
	t.Cleanup(srv.Close)
	return hub, srv
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func waitConnected(t *testing.T, h *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.Connected() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Connected()=%d, want %d", h.Connected(), want)
}

func TestHub_RequestNoClient(t *testing.T) {
	h := NewHub()
	if _, err := h.Request("anything", nil, 50*time.Millisecond); err != ErrNoClient {
		t.Fatalf("err=%v, want ErrNoClient", err)
	}
}

func TestHub_RequestRoundTrip(t *testing.T) {
	hub, srv := startTestHub(t)
	c := dial(t, srv)
	waitConnected(t, hub, 1)

	// Mock extension: read request, echo a response.
	go func() {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var in wireMessage
		_ = json.Unmarshal(raw, &in)
		if in.Type != "request" || in.Method != "cookies.getAll" {
			t.Errorf("unexpected request: %+v", in)
		}
		resp := wireMessage{Type: "response", ID: in.ID, Result: json.RawMessage(`[{"name":"a"}]`)}
		out, _ := json.Marshal(resp)
		_ = c.WriteMessage(websocket.TextMessage, out)
	}()

	result, err := hub.Request("cookies.getAll", nil, time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if string(result) != `[{"name":"a"}]` {
		t.Fatalf("result=%s", string(result))
	}
}

func TestHub_RequestErrorPropagates(t *testing.T) {
	hub, srv := startTestHub(t)
	c := dial(t, srv)
	waitConnected(t, hub, 1)

	go func() {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var in wireMessage
		_ = json.Unmarshal(raw, &in)
		resp := wireMessage{Type: "response", ID: in.ID, Error: "boom"}
		out, _ := json.Marshal(resp)
		_ = c.WriteMessage(websocket.TextMessage, out)
	}()

	if _, err := hub.Request("x", nil, time.Second); err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v, want boom", err)
	}
}

func TestHub_RequestTimeout(t *testing.T) {
	hub, srv := startTestHub(t)
	_ = dial(t, srv) // connected but mute
	waitConnected(t, hub, 1)

	if _, err := hub.Request("x", nil, 80*time.Millisecond); err != ErrTimeout {
		t.Fatalf("err=%v, want ErrTimeout", err)
	}
}

func TestHub_EventsRingAndSinceCursor(t *testing.T) {
	hub, srv := startTestHub(t)
	c := dial(t, srv)
	waitConnected(t, hub, 1)

	send := func(name, data string) {
		ev := wireMessage{Type: "event", Name: name, Data: json.RawMessage(data)}
		raw, _ := json.Marshal(ev)
		_ = c.WriteMessage(websocket.TextMessage, raw)
	}

	send("tabs.created", `{"id":1}`)
	send("tabs.created", `{"id":2}`)
	send("tabs.removed", `{"id":1}`)

	// Wait for events to be ingested.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && hub.LatestSeq() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	if hub.LatestSeq() != 3 {
		t.Fatalf("LatestSeq=%d, want 3", hub.LatestSeq())
	}

	all := hub.Events(0)
	if len(all) != 3 {
		t.Fatalf("len(all)=%d, want 3", len(all))
	}
	if all[0].Name != "tabs.created" || all[2].Name != "tabs.removed" {
		t.Fatalf("ordering off: %+v", all)
	}
	tail := hub.Events(all[1].Seq)
	if len(tail) != 1 || tail[0].Name != "tabs.removed" {
		t.Fatalf("since cursor wrong: %+v", tail)
	}
}

func TestHub_DisconnectClearsClient(t *testing.T) {
	hub, srv := startTestHub(t)
	c := dial(t, srv)
	waitConnected(t, hub, 1)

	_ = c.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hub.Connected() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Connected() != 0 {
		t.Fatalf("Connected=%d after close", hub.Connected())
	}
	if _, err := hub.Request("x", nil, 50*time.Millisecond); err != ErrNoClient {
		t.Fatalf("err=%v, want ErrNoClient", err)
	}
}

func TestHub_ConcurrentRequests(t *testing.T) {
	hub, srv := startTestHub(t)
	c := dial(t, srv)
	waitConnected(t, hub, 1)

	// Echo every request with its ID baked into result so we can verify
	// responses don't get crossed.
	var writeMu sync.Mutex
	go func() {
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var in wireMessage
			_ = json.Unmarshal(raw, &in)
			resp := wireMessage{
				Type:   "response",
				ID:     in.ID,
				Result: json.RawMessage(`"` + in.ID + `"`),
			}
			out, _ := json.Marshal(resp)
			writeMu.Lock()
			_ = c.WriteMessage(websocket.TextMessage, out)
			writeMu.Unlock()
		}
	}()

	const N = 20
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := hub.Request("x", nil, 2*time.Second); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent request: %v", err)
	}
}
