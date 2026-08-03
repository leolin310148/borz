package mcp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
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
	siteLister   = site.AllSites
	siteFinder   = site.FindSite
	siteBuilder  = site.BuildEvalRequestWithOptions
	siteGetJSON  = client.GetJSON
	sitePostJSON = client.PostJSON
)

var (
	randomReader      io.Reader = rand.Reader
	fallbackIDCounter atomic.Uint64
)

func newID() string {
	b := make([]byte, 8)
	if _, err := io.ReadFull(randomReader, b); err != nil {
		fallback := uint64(os.Getpid())<<32 ^ fallbackIDCounter.Add(1)
		binary.BigEndian.PutUint64(b, fallback)
	}
	return hex.EncodeToString(b)
}

// sendCommand is a variable so tests can stub out the daemon round-trip.
var sendCommand = client.SendCommand

func normalizeRef(ref string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "@"))
}

// setTab sets the TabID on a request if the tool call includes a "tab" param.
func setTab(req *protocol.Request, r mcp.CallToolRequest) {
	if tab := tabIDArg(r); tab != "" {
		req.TabID = tab
	}
}

func tabIDArg(r mcp.CallToolRequest) string {
	return strings.TrimSpace(r.GetString("tab", ""))
}

// applyWaitFor reads optional waitFor / timeout params off the tool call and
// attaches them to the request so the daemon polls document.querySelector
// after the action runs.
func applyWaitFor(req *protocol.Request, r mcp.CallToolRequest) {
	if sel := strings.TrimSpace(r.GetString("waitFor", "")); sel != "" {
		req.WaitFor = sel
	}
	if _, ok := r.GetArguments()["timeout"]; ok {
		if ms := r.GetInt("timeout", 0); ms >= 0 {
			req.TimeoutMs = intPtr(ms)
		}
	}
	applyDelays(req, r)
}

// applyDelays reads optional preDelay / postDelay (ms) off the tool call
// and attaches them so the daemon sleeps before / after the action.
func applyDelays(req *protocol.Request, r mcp.CallToolRequest) {
	args := r.GetArguments()
	if _, ok := args["preDelay"]; ok {
		if ms := r.GetInt("preDelay", 0); ms >= 0 {
			req.PreDelayMs = intPtr(ms)
		}
	}
	if _, ok := args["postDelay"]; ok {
		if ms := r.GetInt("postDelay", 0); ms >= 0 {
			req.PostDelayMs = intPtr(ms)
		}
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
	opts, validationMsg := viewportOptionsFromMCP(r)
	if validationMsg != "" {
		return mcp.NewToolResultError(validationMsg), nil
	}
	req.Viewport = opts
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

func handleUpload(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ref, err := r.RequireString("ref")
	if err != nil {
		return mcp.NewToolResultError("ref is required"), nil
	}
	raw, ok := r.GetArguments()["files"]
	if !ok || raw == nil {
		return mcp.NewToolResultError("files is required"), nil
	}
	rawList, ok := raw.([]any)
	if !ok {
		return mcp.NewToolResultError("files must be an array of strings"), nil
	}
	files := make([]string, 0, len(rawList))
	for i, v := range rawList {
		s, ok := v.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("files[%d] must be a string", i)), nil
		}
		if s = strings.TrimSpace(s); s != "" {
			files = append(files, s)
		}
	}
	if len(files) == 0 {
		return mcp.NewToolResultError("files must contain at least one path"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionUpload, Ref: normalizeRef(ref), Files: files}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Uploaded %d file(s) to @%s", len(files), normalizeRef(ref))), nil
}

func handlePress(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key, err := r.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError("key is required"), nil
	}
	modifiers, errMsg := stringListArg(r, "modifiers")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	commands, errMsg := stringListArg(r, "commands")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionPress, Key: key, Modifiers: modifiers, Commands: commands}
	setTab(req, r)
	applyWaitFor(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, fmt.Sprintf("Pressed key %q", key)), nil
}

// stringListArg reads an optional array-of-strings tool argument. Returns a
// non-empty error message when the argument is present but malformed.
func stringListArg(r mcp.CallToolRequest, name string) ([]string, string) {
	raw, ok := r.GetArguments()[name]
	if !ok || raw == nil {
		return nil, ""
	}
	rawList, ok := raw.([]any)
	if !ok {
		return nil, fmt.Sprintf("%s must be an array of strings", name)
	}
	out := make([]string, 0, len(rawList))
	for i, v := range rawList {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Sprintf("%s[%d] must be a string", name, i)
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, ""
}

func handleDialog(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command := strings.TrimSpace(r.GetString("command", "accept"))
	if command == "" {
		command = "accept"
	}
	switch command {
	case "accept", "dismiss", "disarm", "status":
	default:
		return mcp.NewToolResultError(fmt.Sprintf(
			"unknown dialog command: %s (want accept, dismiss, disarm, or status)", command)), nil
	}
	req := &protocol.Request{
		ID: newID(), Action: protocol.ActionDialog,
		DialogResponse: command, PromptText: r.GetString("promptText", ""),
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	info, _ := dialogInfoMap(resp)
	if command == "status" {
		return textResult(resp, formatDialogStatus(info)), nil
	}
	// The daemon already distinguishes "answered the open dialog" from
	// "armed the next one"; surface its message so the model isn't left
	// guessing which happened.
	if message, _ := info["message"].(string); message != "" {
		return textResult(resp, message), nil
	}
	if command == "disarm" {
		return textResult(resp, "Dialog handler disarmed"), nil
	}
	return textResult(resp, fmt.Sprintf("Dialog handler armed: %s", command)), nil
}

func dialogInfoMap(resp *protocol.Response) (map[string]interface{}, bool) {
	if resp == nil || resp.Data == nil {
		return nil, false
	}
	info, ok := resp.Data.DialogInfo.(map[string]interface{})
	return info, ok
}

// formatDialogStatus renders `dialog status` as text, since textResult only
// carries a message — the model needs the open dialog's text to decide
// whether to accept or dismiss it.
func formatDialogStatus(info map[string]interface{}) string {
	var b strings.Builder
	if pending, ok := info["pending"].(map[string]interface{}); ok {
		kind, _ := pending["type"].(string)
		message, _ := pending["message"].(string)
		fmt.Fprintf(&b, "Open dialog: %s — %q", kind, message)
		if blocked, _ := info["blocked"].(bool); blocked {
			b.WriteString("\nBLOCKING the page: call browser_dialog with command=accept or dismiss to release it.")
		}
		if def, _ := pending["defaultPrompt"].(string); def != "" {
			fmt.Fprintf(&b, "\nDefault prompt text: %q", def)
		}
	} else {
		b.WriteString("Open dialog: none")
	}
	if armed, _ := info["armed"].(bool); armed {
		action, _ := info["action"].(string)
		fmt.Fprintf(&b, "\nArmed handler: %s", action)
	} else {
		b.WriteString("\nArmed handler: none")
	}
	if history, _ := info["history"].([]interface{}); len(history) > 0 {
		fmt.Fprintf(&b, "\nRecent dialogs (%d):", len(history))
		for _, item := range history {
			ev, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			kind, _ := ev["type"].(string)
			message, _ := ev["message"].(string)
			outcome := "resolved outside borz"
			if auto, _ := ev["autoHandled"].(bool); auto {
				handledAs, _ := ev["handledAs"].(string)
				outcome = "borz " + handledAs
			}
			fmt.Fprintf(&b, "\n  %s %q — %s", kind, message, outcome)
		}
	}
	return b.String()
}

func handleFileChooser(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command := strings.TrimSpace(r.GetString("command", "status"))
	if command == "" {
		command = "status"
	}
	files, errMsg := stringListArg(r, "files")
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if command == "accept" && len(files) == 0 {
		return mcp.NewToolResultError("files is required for command=accept"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionFileChooser, FileChooserCommand: command, Files: files}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	switch command {
	case "accept":
		return textResult(resp, fmt.Sprintf("File chooser armed: next dialog receives %d file(s)", len(files))), nil
	case "cancel":
		return textResult(resp, "File chooser armed: next dialog will be cancelled"), nil
	case "disarm":
		return textResult(resp, "File chooser disarmed"), nil
	default:
		return textResult(resp, "File chooser status"), nil
	}
}

func handleTabFront(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabFront}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "Brought tab to front"), nil
}

func handlePageVisibility(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	state := strings.ToLower(strings.TrimSpace(r.GetString("state", "")))
	switch state {
	case "", "visible", "hidden", "reset":
	default:
		return mcp.NewToolResultError("state must be visible, hidden, or reset"), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionPageVisibility, Visibility: state}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	switch state {
	case "":
		return textResult(resp, "Page visibility status"), nil
	case "reset":
		return textResult(resp, "Page visibility override removed"), nil
	default:
		return textResult(resp, fmt.Sprintf("Page visibility override set: %s", state)), nil
	}
}

func handleWebAuthn(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command := strings.ToLower(strings.TrimSpace(r.GetString("command", "")))
	req := &protocol.Request{
		ID:              newID(),
		Action:          protocol.ActionWebAuthn,
		WebAuthnCommand: command,
	}
	setTab(req, r)
	args := r.GetArguments()

	switch command {
	case "enable", "disable":
	case "add":
		opts := &protocol.VirtualAuthenticatorOptions{
			Protocol:                    strings.ToLower(strings.TrimSpace(r.GetString("protocol", "ctap2"))),
			Transport:                   strings.ToLower(strings.TrimSpace(r.GetString("transport", "internal"))),
			HasResidentKey:              mcpBoolDefault(r, "hasResidentKey", true),
			HasUserVerification:         mcpBoolDefault(r, "hasUserVerification", true),
			IsUserVerified:              mcpBoolDefault(r, "isUserVerified", true),
			AutomaticPresenceSimulation: mcpBoolDefault(r, "automaticPresence", true),
		}
		if opts.Protocol != "ctap2" && opts.Protocol != "u2f" {
			return mcp.NewToolResultError("protocol must be ctap2 or u2f"), nil
		}
		switch opts.Transport {
		case "internal", "usb", "nfc", "ble":
		default:
			return mcp.NewToolResultError("transport must be one of: internal, usb, nfc, ble"), nil
		}
		if opts.IsUserVerified && !opts.HasUserVerification {
			return mcp.NewToolResultError("isUserVerified=true requires hasUserVerification=true"), nil
		}
		if opts.Protocol == "u2f" && (opts.HasResidentKey || opts.HasUserVerification || opts.IsUserVerified) {
			return mcp.NewToolResultError("u2f requires hasResidentKey=false, hasUserVerification=false, and isUserVerified=false"), nil
		}
		req.VirtualAuthenticator = opts
	case "credentials", "remove", "set-user-verified", "set-automatic-presence":
		req.AuthenticatorID = strings.TrimSpace(r.GetString("authenticatorId", ""))
		if req.AuthenticatorID == "" {
			return mcp.NewToolResultError("authenticatorId is required for command=" + command), nil
		}
		if command == "set-user-verified" {
			if _, ok := args["isUserVerified"]; !ok {
				return mcp.NewToolResultError("isUserVerified is required for command=set-user-verified"), nil
			}
			value := r.GetBool("isUserVerified", false)
			req.UserVerified = &value
		}
		if command == "set-automatic-presence" {
			if _, ok := args["automaticPresence"]; !ok {
				return mcp.NewToolResultError("automaticPresence is required for command=set-automatic-presence"), nil
			}
			value := r.GetBool("automaticPresence", false)
			req.AutomaticPresence = &value
		}
	default:
		return mcp.NewToolResultError("command must be one of: enable, disable, add, credentials, remove, set-user-verified, set-automatic-presence"), nil
	}

	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return textResult(resp, "WebAuthn "+command+" completed"), nil
}

func mcpBoolDefault(r mcp.CallToolRequest, name string, defaultValue bool) bool {
	if _, ok := r.GetArguments()[name]; !ok {
		return defaultValue
	}
	return r.GetBool(name, defaultValue)
}

func handleClipboardWrite(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError("text is required"), nil
	}
	paste := r.GetBool("paste", false)
	req := &protocol.Request{ID: newID(), Action: protocol.ActionClipboardWrite, Text: text, Paste: paste}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	if paste {
		return textResult(resp, "Clipboard written and pasted (Ctrl+Shift+V)"), nil
	}
	return textResult(resp, "Clipboard written"), nil
}

func handleTermText(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTermText}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatGet(resp), nil
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
		Diff:        r.GetBool("diff", false),
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
	if raw, ok := r.GetArguments()["annotations"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return mcp.NewToolResultError("annotations must be an array of {ref,text} objects"), nil
		}
		if err := json.Unmarshal(encoded, &req.Annotations); err != nil {
			return mcp.NewToolResultError("annotations must be an array of {ref,text} objects"), nil
		}
		for i := range req.Annotations {
			req.Annotations[i].Ref = normalizeRef(req.Annotations[i].Ref)
		}
	}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatScreenshot(resp), nil
}

func handleViewport(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, validationMsg := viewportOptionsFromMCP(r)
	if validationMsg != "" {
		return mcp.NewToolResultError(validationMsg), nil
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionViewport, Viewport: opts}
	setTab(req, r)
	resp, err := sendCommand(req)
	if e := checkError(resp, err); e != nil {
		return e, nil
	}
	return formatViewport(resp), nil
}

func viewportOptionsFromMCP(r mcp.CallToolRequest) (*protocol.ViewportOptions, string) {
	args := r.GetArguments()
	if r.GetBool("reset", false) {
		return &protocol.ViewportOptions{Reset: true}, ""
	}
	var opts protocol.ViewportOptions
	has := false
	presetName := r.GetString("preset", "")
	if presetName == "" && r.GetBool("mobile", false) {
		if _, widthSet := args["width"]; !widthSet {
			if _, heightSet := args["height"]; !heightSet {
				presetName = "mobile"
			}
		}
	}
	if presetName != "" {
		preset, ok := protocol.ViewportPreset(presetName)
		if !ok {
			return nil, "preset must be one of: " + strings.Join(protocol.ViewportPresetNames(), ", ")
		}
		opts = preset
		has = true
	}
	if _, ok := args["width"]; ok {
		opts.Width = r.GetInt("width", 0)
		has = true
	}
	if _, ok := args["height"]; ok {
		opts.Height = r.GetInt("height", 0)
		has = true
	}
	if _, ok := args["dpr"]; ok {
		opts.DPR = r.GetFloat("dpr", 0)
		has = true
	}
	if _, ok := args["mobile"]; ok {
		opts.Mobile = r.GetBool("mobile", false)
		has = true
	}
	if _, ok := args["touch"]; ok {
		touch := r.GetBool("touch", false)
		opts.Touch = &touch
		has = true
	}
	if !has {
		return nil, ""
	}
	if opts.DPR <= 0 {
		opts.DPR = 1
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil, "width and height must be positive; use preset=mobile/tablet/desktop or pass both width and height"
	}
	return &opts, ""
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
	opts, validationMsg := viewportOptionsFromMCP(r)
	if validationMsg != "" {
		return mcp.NewToolResultError(validationMsg), nil
	}
	req.Viewport = opts
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
	if tab := tabIDArg(r); tab != "" {
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
	if tab := tabIDArg(r); tab != "" {
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

func siteScopeArg(r mcp.CallToolRequest) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(r.GetString("scope", "client")))
	switch scope {
	case "client", "server":
		return scope, nil
	default:
		return "", fmt.Errorf("scope must be client or server")
	}
}

type mcpServerSiteListResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    struct {
		Sites []*site.SiteMeta `json:"sites"`
	} `json:"data"`
}

type mcpServerSiteInfoResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    struct {
		Site *site.SiteMeta `json:"site"`
	} `json:"data"`
}

func mcpServerSites() ([]*site.SiteMeta, error) {
	raw, err := siteGetJSON("/v1/sites", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var payload mcpServerSiteListResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode server site list: %w", err)
	}
	if !payload.Success {
		if payload.Error == "" {
			payload.Error = "unknown error"
		}
		return nil, fmt.Errorf("server site list failed: %s", payload.Error)
	}
	if payload.Data.Sites == nil {
		return []*site.SiteMeta{}, nil
	}
	return payload.Data.Sites, nil
}

func mcpServerSiteInfo(name string) (*site.SiteMeta, error) {
	raw, err := sitePostJSON("/v1/sites/info", map[string]interface{}{"name": name}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var payload mcpServerSiteInfoResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode server site info: %w", err)
	}
	if !payload.Success || payload.Data.Site == nil {
		if payload.Error == "" {
			payload.Error = "adapter not found: " + name
		}
		return nil, fmt.Errorf("server site info failed: %s", payload.Error)
	}
	return payload.Data.Site, nil
}

func formatSiteList(sites []*site.SiteMeta) *mcp.CallToolResult {
	if len(sites) == 0 {
		return mcp.NewToolResultText("No site adapters available in the selected scope.")
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
	return mcp.NewToolResultText(sb.String())
}

func handleSiteList(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	scope, err := siteScopeArg(r)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var sites []*site.SiteMeta
	if scope == "server" {
		sites, err = mcpServerSites()
	} else {
		sites = siteLister()
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list %s site adapters: %v", scope, err)), nil
	}
	return formatSiteList(sites), nil
}

func handleSiteInfo(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := r.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("name is required"), nil
	}
	scope, err := siteScopeArg(r)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var meta *site.SiteMeta
	if scope == "server" {
		meta, err = mcpServerSiteInfo(name)
	} else {
		meta = siteFinder(name)
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
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
	scope, err := siteScopeArg(r)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := map[string]interface{}{}
	if raw, ok := r.GetArguments()["args"]; ok && raw != nil {
		if m, ok := raw.(map[string]interface{}); ok {
			args = m
		} else {
			return mcp.NewToolResultError("args must be an object"), nil
		}
	}

	tabID := tabIDArg(r)
	if scope == "server" {
		body := map[string]interface{}{
			"name":  name,
			"args":  args,
			"force": r.GetBool("force", false),
		}
		if tabID != "" {
			body["tab"] = tabID
		}
		if timeout := r.GetInt("timeout", 0); timeout > 0 {
			body["timeoutMs"] = timeout
		}
		raw, postErr := sitePostJSON("/v1/sites/run", body, 30*time.Second)
		if postErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("server site run failed: %v", postErr)), nil
		}
		var resp protocol.Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("decode server site result: %v", err)), nil
		}
		if e := checkError(&resp, nil); e != nil {
			return e, nil
		}
		if r.GetBool("raw", false) {
			return formatEvalRaw(&resp), nil
		}
		return formatEval(&resp), nil
	}

	meta := siteFinder(name)
	if meta == nil {
		return mcp.NewToolResultError(fmt.Sprintf("adapter not found: %s", name)), nil
	}
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
		return mcp.NewToolResultError(unknownMCPCommand("bookmarks", cmd, "tree", "search", "create", "update", "remove")), nil
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
		return mcp.NewToolResultError(unknownMCPCommand("browser_history", cmd, "search", "deleteUrl")), nil
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
		return mcp.NewToolResultError(unknownMCPCommand("downloads", cmd, "list", "search", "start", "erase", "cancel", "pause", "resume", "show", "showFolder")), nil
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
		return mcp.NewToolResultError(unknownMCPCommand("windows", cmd, "list", "new", "focus", "close")), nil
	}
}

func unknownMCPCommand(tool, got string, valid ...string) string {
	msg := fmt.Sprintf("unknown %s command: %s", tool, got)
	if len(valid) > 0 {
		msg += ". Valid commands: " + strings.Join(valid, ", ")
	}
	return msg
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
