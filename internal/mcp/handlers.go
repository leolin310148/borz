package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/leolin310148/bb-browser-go/internal/client"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func normalizeRef(ref string) string {
	return strings.TrimPrefix(ref, "@")
}

// setTab sets the TabID on a request if the tool call includes a "tab" param.
func setTab(req *protocol.Request, r mcp.CallToolRequest) {
	if tab := r.GetString("tab", ""); tab != "" {
		req.TabID = tab
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
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Navigated to %s", url)), nil
}

func handleBack(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionBack}
	setTab(req, r)
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Navigated back"), nil
}

func handleForward(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionForward}
	setTab(req, r)
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Navigated forward"), nil
}

func handleRefresh(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionRefresh}
	setTab(req, r)
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Page refreshed"), nil
}

func handleClose(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionClose}
	setTab(req, r)
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	}
	if depth := r.GetInt("maxDepth", 0); depth > 0 {
		req.MaxDepth = intPtr(depth)
	}
	setTab(req, r)
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatSnapshot(resp), nil
}

func handleScreenshot(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionScreenshot}
	setTab(req, r)
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	req := &protocol.Request{ID: newID(), Action: protocol.ActionEval, Script: script}
	setTab(req, r)
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatEval(resp), nil
}

func handleWait(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ms := r.GetInt("ms", 1000)
	req := &protocol.Request{ID: newID(), Action: protocol.ActionWait, Ms: intPtr(ms)}
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Waited %d ms", ms)), nil
}

// --- Tab Management Handlers ---

func handleTabList(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabList}
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
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
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if cmd == "clear" {
		return mcp.NewToolResultText("Console messages cleared"), nil
	}
	return formatConsole(resp), nil
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
	resp, err := client.SendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if cmd == "clear" {
		return mcp.NewToolResultText("JavaScript errors cleared"), nil
	}
	return formatErrors(resp), nil
}
