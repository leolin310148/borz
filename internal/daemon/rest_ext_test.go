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
	"github.com/leolin310148/borz/internal/daemon/extbridge"
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

func TestExt_GenericFeatureRoutesRoundTrip(t *testing.T) {
	s, srv := startRouted(t)
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	type expected struct {
		Method string
		Result json.RawMessage
		Check  func(t *testing.T, params map[string]any)
	}
	expect := make(chan expected, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for exp := range expect {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var in map[string]any
			_ = json.Unmarshal(raw, &in)
			if in["method"] != exp.Method {
				t.Errorf("method=%v want %s", in["method"], exp.Method)
			}
			var params map[string]any
			if p, ok := in["params"].(map[string]any); ok {
				params = p
			} else {
				params = map[string]any{}
			}
			if exp.Check != nil {
				exp.Check(t, params)
			}
			out, _ := json.Marshal(map[string]any{
				"type":   "response",
				"id":     in["id"],
				"result": exp.Result,
			})
			_ = c.WriteMessage(websocket.TextMessage, out)
		}
	}()

	doGet := func(path string) any {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s status=%d", path, resp.StatusCode)
		}
		var body any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return body
	}
	doPost := func(path, body string) any {
		t.Helper()
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("POST %s status=%d", path, resp.StatusCode)
		}
		var out any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}

	expect <- expected{Method: "capabilities", Result: json.RawMessage(`{"ok":true,"supportedMethods":["bookmarks.search"]}`)}
	if body := doGet("/v1/ext/capabilities").(map[string]any); body["ok"] != true {
		t.Fatalf("capabilities body=%v", body)
	}

	expect <- expected{
		Method: "bookmarks.search",
		Result: json.RawMessage(`[{"id":"1","title":"GitHub"}]`),
		Check: func(t *testing.T, params map[string]any) {
			if params["query"] != "git" {
				t.Fatalf("bookmark query params=%v", params)
			}
		},
	}
	if body := doGet("/v1/bookmarks/search?q=git").([]any); len(body) != 1 {
		t.Fatalf("bookmark search body=%v", body)
	}

	expect <- expected{
		Method: "history.search",
		Result: json.RawMessage(`[{"url":"https://github.com","visitCount":2}]`),
		Check: func(t *testing.T, params map[string]any) {
			if params["text"] != "git" || params["maxResults"] != float64(5) {
				t.Fatalf("history params=%v", params)
			}
		},
	}
	if body := doGet("/v1/browser-history/search?q=git&maxResults=5").([]any); len(body) != 1 {
		t.Fatalf("history search body=%v", body)
	}

	expect <- expected{
		Method: "downloads.download",
		Result: json.RawMessage(`7`),
		Check: func(t *testing.T, params map[string]any) {
			if params["url"] != "https://example.com/file.zip" {
				t.Fatalf("download params=%v", params)
			}
		},
	}
	if body := doPost("/v1/downloads/download", `{"url":"https://example.com/file.zip"}`).(float64); body != 7 {
		t.Fatalf("download body=%v", body)
	}

	expect <- expected{
		Method: "windows.update",
		Result: json.RawMessage(`{"id":9,"focused":true}`),
		Check: func(t *testing.T, params map[string]any) {
			if params["id"] != float64(9) {
				t.Fatalf("window params=%v", params)
			}
		},
	}
	if body := doPost("/v1/windows/update", `{"id":9,"updateInfo":{"focused":true}}`).(map[string]any); body["focused"] != true {
		t.Fatalf("window body=%v", body)
	}

	expect <- expected{
		Method: "tabs.query",
		Result: json.RawMessage(`[{"id":3}]`),
		Check: func(t *testing.T, params map[string]any) {
			if params["active"] != true {
				t.Fatalf("tabs query params=%v", params)
			}
		},
	}
	if body := doGet("/v1/ext/tabs/query?active=true").([]any); len(body) != 1 {
		t.Fatalf("tabs query body=%v", body)
	}

	close(expect)
	<-done
}

func TestExt_GenericFeatureRoutesAdditionalParams(t *testing.T) {
	s, srv := startRouted(t)
	c := dialExt(t, srv)
	waitConnected(t, s, 1)

	type expected struct {
		Method string
		Check  func(t *testing.T, params map[string]any)
		Result string
	}
	expect := make(chan expected, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for exp := range expect {
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var in map[string]any
			_ = json.Unmarshal(raw, &in)
			if in["method"] != exp.Method {
				t.Errorf("method=%v want %s", in["method"], exp.Method)
			}
			params, _ := in["params"].(map[string]any)
			if params == nil {
				params = map[string]any{}
			}
			exp.Check(t, params)
			out, _ := json.Marshal(map[string]any{
				"type":   "response",
				"id":     in["id"],
				"result": json.RawMessage(exp.Result),
			})
			_ = c.WriteMessage(websocket.TextMessage, out)
		}
	}()

	getJSON := func(path string) map[string]any {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}

	expect <- expected{
		Method: "history.search",
		Result: `{"ok":true}`,
		Check: func(t *testing.T, params map[string]any) {
			if params["startTime"] != float64(10.5) || params["endTime"] != float64(20.25) || params["limit"] != float64(4) {
				t.Fatalf("history params=%v", params)
			}
		},
	}
	_ = getJSON("/v1/browser-history/search?startTime=10.5&endTime=20.25&limit=4")

	expect <- expected{
		Method: "downloads.search",
		Result: `{"ok":true}`,
		Check: func(t *testing.T, params map[string]any) {
			if params["id"] != float64(7) || params["limit"] != float64(8) || params["totalBytesGreater"] != float64(100) || params["totalBytesLess"] != float64(200) {
				t.Fatalf("download numeric params=%v", params)
			}
			if params["filename"] != "a.zip" || params["paused"] != "false" {
				t.Fatalf("download filter params=%v", params)
			}
		},
	}
	_ = getJSON("/v1/downloads/search?id=7&limit=8&totalBytesGreater=100&totalBytesLess=200&filename=a.zip&paused=false")

	expect <- expected{
		Method: "windows.getAll",
		Result: `null`,
		Check: func(t *testing.T, params map[string]any) {
			if params["populate"] != false {
				t.Fatalf("window params=%v", params)
			}
		},
	}
	if body := getJSON("/v1/windows?populate=false"); body["ok"] != true {
		t.Fatalf("null response should become ok envelope, got %v", body)
	}

	close(expect)
	<-done
}

func TestExt_GenericCallValidation(t *testing.T) {
	_, srv := startRouted(t)
	resp, err := http.Post(srv.URL+"/v1/ext/call", "application/json", strings.NewReader(`{"params":{}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", resp.StatusCode)
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
	for _, p := range []string{"/v1/cookies/all", "/v1/tabs/events", "/v1/bookmarks/tree"} {
		resp, err := http.Post(srv.URL+p, "application/json", nil)
		if err != nil {
			t.Fatalf("post %s: %v", p, err)
		}
		if resp.StatusCode != 405 {
			t.Fatalf("POST %s: status=%d want 405", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
	for _, p := range []string{"/v1/bookmarks/create", "/v1/ext/call"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("get %s: %v", p, err)
		}
		if resp.StatusCode != 405 {
			t.Fatalf("GET %s: status=%d want 405", p, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp, err := http.Post(srv.URL+"/v1/bookmarks/create", "application/json", strings.NewReader(`{`))
	if err != nil {
		t.Fatalf("post invalid json: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestExtHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	body, err := readExtBody(req)
	if err != nil {
		t.Fatalf("empty body err=%v", err)
	}
	if len(body) != 0 {
		t.Fatalf("empty body = %+v", body)
	}
	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{`))
	if _, err := readExtBody(req); err == nil {
		t.Fatal("expected invalid JSON error")
	}
	req = httptest.NewRequest(http.MethodGet, "/x?q=needle&limit=5&n=1.25&empty=", nil)
	filter := queryFilter(req, "q", "empty", "missing")
	if filter["q"] != "needle" || len(filter) != 1 {
		t.Fatalf("filter = %+v", filter)
	}
	copyQueryInt(filter, req.URL.Query(), "limit")
	copyQueryFloat(filter, req.URL.Query(), "n")
	copyQueryInt(filter, req.URL.Query(), "bad")
	copyQueryFloat(filter, req.URL.Query(), "bad")
	if filter["limit"] != 5 || filter["n"] != 1.25 {
		t.Fatalf("numeric filter = %+v", filter)
	}

	if extErrStatus(extbridge.ErrNoClient) != http.StatusServiceUnavailable {
		t.Fatal("ErrNoClient should map to 503")
	}
	if extErrStatus(extbridge.ErrTimeout) != http.StatusGatewayTimeout {
		t.Fatal("ErrTimeout should map to 504")
	}
	if extErrStatus(errors.New("boom")) != http.StatusBadGateway {
		t.Fatal("generic extension errors should map to 502")
	}
}
