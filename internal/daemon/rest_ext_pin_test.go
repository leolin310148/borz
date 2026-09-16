package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/daemon/extbridge"
)

// startPinRouted wires a Server to both a fake CDP (so tab references resolve
// to a real target) and the ext routes (so the extension bridge can answer).
// pages is a list of {targetId, url, title} triples.
func startPinRouted(t *testing.T, pages [][3]string) (*Server, *httptest.Server) {
	t.Helper()
	f := newFakeCDP(t)
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		infos := make([]interface{}, 0, len(pages))
		for _, p := range pages {
			infos = append(infos, map[string]interface{}{
				"targetId": p[0], "type": "page", "url": p[1], "title": p[2],
			})
		}
		return map[string]interface{}{"targetInfos": infos}, nil
	})

	tabs := NewTabStateManager()
	cdp := NewCdpConnection(f.Host(), f.Port(), tabs)
	if err := cdp.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(cdp.Disconnect)
	time.Sleep(20 * time.Millisecond)

	s := &Server{
		opts:      ServerOptions{Host: "127.0.0.1", Port: 0, Profile: "default"},
		cdp:       cdp,
		extHub:    extbridge.NewHub(),
		startTime: time.Now(),
	}
	mux := http.NewServeMux()
	s.registerExtRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// extResponder answers the next n bridge requests, recording each one. The
// result for a request is whatever results[method] holds.
func extResponder(t *testing.T, c *websocket.Conn, results map[string]any, n int) <-chan []map[string]any {
	t.Helper()
	out := make(chan []map[string]any, 1)
	go func() {
		var seen []map[string]any
		for i := 0; i < n; i++ {
			_, raw, err := c.ReadMessage()
			if err != nil {
				break
			}
			var in map[string]any
			_ = json.Unmarshal(raw, &in)
			seen = append(seen, in)
			method, _ := in["method"].(string)
			reply, _ := json.Marshal(map[string]any{
				"type":   "response",
				"id":     in["id"],
				"result": results[method],
			})
			_ = c.WriteMessage(websocket.TextMessage, reply)
		}
		out <- seen
	}()
	return out
}

func postPin(t *testing.T, srv *httptest.Server, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/ext/tabs/pin", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestExtTabPin_RoundTripPinsMatchingTab(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://teams.example/", "Teams"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	seen := extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{
			{"id": 7, "url": "https://other.example/", "title": "Other"},
			{"id": 42, "url": "https://teams.example/", "title": "Teams", "active": true},
		},
		"tabs.update": map[string]any{"id": 42, "pinned": true},
	}, 2)

	resp, body := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if body["pinned"] != true || body["id"].(float64) != 42 {
		t.Fatalf("body=%+v", body)
	}
	if body["url"] != "https://teams.example/" || body["title"] != "Teams" {
		t.Fatalf("body=%+v", body)
	}
	if body["tab"] != s.cdp.TabManager.GetShortID("T1") {
		t.Fatalf("tab=%v want short id for T1", body["tab"])
	}

	calls := <-seen
	if len(calls) != 2 || calls[0]["method"] != "tabs.query" || calls[1]["method"] != "tabs.update" {
		t.Fatalf("calls=%+v", calls)
	}
	params := calls[1]["params"].(map[string]any)
	if params["id"].(float64) != 42 {
		t.Fatalf("update id=%v", params["id"])
	}
	if params["updateProperties"].(map[string]any)["pinned"] != true {
		t.Fatalf("updateProperties=%+v", params["updateProperties"])
	}
}

func TestExtTabPin_UnpinPassesPinnedFalse(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://teams.example/", "Teams"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	seen := extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{
			{"id": 42, "url": "https://teams.example/", "active": true},
		},
		"tabs.update": map[string]any{"id": 42},
	}, 2)

	resp, body := postPin(t, srv, `{"pinned":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if body["pinned"] != false {
		t.Fatalf("body=%+v", body)
	}
	calls := <-seen
	props := calls[1]["params"].(map[string]any)["updateProperties"].(map[string]any)
	if props["pinned"] != false {
		t.Fatalf("updateProperties=%+v", props)
	}
}

func TestExtTabPin_ResolvesExplicitTabRef(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{
		{"T1", "https://a.example/", "A"},
		{"T2", "https://b.example/", "B"},
	})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{
			{"id": 1, "url": "https://a.example/", "active": true},
			{"id": 2, "url": "https://b.example/"},
		},
		"tabs.update": map[string]any{"id": 2},
	}, 2)

	// Index 1 is the second page, so the b.example tab must be the one pinned
	// even though a.example is the active one.
	resp, body := postPin(t, srv, `{"tab":"1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if body["id"].(float64) != 2 || body["url"] != "https://b.example/" {
		t.Fatalf("body=%+v", body)
	}
}

func TestExtTabPin_UnknownTabRefIs404(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://a.example/", "A"}})
	dialExt(t, srv)
	waitConnected(t, s, 1)

	resp, body := postPin(t, srv, `{"tab":"nope"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
}

func TestExtTabPin_ExtensionSeesNoMatchingTab(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://gone.example/", "Gone"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{{"id": 1, "url": "https://other.example/"}},
	}, 1)

	resp, body := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if !strings.Contains(body["error"].(string), "https://gone.example/") {
		t.Fatalf("error=%v", body["error"])
	}
}

func TestExtTabPin_AmbiguousURLWithNoActiveTabIsRejected(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://dup.example/", "Dup"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{
			{"id": 1, "url": "https://dup.example/"},
			{"id": 2, "url": "https://dup.example/"},
		},
	}, 1)

	resp, body := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if body["url"] != "https://dup.example/" {
		t.Fatalf("body=%+v", body)
	}
	if len(body["candidates"].([]any)) != 2 {
		t.Fatalf("candidates=%+v", body["candidates"])
	}
}

func TestExtTabPin_AmbiguousURLPrefersActiveTab(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://dup.example/", "Dup"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	extResponder(t, c, map[string]any{
		"tabs.query": []map[string]any{
			{"id": 1, "url": "https://dup.example/"},
			{"id": 2, "url": "https://dup.example/", "active": true},
		},
		"tabs.update": map[string]any{"id": 2},
	}, 2)

	resp, body := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
	if body["id"].(float64) != 2 {
		t.Fatalf("body=%+v", body)
	}
}

func TestExtTabPin_NoExtensionIs503(t *testing.T) {
	_, srv := startPinRouted(t, [][3]string{{"T1", "https://a.example/", "A"}})
	resp, _ := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
}

func TestExtTabPin_RejectsNonPostAndBadBody(t *testing.T) {
	_, srv := startPinRouted(t, [][3]string{{"T1", "https://a.example/", "A"}})

	resp, err := http.Get(srv.URL + "/v1/ext/tabs/pin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", resp.StatusCode)
	}

	bad, _ := postPin(t, srv, `{not json`)
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", bad.StatusCode)
	}
}

func TestExtTabPin_UnreadableExtensionTabListIs502(t *testing.T) {
	s, srv := startPinRouted(t, [][3]string{{"T1", "https://a.example/", "A"}})
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	// tabs.query answering with an object instead of an array.
	extResponder(t, c, map[string]any{"tabs.query": map[string]any{"oops": true}}, 1)

	resp, body := postPin(t, srv, `{}`)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d body=%+v", resp.StatusCode, body)
	}
}
