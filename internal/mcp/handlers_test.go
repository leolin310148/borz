package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/site"
	"github.com/mark3labs/mcp-go/mcp"
)

// stubSiteLister replaces siteLister for the duration of a test.
func stubSiteLister(t *testing.T, fn func() []*site.SiteMeta) {
	t.Helper()
	orig := siteLister
	siteLister = fn
	t.Cleanup(func() { siteLister = orig })
}

// stubSiteFinder replaces siteFinder for the duration of a test.
func stubSiteFinder(t *testing.T, fn func(string) *site.SiteMeta) {
	t.Helper()
	orig := siteFinder
	siteFinder = fn
	t.Cleanup(func() { siteFinder = orig })
}

// stubSiteBuilder replaces siteBuilder for the duration of a test.
func stubSiteBuilder(t *testing.T, fn func(*site.SiteMeta, map[string]interface{}, string) (*protocol.Request, error)) {
	t.Helper()
	orig := siteBuilder
	siteBuilder = fn
	t.Cleanup(func() { siteBuilder = orig })
}

// stubSend swaps sendCommand for the duration of a test.
func stubSend(t *testing.T, fn func(*protocol.Request) (*protocol.Response, error)) {
	t.Helper()
	orig := sendCommand
	sendCommand = fn
	t.Cleanup(func() { sendCommand = orig })
}

// capturingSend records the request and returns a preset response.
type capture struct {
	req  *protocol.Request
	resp *protocol.Response
	err  error
}

func capturingSend(t *testing.T, resp *protocol.Response) *capture {
	t.Helper()
	c := &capture{resp: resp}
	stubSend(t, func(r *protocol.Request) (*protocol.Response, error) {
		c.req = r
		return c.resp, c.err
	})
	return c
}

func mkReq(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func ok() *protocol.Response { return &protocol.Response{Success: true} }

func extBridgeDaemon(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	u := strings.TrimPrefix(ts.URL, "http://")
	host, portStr, err := net.SplitHostPort(u)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	data, _ := json.Marshal(protocol.DaemonInfo{PID: os.Getpid(), Host: host, Port: port})
	if err := os.WriteFile(filepath.Join(home, "daemon.json"), data, 0o600); err != nil {
		t.Fatalf("write daemon.json: %v", err)
	}
}

// --- helpers ---

func TestNormalizeRef(t *testing.T) {
	if normalizeRef("@5") != "5" {
		t.Error("strip @ prefix")
	}
	if normalizeRef("5") != "5" {
		t.Error("no-op without @")
	}
}

func TestIntPtr(t *testing.T) {
	if p := intPtr(7); p == nil || *p != 7 {
		t.Errorf("intPtr(7) = %v", p)
	}
}

func TestNewID_HexLength(t *testing.T) {
	a := newID()
	if len(a) != 16 {
		t.Errorf("len = %d, want 16 hex chars", len(a))
	}
	// likely unique across consecutive calls
	if a == newID() {
		t.Error("two consecutive newIDs are equal — RNG broken?")
	}
}

func TestSetTab(t *testing.T) {
	req := &protocol.Request{}
	setTab(req, mkReq(map[string]any{"tab": "t1"}))
	if req.TabID != "t1" {
		t.Errorf("TabID = %v", req.TabID)
	}

	req = &protocol.Request{}
	setTab(req, mkReq(nil))
	if req.TabID != nil {
		t.Errorf("TabID should stay nil, got %v", req.TabID)
	}
}

// --- navigation handlers ---

func TestHandleNavigate_MissingURL(t *testing.T) {
	res, _ := handleNavigate(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Fatalf("expected error result")
	}
}

func TestHandleNavigate_Success(t *testing.T) {
	cap := capturingSend(t, ok())
	res, _ := handleNavigate(context.Background(), mkReq(map[string]any{"url": "https://example.com", "new": true, "tab": "t1"}))
	if res.IsError {
		t.Fatalf("unexpected error: %v", res)
	}
	if cap.req.Action != protocol.ActionOpen || cap.req.URL != "https://example.com" || !cap.req.New || cap.req.TabID != "t1" {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestHandleNavigate_SendError(t *testing.T) {
	stubSend(t, func(*protocol.Request) (*protocol.Response, error) {
		return nil, errors.New("down")
	})
	res, _ := handleNavigate(context.Background(), mkReq(map[string]any{"url": "x"}))
	if !res.IsError {
		t.Errorf("expected error, got %v", res)
	}
}

func TestHandleNavigate_CommandFailure(t *testing.T) {
	capturingSend(t, &protocol.Response{Success: false, Error: "boom"})
	res, _ := handleNavigate(context.Background(), mkReq(map[string]any{"url": "x"}))
	if !res.IsError {
		t.Error("expected error result")
	}
}

func TestHandleBackForwardRefreshClose(t *testing.T) {
	cases := []struct {
		name   string
		fn     func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		action protocol.ActionType
	}{
		{"back", handleBack, protocol.ActionBack},
		{"forward", handleForward, protocol.ActionForward},
		{"refresh", handleRefresh, protocol.ActionRefresh},
		{"close", handleClose, protocol.ActionClose},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cap := capturingSend(t, ok())
			res, _ := c.fn(context.Background(), mkReq(map[string]any{"tab": "T"}))
			if res.IsError {
				t.Fatalf("unexpected error: %v", res)
			}
			if cap.req.Action != c.action {
				t.Errorf("action = %v, want %v", cap.req.Action, c.action)
			}
			if cap.req.TabID != "T" {
				t.Errorf("tab = %v", cap.req.TabID)
			}
		})
	}
}

// --- interaction handlers ---

func TestRefRequiredHandlers(t *testing.T) {
	handlers := map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error){
		"click":   handleClick,
		"hover":   handleHover,
		"check":   handleCheck,
		"uncheck": handleUncheck,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			res, _ := h(context.Background(), mkReq(nil))
			if !res.IsError {
				t.Errorf("%s without ref should error", name)
			}
		})
	}
}

func TestHandleClick_NormalizesRefAndSends(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleClick(context.Background(), mkReq(map[string]any{"ref": "@7"}))
	if cap.req.Ref != "7" || cap.req.Action != protocol.ActionClick {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestSimpleRefHandlers_Success(t *testing.T) {
	cases := []struct {
		name   string
		fn     func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		action protocol.ActionType
	}{
		{"hover", handleHover, protocol.ActionHover},
		{"check", handleCheck, protocol.ActionCheck},
		{"uncheck", handleUncheck, protocol.ActionUncheck},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cap := capturingSend(t, ok())
			res, _ := c.fn(context.Background(), mkReq(map[string]any{"ref": "@4", "tab": "t"}))
			if res.IsError {
				t.Fatalf("unexpected error: %v", res)
			}
			if cap.req.Action != c.action || cap.req.Ref != "4" || cap.req.TabID != "t" {
				t.Errorf("req = %+v", cap.req)
			}
		})
	}
}

func TestHandleFill(t *testing.T) {
	// missing ref
	res, _ := handleFill(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Error("missing ref should error")
	}
	// missing text
	res, _ = handleFill(context.Background(), mkReq(map[string]any{"ref": "1"}))
	if !res.IsError {
		t.Error("missing text should error")
	}
	// success
	cap := capturingSend(t, ok())
	_, _ = handleFill(context.Background(), mkReq(map[string]any{"ref": "@9", "text": "hi"}))
	if cap.req.Ref != "9" || cap.req.Text != "hi" || cap.req.Action != protocol.ActionFill {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestHandleType(t *testing.T) {
	res, _ := handleType(context.Background(), mkReq(map[string]any{"ref": "1"}))
	if !res.IsError {
		t.Error("missing text should error")
	}
	cap := capturingSend(t, ok())
	_, _ = handleType(context.Background(), mkReq(map[string]any{"ref": "2", "text": "x"}))
	if cap.req.Action != protocol.ActionType_ || cap.req.Text != "x" {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestHandleSelect(t *testing.T) {
	res, _ := handleSelect(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Error("missing ref")
	}
	res, _ = handleSelect(context.Background(), mkReq(map[string]any{"ref": "1"}))
	if !res.IsError {
		t.Error("missing value")
	}
	cap := capturingSend(t, ok())
	_, _ = handleSelect(context.Background(), mkReq(map[string]any{"ref": "1", "value": "opt"}))
	if cap.req.Value != "opt" {
		t.Errorf("value = %q", cap.req.Value)
	}
}

func TestHandlePress(t *testing.T) {
	res, _ := handlePress(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Error("missing key")
	}
	cap := capturingSend(t, ok())
	_, _ = handlePress(context.Background(), mkReq(map[string]any{"key": "Enter"}))
	if cap.req.Key != "Enter" {
		t.Errorf("key = %q", cap.req.Key)
	}
}

func TestHandleScroll_Defaults(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleScroll(context.Background(), mkReq(nil))
	if cap.req.Direction != "down" {
		t.Errorf("default direction = %q", cap.req.Direction)
	}
	if cap.req.Pixels == nil || *cap.req.Pixels != 300 {
		t.Errorf("default pixels = %v", cap.req.Pixels)
	}
}

func TestHandleScroll_Custom(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleScroll(context.Background(), mkReq(map[string]any{"direction": "up", "pixels": float64(100)}))
	if cap.req.Direction != "up" || *cap.req.Pixels != 100 {
		t.Errorf("req = %+v", cap.req)
	}
}

// --- observation handlers ---

func TestHandleSnapshot(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		SnapshotData: &protocol.SnapshotData{Snapshot: "tree"},
	}})
	_, _ = handleSnapshot(context.Background(), mkReq(map[string]any{
		"interactive": true, "compact": true, "maxDepth": float64(3), "selector": "body",
	}))
	if !cap.req.Interactive || !cap.req.Compact || cap.req.Selector != "body" {
		t.Errorf("req = %+v", cap.req)
	}
	if cap.req.MaxDepth == nil || *cap.req.MaxDepth != 3 {
		t.Errorf("maxDepth = %v", cap.req.MaxDepth)
	}
}

func TestHandleSnapshot_ZeroDepthOmitted(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleSnapshot(context.Background(), mkReq(nil))
	if cap.req.MaxDepth != nil {
		t.Errorf("zero depth should be omitted: %v", cap.req.MaxDepth)
	}
}

func TestHandleScreenshot(t *testing.T) {
	capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{DataURL: "data:image/png;base64,AAA"}})
	res, _ := handleScreenshot(context.Background(), mkReq(nil))
	if res.IsError {
		t.Errorf("unexpected err: %v", res)
	}
}

func TestHandleGet(t *testing.T) {
	res, _ := handleGet(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Error("missing attribute")
	}
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Value: "ok"}})
	_, _ = handleGet(context.Background(), mkReq(map[string]any{"attribute": "text", "ref": "@3"}))
	if cap.req.Attribute != "text" || cap.req.Ref != "3" {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestHandleEval(t *testing.T) {
	res, _ := handleEval(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Error("missing script")
	}
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: "hi"}})
	_, _ = handleEval(context.Background(), mkReq(map[string]any{"script": "1+1"}))
	if cap.req.Script != "1+1" {
		t.Errorf("script = %q", cap.req.Script)
	}
}

func TestHandleWait(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleWait(context.Background(), mkReq(map[string]any{"ms": float64(250)}))
	if cap.req.Ms == nil || *cap.req.Ms != 250 {
		t.Errorf("ms = %v", cap.req.Ms)
	}

	cap = capturingSend(t, ok())
	_, _ = handleWait(context.Background(), mkReq(nil))
	if cap.req.Ms == nil || *cap.req.Ms != 1000 {
		t.Errorf("default ms = %v", cap.req.Ms)
	}
}

// --- tab handlers ---

func TestHandleTabList(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Tabs: []protocol.TabInfo{{Index: 0, URL: "u"}}}})
	res, _ := handleTabList(context.Background(), mkReq(nil))
	if res.IsError {
		t.Errorf("unexpected error")
	}
	if cap.req.Action != protocol.ActionTabList {
		t.Errorf("action = %v", cap.req.Action)
	}
}

func TestHandleTabNew(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleTabNew(context.Background(), mkReq(map[string]any{"url": "https://x"}))
	if cap.req.URL != "https://x" {
		t.Errorf("url = %q", cap.req.URL)
	}

	cap = capturingSend(t, ok())
	res, _ := handleTabNew(context.Background(), mkReq(nil))
	if cap.req.URL != "" {
		t.Errorf("URL should be empty")
	}
	if res.IsError {
		t.Errorf("unexpected error")
	}
}

func TestHandleTabSelect(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleTabSelect(context.Background(), mkReq(map[string]any{"tab": "t1", "index": float64(2)}))
	if cap.req.TabID != "t1" || cap.req.Index == nil || *cap.req.Index != 2 {
		t.Errorf("req = %+v", cap.req)
	}

	cap = capturingSend(t, ok())
	_, _ = handleTabSelect(context.Background(), mkReq(nil))
	if cap.req.TabID != nil || cap.req.Index != nil {
		t.Errorf("empty tab-select should have no fields: %+v", cap.req)
	}
}

func TestHandleTabClose(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleTabClose(context.Background(), mkReq(map[string]any{"tab": "t2", "index": float64(1)}))
	if cap.req.TabID != "t2" || cap.req.Index == nil || *cap.req.Index != 1 {
		t.Errorf("req = %+v", cap.req)
	}
}

// --- diagnostics handlers ---

func TestHandleNetwork_List(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		NetworkRequests: []protocol.NetworkRequestInfo{{URL: "u", Method: "GET", Type: "xhr"}},
	}})
	res, _ := handleNetwork(context.Background(), mkReq(map[string]any{
		"command": "requests", "filter": "f", "withBody": true, "method": "POST", "status": "200",
	}))
	if res.IsError {
		t.Error("unexpected error")
	}
	if cap.req.NetworkCommand != "requests" || !cap.req.WithBody || cap.req.Method != "POST" {
		t.Errorf("req = %+v", cap.req)
	}
}

func TestHandleNetwork_Clear(t *testing.T) {
	capturingSend(t, ok())
	res, _ := handleNetwork(context.Background(), mkReq(map[string]any{"command": "clear"}))
	if !strings.Contains(firstText(t, res), "cleared") {
		t.Errorf("got %q", firstText(t, res))
	}
}

func TestHandleConsole(t *testing.T) {
	// get mode (default)
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		ConsoleMessages: []protocol.ConsoleMessageInfo{{Type: "log", Text: "x"}},
	}})
	res, _ := handleConsole(context.Background(), mkReq(map[string]any{"filter": "f"}))
	if res.IsError || cap.req.ConsoleCommand != "get" {
		t.Errorf("get-mode failed: %v / %+v", res, cap.req)
	}

	// clear mode
	capturingSend(t, ok())
	res, _ = handleConsole(context.Background(), mkReq(map[string]any{"clear": true}))
	if !strings.Contains(firstText(t, res), "cleared") {
		t.Errorf("got %q", firstText(t, res))
	}
}

// --- site adapter handlers ---

func TestHandleSiteList_Empty(t *testing.T) {
	stubSiteLister(t, func() []*site.SiteMeta { return nil })
	res, _ := handleSiteList(context.Background(), mkReq(nil))
	if res.IsError {
		t.Fatalf("unexpected error: %v", res)
	}
	if !strings.Contains(firstText(t, res), "No site adapters") {
		t.Errorf("got %q", firstText(t, res))
	}
}

func TestHandleSiteList_WithAdapters(t *testing.T) {
	stubSiteLister(t, func() []*site.SiteMeta {
		return []*site.SiteMeta{
			{Name: "twitter/search", Description: "search tweets", Domain: "twitter.com", Source: "community"},
			{Name: "custom/one", Description: "local thing", Source: "local"},
		}
	})
	res, _ := handleSiteList(context.Background(), mkReq(nil))
	if res.IsError {
		t.Fatalf("unexpected error")
	}
	got := firstText(t, res)
	if !strings.Contains(got, "Site adapters (2)") {
		t.Errorf("missing header: %q", got)
	}
	if !strings.Contains(got, "twitter/search") || !strings.Contains(got, "search tweets") {
		t.Errorf("missing first adapter: %q", got)
	}
	if !strings.Contains(got, "[local]") || !strings.Contains(got, "[community]") {
		t.Errorf("missing source tags: %q", got)
	}
}

func TestHandleSiteInfo_Missing(t *testing.T) {
	// missing name
	res, _ := handleSiteInfo(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Errorf("missing name should error")
	}

	// adapter not found
	stubSiteFinder(t, func(string) *site.SiteMeta { return nil })
	res, _ = handleSiteInfo(context.Background(), mkReq(map[string]any{"name": "nope"}))
	if !res.IsError {
		t.Errorf("missing adapter should error")
	}
	if !strings.Contains(firstText(t, res), "not found") {
		t.Errorf("got %q", firstText(t, res))
	}
}

func TestHandleSiteInfo_Success(t *testing.T) {
	stubSiteFinder(t, func(name string) *site.SiteMeta {
		if name != "twitter/search" {
			return nil
		}
		return &site.SiteMeta{
			Name: "twitter/search", Description: "search", Domain: "twitter.com",
			Args: map[string]site.ArgDef{"query": {Required: true, Description: "q"}},
		}
	})
	res, _ := handleSiteInfo(context.Background(), mkReq(map[string]any{"name": "twitter/search"}))
	if res.IsError {
		t.Fatalf("unexpected error: %v", res)
	}
	got := firstText(t, res)
	if !strings.Contains(got, `"name": "twitter/search"`) || !strings.Contains(got, `"query"`) {
		t.Errorf("got %q", got)
	}
}

func TestHandleSiteRun_MissingName(t *testing.T) {
	res, _ := handleSiteRun(context.Background(), mkReq(nil))
	if !res.IsError {
		t.Errorf("missing name should error")
	}
}

func TestHandleSiteRun_NotFound(t *testing.T) {
	stubSiteFinder(t, func(string) *site.SiteMeta { return nil })
	res, _ := handleSiteRun(context.Background(), mkReq(map[string]any{"name": "nope"}))
	if !res.IsError || !strings.Contains(firstText(t, res), "not found") {
		t.Errorf("got %+v / %q", res, firstText(t, res))
	}
}

func TestHandleSiteRun_BadArgs(t *testing.T) {
	stubSiteFinder(t, func(string) *site.SiteMeta { return &site.SiteMeta{Name: "a"} })
	res, _ := handleSiteRun(context.Background(), mkReq(map[string]any{"name": "a", "args": "string-not-object"}))
	if !res.IsError || !strings.Contains(firstText(t, res), "args must be an object") {
		t.Errorf("got %+v / %q", res, firstText(t, res))
	}
}

func TestHandleSiteRun_BuilderError(t *testing.T) {
	stubSiteFinder(t, func(string) *site.SiteMeta { return &site.SiteMeta{Name: "a"} })
	stubSiteBuilder(t, func(*site.SiteMeta, map[string]interface{}, string) (*protocol.Request, error) {
		return nil, errors.New("read fail")
	})
	res, _ := handleSiteRun(context.Background(), mkReq(map[string]any{"name": "a"}))
	if !res.IsError || !strings.Contains(firstText(t, res), "read fail") {
		t.Errorf("got %+v / %q", res, firstText(t, res))
	}
}

func TestHandleSiteRun_Success(t *testing.T) {
	stubSiteFinder(t, func(string) *site.SiteMeta { return &site.SiteMeta{Name: "twitter/search"} })

	var gotArgs map[string]interface{}
	var gotTab string
	stubSiteBuilder(t, func(m *site.SiteMeta, args map[string]interface{}, tab string) (*protocol.Request, error) {
		gotArgs = args
		gotTab = tab
		return &protocol.Request{Action: protocol.ActionEval, Script: "dummy", TabID: tab}, nil
	})
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: map[string]interface{}{"hits": 3}}})

	res, _ := handleSiteRun(context.Background(), mkReq(map[string]any{
		"name": "twitter/search",
		"args": map[string]any{"query": "ai"},
		"tab":  "t1",
	}))
	if res.IsError {
		t.Fatalf("unexpected error: %v", res)
	}
	if gotArgs["query"] != "ai" {
		t.Errorf("args not passed: %+v", gotArgs)
	}
	if gotTab != "t1" {
		t.Errorf("tab = %q", gotTab)
	}
	if cap.req.Action != protocol.ActionEval || cap.req.ID == "" {
		t.Errorf("dispatched req = %+v", cap.req)
	}
	if !strings.Contains(firstText(t, res), `"hits": 3`) {
		t.Errorf("result not formatted: %q", firstText(t, res))
	}
}

func TestHandleSiteRun_SendError(t *testing.T) {
	stubSiteFinder(t, func(string) *site.SiteMeta { return &site.SiteMeta{Name: "a"} })
	stubSiteBuilder(t, func(*site.SiteMeta, map[string]interface{}, string) (*protocol.Request, error) {
		return &protocol.Request{Action: protocol.ActionEval}, nil
	})
	stubSend(t, func(*protocol.Request) (*protocol.Response, error) {
		return nil, errors.New("connection refused")
	})
	res, _ := handleSiteRun(context.Background(), mkReq(map[string]any{"name": "a"}))
	if !res.IsError {
		t.Errorf("expected error result")
	}
}

// --- new flag plumbing (waitFor/timeout, snapshot mode, eval auto-await, doctor) ---

func TestHandleNavigate_PassesWaitForAndTimeout(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleNavigate(context.Background(), mkReq(map[string]any{
		"url":     "https://example.com",
		"waitFor": ".loaded",
		"timeout": float64(2500),
	}))
	if cap.req.WaitFor != ".loaded" {
		t.Errorf("waitFor = %q", cap.req.WaitFor)
	}
	if cap.req.TimeoutMs == nil || *cap.req.TimeoutMs != 2500 {
		t.Errorf("timeoutMs = %v", cap.req.TimeoutMs)
	}
}

func TestHandleClick_PassesWaitFor(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleClick(context.Background(), mkReq(map[string]any{
		"ref":     "@7",
		"waitFor": ".modal",
	}))
	if cap.req.WaitFor != ".modal" {
		t.Errorf("waitFor = %q", cap.req.WaitFor)
	}
}

func TestHandleSnapshot_TextOnlySetsMode(t *testing.T) {
	cap := capturingSend(t, ok())
	_, _ = handleSnapshot(context.Background(), mkReq(map[string]any{"textOnly": true}))
	if cap.req.Mode != "text" {
		t.Errorf("mode = %q, want text", cap.req.Mode)
	}
}

func TestHandleEval_AutoWrapsTopLevelAwait(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: "ok"}})
	_, _ = handleEval(context.Background(), mkReq(map[string]any{"script": "await fetch('/x')"}))
	if !strings.Contains(cap.req.Script, "async") {
		t.Errorf("expected auto-wrap to inject async IIFE; got %q", cap.req.Script)
	}
}

func TestHandleEval_NoAutoAwait(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: "ok"}})
	src := "await fetch('/x')"
	_, _ = handleEval(context.Background(), mkReq(map[string]any{"script": src, "noAutoAwait": true}))
	if cap.req.Script != src {
		t.Errorf("expected raw script; got %q", cap.req.Script)
	}
}

func TestHandleDoctor_ReturnsCheckList(t *testing.T) {
	res, err := handleDoctor(context.Background(), mkReq(map[string]any{"json": true}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	out := firstText(t, res)
	if !strings.Contains(out, "\"checks\"") || !strings.Contains(out, "\"ok\"") {
		t.Errorf("expected doctor JSON envelope; got: %s", out)
	}
}

func TestHandleErrors(t *testing.T) {
	cap := capturingSend(t, &protocol.Response{Success: true, Data: &protocol.ResponseData{
		JSErrors: []protocol.JSErrorInfo{{Message: "oops"}},
	}})
	res, _ := handleErrors(context.Background(), mkReq(nil))
	if res.IsError || cap.req.ErrorsCommand != "get" {
		t.Errorf("get-mode failed: %v", res)
	}

	capturingSend(t, ok())
	res, _ = handleErrors(context.Background(), mkReq(map[string]any{"clear": true}))
	if !strings.Contains(firstText(t, res), "cleared") {
		t.Errorf("got %q", firstText(t, res))
	}
}

func TestExtensionBridgeHandlers_ForwardRequests(t *testing.T) {
	type seenRequest struct {
		method string
		path   string
		query  string
		body   map[string]any
	}
	var seen []seenRequest
	extBridgeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write([]byte(`{"running":true}`))
			return
		}
		rec := seenRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		_ = json.NewDecoder(r.Body).Decode(&rec.body)
		seen = append(seen, rec)
		switch r.URL.Path {
		case "/v1/ext/capabilities":
			w.Write([]byte(`{"name":"borz-ext","supportedMethods":["bookmarks.search"]}`))
		case "/v1/ext/call":
			w.Write([]byte(`{"result":{"ok":true}}`))
		case "/v1/bookmarks/tree":
			w.Write([]byte(`[{"id":"root"}]`))
		case "/v1/bookmarks/search":
			w.Write([]byte(`[{"id":"b1"}]`))
		case "/v1/bookmarks/create", "/v1/bookmarks/update", "/v1/bookmarks/remove":
			w.Write([]byte(`{"ok":true}`))
		case "/v1/browser-history/search":
			w.Write([]byte(`[{"id":"h1"}]`))
		case "/v1/browser-history/delete-url":
			w.Write([]byte(`{"ok":true}`))
		case "/v1/downloads/search":
			w.Write([]byte(`[{"id":7}]`))
		case "/v1/downloads/download", "/v1/downloads/erase", "/v1/downloads/cancel", "/v1/downloads/pause", "/v1/downloads/resume", "/v1/downloads/show", "/v1/downloads/show-default-folder":
			w.Write([]byte(`{"ok":true}`))
		case "/v1/windows", "/v1/windows/create", "/v1/windows/update", "/v1/windows/close":
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected path %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	})

	cases := []struct {
		name string
		fn   func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args map[string]any
		want string
	}{
		{"extension status", handleExtensionStatus, nil, "borz-ext"},
		{"extension call", handleExtensionCall, map[string]any{"method": "bookmarks.search", "params": map[string]any{"query": "go"}}, `"ok": true`},
		{"bookmarks tree", handleBookmarks, map[string]any{"command": "tree"}, `"id": "root"`},
		{"bookmarks search", handleBookmarks, map[string]any{"command": "search", "query": "go"}, `"id": "b1"`},
		{"bookmarks create", handleBookmarks, map[string]any{"command": "create", "url": "https://new.test", "title": "New", "parentId": "root"}, `"ok": true`},
		{"bookmarks update", handleBookmarks, map[string]any{"command": "update", "id": "b1", "title": "Updated", "url": "https://updated.test"}, `"ok": true`},
		{"bookmarks remove", handleBookmarks, map[string]any{"command": "remove", "id": "b1", "recursive": true}, `"ok": true`},
		{"history search", handleBrowserHistory, map[string]any{"command": "search", "query": "docs", "limit": 5}, `"id": "h1"`},
		{"history delete", handleBrowserHistory, map[string]any{"command": "deleteUrl", "url": "https://old.test"}, `"ok": true`},
		{"downloads list", handleDownloads, map[string]any{"command": "list", "state": "complete", "limit": 3}, `"id": 7`},
		{"downloads search", handleDownloads, map[string]any{"command": "search", "query": "zip"}, `"id": 7`},
		{"downloads start", handleDownloads, map[string]any{"command": "start", "url": "https://file.test/a.zip", "filename": "a.zip", "saveAs": true}, `"ok": true`},
		{"downloads erase", handleDownloads, map[string]any{"command": "erase", "id": 7, "query": "old"}, `"ok": true`},
		{"downloads cancel", handleDownloads, map[string]any{"command": "cancel", "id": 7}, `"ok": true`},
		{"downloads pause", handleDownloads, map[string]any{"command": "pause", "id": 7}, `"ok": true`},
		{"downloads resume", handleDownloads, map[string]any{"command": "resume", "id": 7}, `"ok": true`},
		{"downloads show", handleDownloads, map[string]any{"command": "show", "id": 7}, `"ok": true`},
		{"downloads show folder", handleDownloads, map[string]any{"command": "showFolder"}, `"ok": true`},
		{"windows list", handleWindows, map[string]any{"command": "list"}, `"ok": true`},
		{"windows new", handleWindows, map[string]any{"command": "new", "url": "https://new.test", "focused": true}, `"ok": true`},
		{"windows focus", handleWindows, map[string]any{"command": "focus", "id": 9}, `"ok": true`},
		{"windows close", handleWindows, map[string]any{"command": "close", "id": 9}, `"ok": true`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.fn(context.Background(), mkReq(tc.args))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if res.IsError {
				t.Fatalf("unexpected error result: %s", firstText(t, res))
			}
			if !strings.Contains(firstText(t, res), tc.want) {
				t.Fatalf("result text = %q, want %q", firstText(t, res), tc.want)
			}
		})
	}

	assertSeen := func(i int, method, path, query string) {
		t.Helper()
		if i >= len(seen) {
			t.Fatalf("missing request %d; seen=%+v", i, seen)
		}
		if seen[i].method != method || seen[i].path != path || seen[i].query != query {
			t.Fatalf("request %d = %+v, want method=%s path=%s query=%s", i, seen[i], method, path, query)
		}
	}
	assertSeen(0, http.MethodGet, "/v1/ext/capabilities", "")
	assertSeen(1, http.MethodPost, "/v1/ext/call", "")
	assertSeen(3, http.MethodGet, "/v1/bookmarks/search", "q=go")
	assertSeen(7, http.MethodGet, "/v1/browser-history/search", "maxResults=5&q=docs")
	assertSeen(9, http.MethodGet, "/v1/downloads/search", "limit=3&state=complete")
	assertSeen(10, http.MethodGet, "/v1/downloads/search", "q=zip")
	if seen[1].body["method"] != "bookmarks.search" {
		t.Fatalf("extension call body = %+v", seen[1].body)
	}
	if seen[4].body["parentId"] != "root" {
		t.Fatalf("bookmark create body = %+v", seen[4].body)
	}
	if seen[12].body["id"] != float64(7) || seen[12].body["q"] != "old" {
		t.Fatalf("downloads erase body = %+v", seen[12].body)
	}
	if seen[19].body["focused"] != true || seen[19].body["url"] != "https://new.test" {
		t.Fatalf("window new body = %+v", seen[19].body)
	}
}

func TestExtensionBridgeHandlers_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		fn   func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args map[string]any
		want string
	}{
		{"extension call missing method", handleExtensionCall, nil, "method is required"},
		{"extension call bad params", handleExtensionCall, map[string]any{"method": "x", "params": "bad"}, "params must be an object"},
		{"bookmarks create missing", handleBookmarks, map[string]any{"command": "create", "url": "https://x.test"}, "url and title are required"},
		{"bookmarks update missing id", handleBookmarks, map[string]any{"command": "update", "title": "x"}, "id is required"},
		{"bookmarks update no changes", handleBookmarks, map[string]any{"command": "update", "id": "b1"}, "title or url is required"},
		{"bookmarks remove missing id", handleBookmarks, map[string]any{"command": "remove"}, "id is required"},
		{"bookmarks unknown", handleBookmarks, map[string]any{"command": "bad"}, "unknown bookmarks command"},
		{"history delete missing url", handleBrowserHistory, map[string]any{"command": "deleteUrl"}, "url is required"},
		{"history unknown", handleBrowserHistory, map[string]any{"command": "bad"}, "unknown browser_history command"},
		{"downloads start missing url", handleDownloads, map[string]any{"command": "start"}, "url is required"},
		{"downloads action missing id", handleDownloads, map[string]any{"command": "cancel"}, "id is required"},
		{"downloads unknown", handleDownloads, map[string]any{"command": "bad"}, "unknown downloads command"},
		{"windows focus missing id", handleWindows, map[string]any{"command": "focus"}, "id is required"},
		{"windows close missing id", handleWindows, map[string]any{"command": "close"}, "id is required"},
		{"windows unknown", handleWindows, map[string]any{"command": "bad"}, "unknown windows command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tc.fn(context.Background(), mkReq(tc.args))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !res.IsError || !strings.Contains(firstText(t, res), tc.want) {
				t.Fatalf("result = %+v text=%q, want error containing %q", res, firstText(t, res), tc.want)
			}
		})
	}
}

func TestRawToolResult_ErrorAndRawFallback(t *testing.T) {
	res := rawToolResult(nil, errors.New("down"))
	if !res.IsError || !strings.Contains(firstText(t, res), "down") {
		t.Fatalf("error result = %+v text=%q", res, firstText(t, res))
	}
	res = rawToolResult(json.RawMessage(`not-json`), nil)
	if res.IsError || firstText(t, res) != "not-json" {
		t.Fatalf("raw fallback = %+v text=%q", res, firstText(t, res))
	}
}
