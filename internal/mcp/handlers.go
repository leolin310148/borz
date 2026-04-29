package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/diagnostics"
	"github.com/leolin310148/borz/internal/jseval"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/site"
	"github.com/mark3labs/mcp-go/mcp"
)

// siteLister / siteFinder / siteBuilder are variables so tests can stub the
// on-disk adapter resolution without creating real files.
var (
	siteLister  = site.AllSites
	siteFinder  = site.FindSite
	siteBuilder = site.BuildEvalRequestWithOptions
)

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// sendCommand is a variable so tests can stub out the daemon round-trip.
var sendCommand = client.SendCommand

func normalizeRef(ref string) string {
	return strings.TrimPrefix(ref, "@")
}

// setTab sets the TabID on a request if the tool call includes a "tab" param.
func setTab(req *protocol.Request, r mcp.CallToolRequest) {
	if tab := r.GetString("tab", ""); tab != "" {
		req.TabID = tab
	}
}

// applyWaitFor reads optional waitFor / timeout params off the tool call and
// attaches them to the request so the daemon polls document.querySelector
// after the action runs.
func applyWaitFor(req *protocol.Request, r mcp.CallToolRequest) {
	if sel := r.GetString("waitFor", ""); sel != "" {
		req.WaitFor = sel
	}
	if ms := r.GetInt("timeout", 0); ms > 0 {
		req.TimeoutMs = intPtr(ms)
	}
}

// intPtr returns a pointer to an int.
func intPtr(v int) *int { return &v }

// --- Navigation Handlers ---

func handleNavigate(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := r.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("url is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionOpen, URL: url}
	if r.GetBool("new", false) {
		req.New = true
	}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Navigated to %s", url)), nil
}

func handleBack(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionBack}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Navigated back"), nil
}

func handleForward(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionForward}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Navigated forward"), nil
}

func handleRefresh(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionRefresh}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Page refreshed"), nil
}

func handleClose(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionClose}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return mcp.NewToolResultText("Tab closed"), nil
}

// --- Interaction Handlers ---

func handleClick(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionClick, Ref: normalizeRef(ref)}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Clicked element @%s", normalizeRef(ref))), nil
}

func handleHover(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionHover, Ref: normalizeRef(ref)}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Hovered over element @%s", normalizeRef(ref))), nil
}

func handleFill(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("text is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionFill, Ref: normalizeRef(ref), Text: text}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Filled element @%s with %q", normalizeRef(ref), text)), nil
}

func handleType(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("text is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionType_, Ref: normalizeRef(ref), Text: text}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Typed %q into element @%s", text, normalizeRef(ref))), nil
}

func handleCheck(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionCheck, Ref: normalizeRef(ref)}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Checked element @%s", normalizeRef(ref))), nil
}

func handleUncheck(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionUncheck, Ref: normalizeRef(ref)}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Unchecked element @%s", normalizeRef(ref))), nil
}

func handleSelect(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	value, err := r.RequireString("value")
	if err != nil {
		return mcp.NewToolResultError("value is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionSelect, Ref: normalizeRef(ref), Value: value}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Selected %q on element @%s", value, normalizeRef(ref))), nil
}

func handlePress(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key, err := r.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError("key is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionPress, Key: key}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Pressed key %q", key)), nil
}

func handleScroll(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	direction := r.GetString("direction", "down")
	pixels := r.GetInt("pixels", 300)
	req := &protocol.Request{
		ID:        newID(),
		Action:    protocol.ActionScroll,
		Direction: direction,
		Pixels:    intPtr(pixels),
	}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Scrolled %s %d pixels", direction, pixels)), nil
}

// --- Observation Handlers ---

func handleSnapshot(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{
		ID:          newID(),
		Action:      protocol.ActionSnapshot,
		Interactive: r.GetBool("interactive", false),
		Compact:     r.GetBool("compact", false),
		Selector:    r.GetString("selector", ""),
		Role:        r.GetString("role", ""),
	}
	if depth := r.GetInt("maxDepth", 0); depth > 0 {
		req.MaxDepth = intPtr(depth)
	}
	if r.GetBool("textOnly", false) {
		req.Mode = "text"
	} else if mode := r.GetString("mode", ""); mode != "" {
		req.Mode = mode
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatSnapshot(resp), nil
}

func handleScreenshot(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionScreenshot}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatScreenshot(resp), nil
}

func handleGet(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	attr, err := r.RequireString("attribute")
	if err != nil {
		return mcp.NewToolResultError("attribute is required"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionGet, Attribute: attr}
	if ref := r.GetString("ref", ""); ref != "" {
		req.Ref = normalizeRef(ref)
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatGet(resp), nil
}

func handleEval(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	script, err := r.RequireString("script")
	if err != nil {
		return mcp.NewToolResultError("script is required"), nil
	}
	if !r.GetBool("noAutoAwait", false) {
		script = jseval.AutoWrapAwait(script)
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionEval, Script: script}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatEval(resp), nil
}

func handleWait(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ms := r.GetInt("ms", 1000)
	req := &protocol.Request{ID: newID(), Action: protocol.ActionWait, Ms: intPtr(ms)}
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Waited %d ms", ms)), nil
}

// --- Tab Management Handlers ---

func handleTabList(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabList}
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatTabList(resp), nil
}

func handleTabNew(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabNew}
	if url := r.GetString("url", ""); url != "" {
		req.URL = url
	}
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	msg := "Opened new tab"
	if req.URL != "" {
		msg += fmt.Sprintf(" at %s", req.URL)
	}
	return textResult(resp, msg), nil
}

func handleTabSelect(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabSelect}
	if tab := r.GetString("tab", ""); tab != "" {
		req.TabID = tab
	}
	if idx := r.GetInt("index", -1); idx >= 0 {
		req.Index = intPtr(idx)
	}
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Switched tab"), nil
}

func handleTabClose(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabClose}
	if tab := r.GetString("tab", ""); tab != "" {
		req.TabID = tab
	}
	if idx := r.GetInt("index", -1); idx >= 0 {
		req.Index = intPtr(idx)
	}
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return mcp.NewToolResultText("Tab closed"), nil
}

// --- Diagnostics Handlers ---

func handleNetwork(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := r.GetString("command", "requests")
	req := &protocol.Request{
		ID:             newID(),
		Action:         protocol.ActionNetwork,
		NetworkCommand: cmd,
		Filter:         r.GetString("filter", ""),
		WithBody:       r.GetBool("withBody", false),
		Method:         r.GetString("method", ""),
		Status:         r.GetString("status", ""),
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if cmd == "clear" {
		return mcp.NewToolResultText("Network requests cleared"), nil
	}
	return formatNetwork(resp), nil
}

func handleConsole(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := "get"
	if r.GetBool("clear", false) {
		cmd = "clear"
	}
	req := &protocol.Request{
		ID:             newID(),
		Action:         protocol.ActionConsole,
		ConsoleCommand: cmd,
		Filter:         r.GetString("filter", ""),
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if cmd == "clear" {
		return mcp.NewToolResultText("Console messages cleared"), nil
	}
	return formatConsole(resp), nil
}

// mcpVersion is set by Run() so handleDoctor can stamp the binary check.
var mcpVersion = "unknown"

func handleDoctor(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	checks, _ := diagnostics.Run(mcpVersion)
	if r.GetBool("json", false) {
		return mcp.NewToolResultText(diagnostics.RenderJSON(checks)), nil
	}
	return mcp.NewToolResultText(diagnostics.RenderText(checks)), nil
}

// --- Site Adapter Handlers ---

func handleSiteList(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sites := siteLister()
	if len(sites) == 0 {
		return mcp.NewToolResultText("No site adapters available. Run `borz site update` on the daemon host to pull community adapters."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Site adapters (%d):\n", len(sites))
	for _, s := range sites {
		src := ""
		if s.Source != "" {
			src = fmt.Sprintf(" [%s]", s.Source)
		}
		fmt.Fprintf(&sb, "  %s%s", s.Name, src)
		if s.Description != "" {
			fmt.Fprintf(&sb, " — %s", s.Description)
		}
		if s.Domain != "" {
			fmt.Fprintf(&sb, " (%s)", s.Domain)
		}
		if s.ReadOnly {
			sb.WriteString(" [read-only]")
		}
		sb.WriteByte('\n')
	}
	return mcp.NewToolResultText(sb.String()), nil
}

func handleSiteInfo(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := r.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	meta := siteFinder(name)
	if meta == nil {
		return mcp.NewToolResultError(fmt.Sprintf("adapter not found: %s", name)), nil
	}
	out, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode metadata: %v", err)), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}

func handleSiteRun(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := r.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	meta := siteFinder(name)
	if meta == nil {
		return mcp.NewToolResultError(fmt.Sprintf("adapter not found: %s", name)), nil
	}

	args := map[string]interface{}{}
	if raw, ok := r.GetArguments()["args"]; ok && raw != nil {
		if m, ok := raw.(map[string]interface{}); ok {
			args = m
		} else {
			return mcp.NewToolResultError("args must be an object"), nil
		}
	}

	tabID := r.GetString("tab", "")
	req, err := siteBuilder(meta, args, tabID, site.EvalOptions{
		Force:     r.GetBool("force", false),
		TimeoutMs: r.GetInt("timeout", 0),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to build adapter request: %v", err)), nil
	}
	req.ID = newID()

	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	site.RecordUsage(meta.Name)
	if r.GetBool("raw", false) {
		return formatEvalRaw(resp), nil
	}
	return formatEval(resp), nil
}

func handleErrors(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := "get"
	if r.GetBool("clear", false) {
		cmd = "clear"
	}
	req := &protocol.Request{
		ID:            newID(),
		Action:        protocol.ActionErrors,
		ErrorsCommand: cmd,
		Filter:        r.GetString("filter", ""),
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if cmd == "clear" {
		return mcp.NewToolResultText("JavaScript errors cleared"), nil
	}
	return formatErrors(resp), nil
}

func handleExtensionStatus(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := client.GetJSON("/v1/ext/capabilities", 10*time.Second)
	return rawToolResult(raw, err), nil
}

func handleExtensionCall(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	method, err := r.RequireString("method")
	if err != nil {
		return mcp.NewToolResultError("method is required"), nil
	}
	params := map[string]interface{}{}
	if raw, ok := r.GetArguments()["params"]; ok && raw != nil {
		if m, ok := raw.(map[string]interface{}); ok {
			params = m
		} else {
			return mcp.NewToolResultError("params must be an object"), nil
		}
	}
	raw, callErr := client.PostJSON("/v1/ext/call", map[string]interface{}{"method": method, "params": params}, 15*time.Second)
	return rawToolResult(raw, callErr), nil
}

func handleBookmarks(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := r.GetString("command", "tree")
	switch cmd {
	case "tree":
		raw, err := client.GetJSON("/v1/bookmarks/tree", 15*time.Second)
		return rawToolResult(raw, err), nil
	case "search":
		q := url.Values{}
		q.Set("q", r.GetString("query", ""))
		raw, err := client.GetJSON("/v1/bookmarks/search?"+q.Encode(), 15*time.Second)
		return rawToolResult(raw, err), nil
	case "create":
		body := map[string]interface{}{
			"url":   r.GetString("url", ""),
			"title": r.GetString("title", ""),
		}
		if body["url"] == "" || body["title"] == "" {
			return mcp.NewToolResultError("url and title are required"), nil
		}
		if parent := r.GetString("parentId", ""); parent != "" {
			body["parentId"] = parent
		}
		raw, err := client.PostJSON("/v1/bookmarks/create", body, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "update":
		id := r.GetString("id", "")
		if id == "" {
			return mcp.NewToolResultError("id is required"), nil
		}
		changes := map[string]interface{}{}
		if title := r.GetString("title", ""); title != "" {
			changes["title"] = title
		}
		if u := r.GetString("url", ""); u != "" {
			changes["url"] = u
		}
		if len(changes) == 0 {
			return mcp.NewToolResultError("title or url is required"), nil
		}
		raw, err := client.PostJSON("/v1/bookmarks/update", map[string]interface{}{"id": id, "changes": changes}, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "remove":
		id := r.GetString("id", "")
		if id == "" {
			return mcp.NewToolResultError("id is required"), nil
		}
		raw, err := client.PostJSON("/v1/bookmarks/remove", map[string]interface{}{"id": id, "recursive": r.GetBool("recursive", false)}, 15*time.Second)
		return rawToolResult(raw, err), nil
	default:
		return mcp.NewToolResultError("unknown bookmarks command"), nil
	}
}

func handleBrowserHistory(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := r.GetString("command", "search")
	switch cmd {
	case "search":
		q := url.Values{}
		q.Set("q", r.GetString("query", ""))
		if limit := r.GetInt("limit", 0); limit > 0 {
			q.Set("maxResults", fmt.Sprintf("%d", limit))
		}
		raw, err := client.GetJSON("/v1/browser-history/search?"+q.Encode(), 15*time.Second)
		return rawToolResult(raw, err), nil
	case "deleteUrl":
		u := r.GetString("url", "")
		if u == "" {
			return mcp.NewToolResultError("url is required"), nil
		}
		raw, err := client.PostJSON("/v1/browser-history/delete-url", map[string]interface{}{"url": u}, 15*time.Second)
		return rawToolResult(raw, err), nil
	default:
		return mcp.NewToolResultError("unknown browser_history command"), nil
	}
}

func handleDownloads(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := r.GetString("command", "list")
	switch cmd {
	case "list", "search":
		q := url.Values{}
		if cmd == "search" {
			q.Set("q", r.GetString("query", ""))
		}
		if state := r.GetString("state", ""); state != "" {
			q.Set("state", state)
		}
		if limit := r.GetInt("limit", 0); limit > 0 {
			q.Set("limit", fmt.Sprintf("%d", limit))
		}
		path := "/v1/downloads/search"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		raw, err := client.GetJSON(path, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "start":
		u := r.GetString("url", "")
		if u == "" {
			return mcp.NewToolResultError("url is required"), nil
		}
		body := map[string]interface{}{"url": u, "saveAs": r.GetBool("saveAs", false)}
		if filename := r.GetString("filename", ""); filename != "" {
			body["filename"] = filename
		}
		raw, err := client.PostJSON("/v1/downloads/download", body, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "erase":
		body := map[string]interface{}{}
		if id := r.GetInt("id", 0); id > 0 {
			body["id"] = id
		}
		if query := r.GetString("query", ""); query != "" {
			body["q"] = query
		}
		raw, err := client.PostJSON("/v1/downloads/erase", body, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "cancel", "pause", "resume", "show":
		id := r.GetInt("id", 0)
		if id <= 0 {
			return mcp.NewToolResultError("id is required"), nil
		}
		raw, err := client.PostJSON("/v1/downloads/"+cmd, map[string]interface{}{"id": id}, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "showFolder":
		raw, err := client.PostJSON("/v1/downloads/show-default-folder", map[string]interface{}{}, 15*time.Second)
		return rawToolResult(raw, err), nil
	default:
		return mcp.NewToolResultError("unknown downloads command"), nil
	}
}

func handleWindows(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cmd := r.GetString("command", "list")
	switch cmd {
	case "list":
		raw, err := client.GetJSON("/v1/windows", 15*time.Second)
		return rawToolResult(raw, err), nil
	case "new":
		body := map[string]interface{}{"focused": r.GetBool("focused", false)}
		if u := r.GetString("url", ""); u != "" {
			body["url"] = u
		}
		raw, err := client.PostJSON("/v1/windows/create", body, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "focus":
		id := r.GetInt("id", 0)
		if id <= 0 {
			return mcp.NewToolResultError("id is required"), nil
		}
		raw, err := client.PostJSON("/v1/windows/update", map[string]interface{}{"id": id, "updateInfo": map[string]interface{}{"focused": true}}, 15*time.Second)
		return rawToolResult(raw, err), nil
	case "close":
		id := r.GetInt("id", 0)
		if id <= 0 {
			return mcp.NewToolResultError("id is required"), nil
		}
		raw, err := client.PostJSON("/v1/windows/close", map[string]interface{}{"id": id}, 15*time.Second)
		return rawToolResult(raw, err), nil
	default:
		return mcp.NewToolResultError("unknown windows command"), nil
	}
}

func rawToolResult(raw json.RawMessage, err error) *mcp.CallToolResult {
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	var pretty any
	if json.Unmarshal(raw, &pretty) == nil {
		if out, marshalErr := json.MarshalIndent(pretty, "", "  "); marshalErr == nil {
			return mcp.NewToolResultText(string(out))
		}
	}
	return mcp.NewToolResultText(string(raw))
}
