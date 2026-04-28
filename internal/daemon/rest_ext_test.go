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

// startRouted spins up the ext routes on an httptest server with no auth so
// tests can exercise both the WS endpoint and the REST endpoints together.
func startRouted(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := newTestServer(t, "")
	mux := http.NewServeMux()
	s.registerExtRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

func dialExt(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ext/ws"
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func waitConnected(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.extHub.Connected() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Connected=%d want %d", s.extHub.Connected(), want)
}

func TestExt_StatusEndpoint(t *testing.T) {
	_, srv := startRouted(t)
	resp, err := http.Get(srv.URL + "/v1/ext/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["connected"].(float64) != 0 {
		t.Fatalf("connected=%v want 0", body["connected"])
	}
}

func TestExt_CookiesAll_NoExtension(t *testing.T) {
	_, srv := startRouted(t)
	resp, err := http.Get(srv.URL + "/v1/cookies/all")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
}

func TestExt_CookiesAll_RoundTrip(t *testing.T) {
	s, srv := startRouted(t)
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	// Mock extension responder.
	go func() {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		out, _ := json.Marshal(map[string]any{
			"type":   "response",
			"id":     in["id"],
			"result": []map[string]any{{"name": "session", "domain": ".example.com"}},
		})
		_ = c.WriteMessage(websocket.TextMessage, out)
	}()

	resp, err := http.Get(srv.URL + "/v1/cookies/all")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var cookies []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cookies); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cookies) != 1 || cookies[0]["name"] != "session" {
		t.Fatalf("cookies=%+v", cookies)
	}
}

func TestExt_TabsEvents(t *testing.T) {
	s, srv := startRouted(t)
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	push := func(name, data string) {
		ev, _ := json.Marshal(map[string]any{
			"type": "event",
			"name": name,
			"data": json.RawMessage(data),
		})
		_ = c.WriteMessage(websocket.TextMessage, ev)
	}
	push("tabs.created", `{"id":7}`)
	push("tabs.removed", `{"id":7}`)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && s.extHub.LatestSeq() < 2 {
		time.Sleep(5 * time.Millisecond)
	}

	resp, err := http.Get(srv.URL + "/v1/tabs/events")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Events []struct {
			Seq  uint64
			Name string
		}
		LatestSeq uint64 `json:"latest_seq"`
		Connected bool
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 2 || body.LatestSeq != 2 || !body.Connected {
		t.Fatalf("body=%+v", body)
	}

	// since cursor.
	resp2, err := http.Get(srv.URL + "/v1/tabs/events?since=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp2.Body.Close()
	var body2 struct {
		Events []struct {
			Seq  uint64
			Name string
		}
	}
	_ = json.NewDecoder(resp2.Body).Decode(&body2)
	if len(body2.Events) != 1 || body2.Events[0].Name != "tabs.removed" {
		t.Fatalf("since cursor wrong: %+v", body2)
	}

	// Bad since.
	resp3, _ := http.Get(srv.URL + "/v1/tabs/events?since=abc")
	if resp3.StatusCode != 400 {
		t.Fatalf("bad since status=%d", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestExt_MethodNotAllowed(t *testing.T) {
	_, srv := startRouted(t)
	for _, p := range []string{"/v1/cookies/all", "/v1/tabs/events"} {
		resp, err := http.Post(srv.URL+p, "application/json", nil)
		if err != nil {
			t.Fatalf("post %s: %v", p, err)
		}
		if resp.StatusCode != 405 {
			t.Fatalf("POST %s: status=%d want 405", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
