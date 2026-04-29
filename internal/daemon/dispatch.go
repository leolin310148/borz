package daemon

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

//go:embed embed/buildDomTree.js
var embeddedFS embed.FS

var (
	buildDomTreeScript     string
	buildDomTreeScriptOnce sync.Once
)

func loadBuildDomTreeScript() string {
	buildDomTreeScriptOnce.Do(func() {
		data, err := embeddedFS.ReadFile("embed/buildDomTree.js")
		if err != nil {
			panic(fmt.Sprintf("Cannot find embedded buildDomTree.js: %v", err))
		}
		buildDomTreeScript = string(data)
	})
	return buildDomTreeScript
}

func okResp(id string, data *protocol.ResponseData) *protocol.Response {
	return &protocol.Response{ID: id, Success: true, Data: data}
}

func failResp(id string, err interface{}) *protocol.Response {
	msg := fmt.Sprintf("%v", err)
	return &protocol.Response{ID: id, Success: false, Error: msg}
}

func intPtr(v int) *int { return &v }

// applyWaitFor honors req.WaitFor / req.TimeoutMs by polling for the selector.
// Returns nil immediately if WaitFor is empty.
func applyWaitFor(cdp *CdpConnection, targetID string, req *protocol.Request) error {
	if req.WaitFor == "" {
		return nil
	}
	timeout := 10 * time.Second
	if req.TimeoutMs != nil && *req.TimeoutMs > 0 {
		timeout = time.Duration(*req.TimeoutMs) * time.Millisecond
	}
	return waitForSelector(cdp, targetID, req.WaitFor, timeout)
}

// withWaitFor runs applyWaitFor on success and converts a wait-for timeout
// into a failResp. Use as: `return withWaitFor(req, cdp, target.ID, okResp(...))`.
// On a non-success input it is a passthrough.
func withWaitFor(req *protocol.Request, cdp *CdpConnection, targetID string, resp *protocol.Response) *protocol.Response {
	if !resp.Success || req.WaitFor == "" {
		return resp
	}
	if err := applyWaitFor(cdp, targetID, req); err != nil {
		return failResp(req.ID, err)
	}
	return resp
}

// waitForTabNavigated polls a freshly-created tab until its document has left
// the initial about:blank context and document.readyState is at least
// 'interactive'. Used right after Target.createTarget so callers don't get a
// tabId that points at a still-blank page — a fetch evaluated against
// about:blank fails CORS as a generic "TypeError: Failed to fetch".
//
// Best-effort: never returns an error to the caller. If the timeout elapses
// (slow network, slow cross-origin nav) the function just returns and the
// dispatch path continues; the caller may see the same race they would have
// without this helper, but at least the common case is fixed.
//
// requestedURL is the URL passed to Target.createTarget. If it's empty or
// "about:blank" we only wait on readyState (there is no navigation to wait
// for). Otherwise we wait for location.href to match the requested origin
// (path/query may legitimately differ after redirects).
func waitForTabNavigated(cdp *CdpConnection, targetID, requestedURL string, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	wantNav := requestedURL != "" && requestedURL != "about:blank"
	deadline := time.Now().Add(timeout)
	expr := `JSON.stringify({readyState: document.readyState, href: location.href})`
	for {
		raw, err := cdp.Evaluate(targetID, expr, true)
		if err == nil {
			var encoded string
			if json.Unmarshal(raw, &encoded) == nil {
				var state struct {
					ReadyState string `json:"readyState"`
					Href       string `json:"href"`
				}
				if json.Unmarshal([]byte(encoded), &state) == nil {
					ready := state.ReadyState == "interactive" || state.ReadyState == "complete"
					navigated := !wantNav || (state.Href != "" && state.Href != "about:blank")
					if ready && navigated {
						return
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newTabReadyTimeout caps how long ActionTabNew / ActionOpen wait for the
// just-created tab's page context to leave about:blank. Kept short so a slow
// cross-origin load doesn't stall the daemon.
const newTabReadyTimeout = 5 * time.Second

// waitForSelector polls Runtime.evaluate(document.querySelector(sel)!=null) on
// 100ms ticks until truthy or timeout. cdp.Evaluate may transiently fail while
// the navigation tears down the old execution context — those errors are
// retried, only the timeout is reported.
func waitForSelector(cdp *CdpConnection, targetID, selector string, timeout time.Duration) error {
	selJSON, _ := json.Marshal(selector)
	expr := fmt.Sprintf("!!document.querySelector(%s)", string(selJSON))
	deadline := time.Now().Add(timeout)
	for {
		raw, err := cdp.Evaluate(targetID, expr, true)
		if err == nil {
			var found bool
			if json.Unmarshal(raw, &found) == nil && found {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait-for selector %q: timeout after %s", selector, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// --- Snapshot ---

func buildSnapshot(cdp *CdpConnection, targetID string, tab *TabState, req *protocol.Request) (*protocol.SnapshotData, error) {
	if req.Mode == "text" {
		snap, err := buildTextSnapshot(cdp, targetID)
		if err != nil {
			return nil, err
		}
		// Text mode does not establish refs; clear any stale ones from a
		// previous tree-mode snapshot so '<text-only>' followed by 'click ref'
		// fails fast with a clear error instead of acting on a stale handle.
		tab.Refs = map[string]*protocol.RefInfo{}
		return snap, nil
	}
	script := loadBuildDomTreeScript()
	buildArgs := `{"showHighlightElements":true,"focusHighlightIndex":-1,"viewportExpansion":-1,"debugMode":false,"startId":0,"startHighlightIndex":0}`
	expression := fmt.Sprintf(`(() => { %s; const fn = globalThis.buildDomTree ?? (typeof window !== 'undefined' ? window.buildDomTree : undefined); if (typeof fn !== 'function') { throw new Error('buildDomTree is not available after script injection'); } return fn(%s); })()`, script, buildArgs)

	raw, err := cdp.Evaluate(targetID, expression, true)
	if err != nil || raw == nil || string(raw) == "null" {
		// Fallback: return page title
		titleRaw, _ := cdp.Evaluate(targetID, "document.title", true)
		title := ""
		json.Unmarshal(titleRaw, &title)
		tab.Refs = map[string]*protocol.RefInfo{}
		return &protocol.SnapshotData{Snapshot: title, Refs: map[string]*protocol.RefInfo{}}, nil
	}

	var result buildDomTreeResult
	if err := json.Unmarshal(raw, &result); err != nil || result.RootID == "" {
		tab.Refs = map[string]*protocol.RefInfo{}
		return &protocol.SnapshotData{Snapshot: "", Refs: map[string]*protocol.RefInfo{}}, nil
	}

	snapshot := ConvertBuildDomTreeResult(&result, req.Interactive, req.Compact, req.MaxDepth, req.Selector, req.Role)
	tab.Refs = snapshot.Refs
	return snapshot, nil
}

// --- Ref resolution ---

func resolveBackendNodeIDByXPath(cdp *CdpConnection, targetID, xpath string) (int, error) {
	cdp.SessionCommand(targetID, "DOM.getDocument", map[string]interface{}{"depth": 0})

	searchRaw, err := cdp.SessionCommand(targetID, "DOM.performSearch", map[string]interface{}{
		"query":                     xpath,
		"includeUserAgentShadowDOM": true,
	})
	if err != nil {
		return 0, err
	}

	var search struct {
		SearchID    string `json:"searchId"`
		ResultCount int    `json:"resultCount"`
	}
	json.Unmarshal(searchRaw, &search)

	defer func() {
		cdp.SessionCommand(targetID, "DOM.discardSearchResults", map[string]interface{}{"searchId": search.SearchID})
	}()

	if search.ResultCount == 0 {
		return 0, fmt.Errorf("unknown ref xpath: %s", xpath)
	}

	resultsRaw, err := cdp.SessionCommand(targetID, "DOM.getSearchResults", map[string]interface{}{
		"searchId":  search.SearchID,
		"fromIndex": 0,
		"toIndex":   search.ResultCount,
	})
	if err != nil {
		return 0, err
	}

	var results struct {
		NodeIDs []int `json:"nodeIds"`
	}
	json.Unmarshal(resultsRaw, &results)

	for _, nodeID := range results.NodeIDs {
		descRaw, err := cdp.SessionCommand(targetID, "DOM.describeNode", map[string]interface{}{"nodeId": nodeID})
		if err != nil {
			continue
		}
		var desc struct {
			Node struct {
				BackendNodeID int `json:"backendNodeId"`
			} `json:"node"`
		}
		json.Unmarshal(descRaw, &desc)
		if desc.Node.BackendNodeID > 0 {
			return desc.Node.BackendNodeID, nil
		}
	}
	return 0, fmt.Errorf("XPath resolved but no backend node id found: %s", xpath)
}

func parseRef(cdp *CdpConnection, targetID string, tab *TabState, ref string) (int, error) {
	found, ok := tab.Refs[ref]
	if !ok {
		return 0, fmt.Errorf("unknown ref: %s. Run snapshot first", ref)
	}
	if found.BackendDOMNodeID > 0 {
		return found.BackendDOMNodeID, nil
	}
	if found.XPath != "" {
		backendID, err := resolveBackendNodeIDByXPath(cdp, targetID, found.XPath)
		if err != nil {
			return 0, err
		}
		found.BackendDOMNodeID = backendID
		return backendID, nil
	}
	return 0, fmt.Errorf("unknown ref: %s. Run snapshot first", ref)
}

// --- Input helpers ---

func getInteractablePoint(cdp *CdpConnection, targetID string, backendNodeID int) (x, y float64, err error) {
	resolvedRaw, err := cdp.SessionCommand(targetID, "DOM.resolveNode", map[string]interface{}{
		"backendNodeId": backendNodeID,
	})
	if err != nil {
		return 0, 0, err
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	json.Unmarshal(resolvedRaw, &resolved)

	callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": `function() {
			if (!(this instanceof Element)) throw new Error('Ref does not resolve to an element');
			this.scrollIntoView({ behavior: 'auto', block: 'center', inline: 'center' });
			const rect = this.getBoundingClientRect();
			if (!rect || rect.width <= 0 || rect.height <= 0) throw new Error('Element is not visible');
			return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
		}`,
		"returnByValue": true,
	})
	if err != nil {
		return 0, 0, err
	}

	var call struct {
		Result struct {
			Value struct {
				X float64 `json:"x"`
				Y float64 `json:"y"`
			} `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	json.Unmarshal(callRaw, &call)

	if call.ExceptionDetails != nil {
		return 0, 0, fmt.Errorf("%s", call.ExceptionDetails.Text)
	}
	return call.Result.Value.X, call.Result.Value.Y, nil
}

func mouseClick(cdp *CdpConnection, targetID string, x, y float64) error {
	cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseMoved", "x": x, "y": y, "button": "none",
	})
	cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1,
	})
	_, err := cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1,
	})
	return err
}

func insertTextIntoNode(cdp *CdpConnection, targetID string, backendNodeID int, text string, clearFirst bool) error {
	resolvedRaw, err := cdp.SessionCommand(targetID, "DOM.resolveNode", map[string]interface{}{
		"backendNodeId": backendNodeID,
	})
	if err != nil {
		return err
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	json.Unmarshal(resolvedRaw, &resolved)

	cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": fmt.Sprintf(`function(clearFirst) {
			if (typeof this.scrollIntoView === 'function') this.scrollIntoView({ behavior: 'auto', block: 'center', inline: 'center' });
			if (typeof this.focus === 'function') this.focus();
			if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement) {
				if (clearFirst) { this.value = ''; this.dispatchEvent(new Event('input', { bubbles: true })); }
				if (typeof this.setSelectionRange === 'function') { const end = this.value.length; this.setSelectionRange(end, end); }
				return true;
			}
			if (this instanceof HTMLElement && this.isContentEditable) {
				if (clearFirst) { this.textContent = ''; this.dispatchEvent(new Event('input', { bubbles: true })); }
				const selection = window.getSelection();
				if (selection) { const range = document.createRange(); range.selectNodeContents(this); range.collapse(false); selection.removeAllRanges(); selection.addRange(range); }
				return true;
			}
			return false;
		}`),
		"arguments":     []map[string]interface{}{{"value": clearFirst}},
		"returnByValue": true,
	})

	if text != "" {
		cdp.SessionCommand(targetID, "DOM.focus", map[string]interface{}{"backendNodeId": backendNodeID})
		_, err = cdp.SessionCommand(targetID, "Input.insertText", map[string]interface{}{"text": text})
		return err
	}
	return nil
}

func getAttributeValue(cdp *CdpConnection, targetID string, backendNodeID int, attribute string) (string, error) {
	resolvedRaw, err := cdp.SessionCommand(targetID, "DOM.resolveNode", map[string]interface{}{
		"backendNodeId": backendNodeID,
	})
	if err != nil {
		return "", err
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	json.Unmarshal(resolvedRaw, &resolved)

	var fn string
	if attribute == "text" {
		fn = `function() { return (this instanceof HTMLElement ? this.innerText : this.textContent || '').trim(); }`
	} else {
		attrJSON, _ := json.Marshal(attribute)
		fn = fmt.Sprintf(`function() { if (%s === 'url') return this.href || this.src || location.href; if (%s === 'title') return document.title; return this.getAttribute(%s) || ''; }`, string(attrJSON), string(attrJSON), string(attrJSON))
	}

	callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId":            resolved.Object.ObjectID,
		"functionDeclaration": fn,
		"returnByValue":       true,
	})
	if err != nil {
		return "", err
	}

	var call struct {
		Result struct {
			Value interface{} `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(callRaw, &call)
	return fmt.Sprintf("%v", call.Result.Value), nil
}

// keyDef is the CDP keyboard event descriptor for a single key.
type keyDef struct {
	Key     string // KeyboardEvent.key
	Code    string // KeyboardEvent.code
	KeyCode int    // windowsVirtualKeyCode
	Text    string // character to emit (empty for non-printable keys)
}

// specialKeymap maps named non-printable keys to their CDP descriptors.
// Non-printable keys need windowsVirtualKeyCode set for Chrome's default
// handler to fire OS-level behavior (newline, cursor move, etc.).
var specialKeymap = map[string]keyDef{
	"Enter":      {"Enter", "Enter", 13, "\r"},
	"Return":     {"Enter", "Enter", 13, "\r"},
	"Tab":        {"Tab", "Tab", 9, "\t"},
	"Backspace":  {"Backspace", "Backspace", 8, ""},
	"Delete":     {"Delete", "Delete", 46, ""},
	"Escape":     {"Escape", "Escape", 27, ""},
	"Esc":        {"Escape", "Escape", 27, ""},
	"Space":      {" ", "Space", 32, " "},
	"ArrowUp":    {"ArrowUp", "ArrowUp", 38, ""},
	"ArrowDown":  {"ArrowDown", "ArrowDown", 40, ""},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37, ""},
	"ArrowRight": {"ArrowRight", "ArrowRight", 39, ""},
	"Up":         {"ArrowUp", "ArrowUp", 38, ""},
	"Down":       {"ArrowDown", "ArrowDown", 40, ""},
	"Left":       {"ArrowLeft", "ArrowLeft", 37, ""},
	"Right":      {"ArrowRight", "ArrowRight", 39, ""},
	"Home":       {"Home", "Home", 36, ""},
	"End":        {"End", "End", 35, ""},
	"PageUp":     {"PageUp", "PageUp", 33, ""},
	"PageDown":   {"PageDown", "PageDown", 34, ""},
	"Insert":     {"Insert", "Insert", 45, ""},
	"Shift":      {"Shift", "ShiftLeft", 16, ""},
	"Control":    {"Control", "ControlLeft", 17, ""},
	"Alt":        {"Alt", "AltLeft", 18, ""},
	"Meta":       {"Meta", "MetaLeft", 91, ""},
	"F1":         {"F1", "F1", 112, ""},
	"F2":         {"F2", "F2", 113, ""},
	"F3":         {"F3", "F3", 114, ""},
	"F4":         {"F4", "F4", 115, ""},
	"F5":         {"F5", "F5", 116, ""},
	"F6":         {"F6", "F6", 117, ""},
	"F7":         {"F7", "F7", 118, ""},
	"F8":         {"F8", "F8", 119, ""},
	"F9":         {"F9", "F9", 120, ""},
	"F10":        {"F10", "F10", 121, ""},
	"F11":        {"F11", "F11", 122, ""},
	"F12":        {"F12", "F12", 123, ""},
}

// resolveKey builds the CDP key descriptor for a key name or a single printable char.
// Named keys are looked up in specialKeymap; single printable runes get a synthetic
// keyCode (a-z → A-Z, 0-9 → 0-9) so typing works in canvas apps expecting real events.
func resolveKey(keyName string) keyDef {
	if def, ok := specialKeymap[keyName]; ok {
		return def
	}
	runes := []rune(keyName)
	if len(runes) == 1 {
		r := runes[0]
		def := keyDef{Key: keyName, Text: keyName}
		switch {
		case r >= 'a' && r <= 'z':
			def.KeyCode = int(r - 'a' + 'A')
			def.Code = "Key" + strings.ToUpper(keyName)
		case r >= 'A' && r <= 'Z':
			def.KeyCode = int(r)
			def.Code = "Key" + keyName
		case r >= '0' && r <= '9':
			def.KeyCode = int(r)
			def.Code = "Digit" + keyName
		case r == ' ':
			def.KeyCode = 32
			def.Code = "Space"
		}
		return def
	}
	return keyDef{Key: keyName}
}

// modifierMask converts a list of modifier names into the CDP bitmask used by
// Input.dispatchKeyEvent / Input.dispatchMouseEvent: alt=1, ctrl=2, meta=4, shift=8.
func modifierMask(mods []string) int {
	var m int
	for _, mod := range mods {
		switch strings.ToLower(mod) {
		case "alt":
			m |= 1
		case "ctrl", "control":
			m |= 2
		case "meta", "cmd", "command":
			m |= 4
		case "shift":
			m |= 8
		}
	}
	return m
}

// --- Trace state ---

var (
	traceRecording bool
	traceEvents    []protocol.TraceEvent
	traceMu        sync.Mutex
)

// --- Main dispatch ---

// DispatchRequest handles all browser commands via CDP.
func DispatchRequest(cdp *CdpConnection, req *protocol.Request) *protocol.Response {
	tabRef := ""
	if req.TabID != nil {
		tabRef = fmt.Sprintf("%v", req.TabID)
	}

	// tab_list and tab_new must work even when no existing tabs or unattachable targets
	if req.Action == protocol.ActionTabList {
		targets, _ := cdp.GetTargets()
		var tabs []protocol.TabInfo
		idx := 0
		for _, t := range targets {
			if t.Type != "page" {
				continue
			}
			tState := cdp.TabManager.GetTab(t.ID)
			tabShort := strings.ToLower(t.ID[max(0, len(t.ID)-4):])
			if tState != nil {
				tabShort = tState.ShortID
			}
			tabs = append(tabs, protocol.TabInfo{
				Index:  idx,
				URL:    t.URL,
				Title:  t.Title,
				Active: t.ID == cdp.CurrentTargetID || (cdp.CurrentTargetID == "" && idx == 0),
				TabID:  t.ID,
				Tab:    tabShort,
			})
			idx++
		}
		activeIdx := 0
		for i, t := range tabs {
			if t.Active {
				activeIdx = i
				break
			}
		}
		return okResp(req.ID, &protocol.ResponseData{Tabs: tabs, ActiveIndex: intPtr(activeIdx)})
	}

	if req.Action == protocol.ActionTabNew {
		url := req.URL
		if url == "" {
			url = "about:blank"
		}
		result, err := cdp.BrowserCommand("Target.createTarget", map[string]interface{}{
			"url": url, "background": true,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var created struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(result, &created)
		cdp.AttachAndEnable(created.TargetID)
		// Wait for the new tab's page context to leave about:blank so that
		// follow-up fetch/eval calls don't run in the initial blank context
		// and fail CORS with a generic "Failed to fetch".
		waitForTabNavigated(cdp, created.TargetID, url, newTabReadyTimeout)
		newTab := cdp.TabManager.GetTab(created.TargetID)
		shortID := ""
		var seq *int
		if newTab != nil {
			shortID = newTab.ShortID
			s := newTab.RecordAction()
			seq = &s
		}
		return okResp(req.ID, &protocol.ResponseData{
			TabID: created.TargetID, URL: url, Tab: shortID, Seq: seq,
		})
	}

	// `open` with no --tab opens a new tab, so it must work when no page
	// targets exist yet (e.g. fresh Chrome with only about:blank closed).
	if req.Action == protocol.ActionOpen && tabRef == "" {
		if req.URL == "" {
			return failResp(req.ID, "missing url parameter")
		}

		// Reuse-by-URL: if a page target already has this exact URL, focus it
		// instead of opening a fresh tab. Prevents tab-blowup in automated
		// workflows that call `open` repeatedly for the same URL. Opt out
		// with --new (or `new: true` on the wire).
		if !req.New {
			if existing := findTargetByExactURL(cdp, req.URL); existing != nil {
				cdp.CurrentTargetID = existing.ID
				cdp.AttachAndEnable(existing.ID)
				cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": existing.ID})
				reused := cdp.TabManager.GetTab(existing.ID)
				shortID := ""
				var seq *int
				if reused != nil {
					shortID = reused.ShortID
					s := reused.RecordAction()
					seq = &s
				}
				return withWaitFor(req, cdp, existing.ID, okResp(req.ID, &protocol.ResponseData{
					TabID: existing.ID, URL: existing.URL, Title: existing.Title,
					Tab: shortID, Seq: seq,
				}))
			}
		}

		result, err := cdp.BrowserCommand("Target.createTarget", map[string]interface{}{
			"url": req.URL,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var created struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(result, &created)
		cdp.AttachAndEnable(created.TargetID)
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": created.TargetID})
		// Same readiness wait as ActionTabNew — see waitForTabNavigated.
		waitForTabNavigated(cdp, created.TargetID, req.URL, newTabReadyTimeout)
		cdp.CurrentTargetID = created.TargetID
		newTab := cdp.TabManager.GetTab(created.TargetID)
		shortID := ""
		var seq *int
		if newTab != nil {
			shortID = newTab.ShortID
			s := newTab.RecordAction()
			seq = &s
		}
		return withWaitFor(req, cdp, created.TargetID, okResp(req.ID, &protocol.ResponseData{
			TabID: created.TargetID, URL: req.URL, Tab: shortID, Seq: seq,
		}))
	}

	target, err := cdp.EnsurePageTarget(tabRef)
	if err != nil {
		return failResp(req.ID, err)
	}
	tab := cdp.TabManager.GetTab(target.ID)
	if tab == nil {
		return failResp(req.ID, "internal error: tab state not found")
	}
	shortID := tab.ShortID

	// Per-request foreground request. Honored for any action that fell through
	// to EnsurePageTarget — handy for fetch/eval against pages that throttle
	// backgrounded tabs, or for clipboard/paste shortcuts that need real focus.
	// Explicit Activate also updates CurrentTargetID since the caller asked to
	// switch focus to this tab.
	if req.Activate {
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": target.ID})
		cdp.SessionCommand(target.ID, "Page.bringToFront", nil)
		cdp.CurrentTargetID = target.ID
	}

	switch req.Action {
	// --- Navigation ---
	case protocol.ActionOpen:
		// tabRef == "" is handled above (hoisted so it works with no existing pages).
		if req.URL == "" {
			return failResp(req.ID, "missing url parameter")
		}
		seq := tab.RecordAction()
		cdp.PageCommand(target.ID, "Page.navigate", map[string]interface{}{"url": req.URL})
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": target.ID})
		// open always activates — pin CurrentTargetID since EnsurePageTarget
		// no longer mutates it for explicit-tab requests.
		cdp.CurrentTargetID = target.ID
		tab.Refs = map[string]*protocol.RefInfo{}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{
			URL: req.URL, Title: target.Title, TabID: target.ID, Tab: shortID, Seq: intPtr(seq),
		}))

	case protocol.ActionBack:
		seq := tab.RecordAction()
		cdp.Evaluate(target.ID, "history.back(); undefined", false)
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionForward:
		seq := tab.RecordAction()
		cdp.Evaluate(target.ID, "history.forward(); undefined", false)
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionRefresh:
		seq := tab.RecordAction()
		cdp.SessionCommand(target.ID, "Page.reload", map[string]interface{}{"ignoreCache": false})
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionClose:
		seq := tab.RecordAction()
		cdp.BrowserCommand("Target.closeTarget", map[string]interface{}{"targetId": target.ID})
		tab.Refs = map[string]*protocol.RefInfo{}
		return okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)})

	// --- Snapshot / Observation ---
	case protocol.ActionSnapshot:
		snapshotData, err := buildSnapshot(cdp, target.ID, tab, req)
		if err != nil {
			return failResp(req.ID, err)
		}
		return okResp(req.ID, &protocol.ResponseData{
			Title: target.Title, URL: target.URL, SnapshotData: snapshotData, Tab: shortID,
		})

	case protocol.ActionScreenshot:
		result, err := cdp.SessionCommand(target.ID, "Page.captureScreenshot", map[string]interface{}{
			"format": "png", "fromSurface": true,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var screenshot struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(result, &screenshot); err != nil {
			return failResp(req.ID, fmt.Errorf("decode screenshot response: %w", err))
		}
		if screenshot.Data == "" {
			return failResp(req.ID, "screenshot response did not include image data")
		}
		if req.Path != "" {
			data, err := base64.StdEncoding.DecodeString(screenshot.Data)
			if err != nil {
				return failResp(req.ID, fmt.Errorf("decode screenshot data: %w", err))
			}
			if err := os.WriteFile(req.Path, data, 0o644); err != nil {
				return failResp(req.ID, fmt.Errorf("write screenshot: %w", err))
			}
			return okResp(req.ID, &protocol.ResponseData{
				ScreenshotPath: req.Path, Tab: shortID,
			})
		}
		return okResp(req.ID, &protocol.ResponseData{
			DataURL: "data:image/png;base64," + screenshot.Data, Tab: shortID,
		})

	// --- Element interaction ---
	case protocol.ActionClick, protocol.ActionHover:
		if req.Ref == "" {
			return failResp(req.ID, "missing ref parameter")
		}
		seq := tab.RecordAction()
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		x, y, err := getInteractablePoint(cdp, target.ID, backendID)
		if err != nil {
			return failResp(req.ID, err)
		}
		cdp.SessionCommand(target.ID, "Input.dispatchMouseEvent", map[string]interface{}{
			"type": "mouseMoved", "x": x, "y": y, "button": "none",
		})
		if req.Action == protocol.ActionClick {
			mouseClick(cdp, target.ID, x, y)
		}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionFill, protocol.ActionType_:
		if req.Ref == "" {
			return failResp(req.ID, "missing ref parameter")
		}
		seq := tab.RecordAction()
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		clearFirst := req.Action == protocol.ActionFill
		if err := insertTextIntoNode(cdp, target.ID, backendID, req.Text, clearFirst); err != nil {
			return failResp(req.ID, err)
		}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Value: req.Text, Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionCheck, protocol.ActionUncheck:
		if req.Ref == "" {
			return failResp(req.ID, "missing ref parameter")
		}
		seq := tab.RecordAction()
		desired := req.Action == protocol.ActionCheck
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		resolvedRaw, _ := cdp.SessionCommand(target.ID, "DOM.resolveNode", map[string]interface{}{"backendNodeId": backendID})
		var resolved struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		json.Unmarshal(resolvedRaw, &resolved)
		cdp.SessionCommand(target.ID, "Runtime.callFunctionOn", map[string]interface{}{
			"objectId":            resolved.Object.ObjectID,
			"functionDeclaration": fmt.Sprintf(`function() { this.checked = %v; this.dispatchEvent(new Event('input', { bubbles: true })); this.dispatchEvent(new Event('change', { bubbles: true })); }`, desired),
		})
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionSelect:
		if req.Ref == "" || req.Value == "" {
			return failResp(req.ID, "missing ref or value parameter")
		}
		seq := tab.RecordAction()
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		resolvedRaw, _ := cdp.SessionCommand(target.ID, "DOM.resolveNode", map[string]interface{}{"backendNodeId": backendID})
		var resolved struct {
			Object struct {
				ObjectID string `json:"objectId"`
			} `json:"object"`
		}
		json.Unmarshal(resolvedRaw, &resolved)
		valueJSON, _ := json.Marshal(req.Value)
		cdp.SessionCommand(target.ID, "Runtime.callFunctionOn", map[string]interface{}{
			"objectId":            resolved.Object.ObjectID,
			"functionDeclaration": fmt.Sprintf(`function() { this.value = %s; this.dispatchEvent(new Event('input', { bubbles: true })); this.dispatchEvent(new Event('change', { bubbles: true })); }`, string(valueJSON)),
		})
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Value: req.Value, Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionGet:
		if req.Attribute == "" {
			return failResp(req.ID, "missing attribute parameter")
		}
		if req.Attribute == "url" && req.Ref == "" {
			raw, _ := cdp.Evaluate(target.ID, "location.href", true)
			var val string
			json.Unmarshal(raw, &val)
			return okResp(req.ID, &protocol.ResponseData{Value: val, Tab: shortID})
		}
		if req.Attribute == "title" && req.Ref == "" {
			raw, _ := cdp.Evaluate(target.ID, "document.title", true)
			var val string
			json.Unmarshal(raw, &val)
			return okResp(req.ID, &protocol.ResponseData{Value: val, Tab: shortID})
		}
		if req.Ref == "" {
			return failResp(req.ID, "missing ref parameter")
		}
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		val, err := getAttributeValue(cdp, target.ID, backendID, req.Attribute)
		if err != nil {
			return failResp(req.ID, err)
		}
		return okResp(req.ID, &protocol.ResponseData{Value: val, Tab: shortID})

	case protocol.ActionKey:
		seq := tab.RecordAction()
		mods := modifierMask(req.Modifiers)
		keyType := req.KeyType
		if keyType == "" {
			keyType = "press"
		}

		send := func(eventType string, def keyDef, withText bool) error {
			params := map[string]interface{}{
				"type":      eventType,
				"modifiers": mods,
			}
			if def.Key != "" {
				params["key"] = def.Key
			}
			if def.Code != "" {
				params["code"] = def.Code
			}
			if def.KeyCode > 0 {
				params["windowsVirtualKeyCode"] = def.KeyCode
				params["nativeVirtualKeyCode"] = def.KeyCode
			}
			if withText && def.Text != "" {
				params["text"] = def.Text
				params["unmodifiedText"] = def.Text
			}
			_, err := cdp.SessionCommand(target.ID, "Input.dispatchKeyEvent", params)
			return err
		}

		keyDef := resolveKey(req.Key)
		if req.Code != "" {
			keyDef.Code = req.Code
		}

		switch keyType {
		case "type":
			if req.Text == "" {
				return failResp(req.ID, "missing text parameter for keyType=type")
			}
			// keyDown with text inserts the char via Chrome's default handler;
			// keyUp closes the event pair. Playwright-style, no separate char event.
			for _, r := range req.Text {
				def := resolveKey(string(r))
				if err := send("keyDown", def, true); err != nil {
					return failResp(req.ID, err)
				}
				if err := send("keyUp", def, false); err != nil {
					return failResp(req.ID, err)
				}
			}
			return okResp(req.ID, &protocol.ResponseData{Value: req.Text, Tab: shortID, Seq: intPtr(seq)})
		case "down":
			if req.Key == "" {
				return failResp(req.ID, "missing key parameter")
			}
			if err := send("keyDown", keyDef, true); err != nil {
				return failResp(req.ID, err)
			}
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)})
		case "up":
			if req.Key == "" {
				return failResp(req.ID, "missing key parameter")
			}
			if err := send("keyUp", keyDef, false); err != nil {
				return failResp(req.ID, err)
			}
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)})
		case "press":
			if req.Key == "" {
				return failResp(req.ID, "missing key parameter")
			}
			if err := send("keyDown", keyDef, true); err != nil {
				return failResp(req.ID, err)
			}
			if err := send("keyUp", keyDef, false); err != nil {
				return failResp(req.ID, err)
			}
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown keyType: %s", keyType))
		}

	case protocol.ActionMouse:
		seq := tab.RecordAction()
		mouseType := req.MouseType
		if mouseType == "" {
			mouseType = "click"
		}
		button := req.Button
		if button == "" && mouseType != "move" && mouseType != "wheel" {
			button = "left"
		}
		if button == "" {
			button = "none"
		}
		x, y := 0.0, 0.0
		if req.X != nil {
			x = *req.X
		}
		if req.Y != nil {
			y = *req.Y
		}
		clickCount := 1
		if req.ClickCount != nil {
			clickCount = *req.ClickCount
		}
		mods := modifierMask(req.Modifiers)

		send := func(eventType string, extra map[string]interface{}) error {
			params := map[string]interface{}{
				"type":      eventType,
				"x":         x,
				"y":         y,
				"modifiers": mods,
				"button":    button,
			}
			for k, v := range extra {
				params[k] = v
			}
			_, err := cdp.SessionCommand(target.ID, "Input.dispatchMouseEvent", params)
			return err
		}

		switch mouseType {
		case "move":
			if err := send("mouseMoved", nil); err != nil {
				return failResp(req.ID, err)
			}
		case "down":
			if err := send("mousePressed", map[string]interface{}{"clickCount": clickCount}); err != nil {
				return failResp(req.ID, err)
			}
		case "up":
			if err := send("mouseReleased", map[string]interface{}{"clickCount": clickCount}); err != nil {
				return failResp(req.ID, err)
			}
		case "click":
			send("mouseMoved", map[string]interface{}{"button": "none"})
			if err := send("mousePressed", map[string]interface{}{"clickCount": clickCount}); err != nil {
				return failResp(req.ID, err)
			}
			if err := send("mouseReleased", map[string]interface{}{"clickCount": clickCount}); err != nil {
				return failResp(req.ID, err)
			}
		case "wheel":
			dx, dy := 0.0, 0.0
			if req.DeltaX != nil {
				dx = *req.DeltaX
			}
			if req.DeltaY != nil {
				dy = *req.DeltaY
			}
			if err := send("mouseWheel", map[string]interface{}{"deltaX": dx, "deltaY": dy}); err != nil {
				return failResp(req.ID, err)
			}
		default:
			return failResp(req.ID, fmt.Sprintf("unknown mouseType: %s", mouseType))
		}
		return okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)})

	case protocol.ActionClipboardRead:
		// Best-effort permission grant; ignore errors (already granted or unsupported).
		cdp.BrowserCommand("Browser.grantPermissions", map[string]interface{}{
			"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"},
		})
		cdp.SessionCommand(target.ID, "Page.bringToFront", nil)
		raw, err := cdp.Evaluate(target.ID,
			`navigator.clipboard.readText().then(t => t).catch(e => { throw new Error(e && e.message || String(e)); })`,
			true)
		if err != nil {
			return failResp(req.ID, err)
		}
		var val string
		json.Unmarshal(raw, &val)
		return okResp(req.ID, &protocol.ResponseData{Value: val, Tab: shortID})

	case protocol.ActionPress:
		if req.Key == "" {
			return failResp(req.ID, "missing key parameter")
		}
		seq := tab.RecordAction()
		cdp.SessionCommand(target.ID, "Input.dispatchKeyEvent", map[string]interface{}{
			"type": "keyDown", "key": req.Key,
		})
		if len(req.Key) == 1 {
			cdp.SessionCommand(target.ID, "Input.dispatchKeyEvent", map[string]interface{}{
				"type": "char", "text": req.Key, "key": req.Key,
			})
		}
		cdp.SessionCommand(target.ID, "Input.dispatchKeyEvent", map[string]interface{}{
			"type": "keyUp", "key": req.Key,
		})
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionScroll:
		seq := tab.RecordAction()
		pixels := 300
		if req.Pixels != nil {
			pixels = *req.Pixels
		}
		var deltaX, deltaY int
		switch req.Direction {
		case "up":
			deltaY = -pixels
		case "down":
			deltaY = pixels
		case "left":
			deltaX = -pixels
		case "right":
			deltaX = pixels
		}
		cdp.SessionCommand(target.ID, "Input.dispatchMouseEvent", map[string]interface{}{
			"type": "mouseWheel", "x": 0, "y": 0, "deltaX": deltaX, "deltaY": deltaY,
		})
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionWait:
		ms := 1000
		if req.Ms != nil {
			ms = *req.Ms
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return okResp(req.ID, &protocol.ResponseData{Tab: shortID})

	case protocol.ActionEval:
		if req.Script == "" {
			return failResp(req.ID, "missing script parameter")
		}
		seq := tab.RecordAction()
		raw, err := cdp.Evaluate(target.ID, req.Script, true)
		if err != nil {
			return failResp(req.ID, err)
		}
		var result interface{}
		json.Unmarshal(raw, &result)
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{
			Result: result, Tab: shortID, Seq: intPtr(seq),
		}))

	// --- Tab management ---
	case protocol.ActionTabList:
		targets, _ := cdp.GetTargets()
		var tabs []protocol.TabInfo
		idx := 0
		for _, t := range targets {
			if t.Type != "page" {
				continue
			}
			tState := cdp.TabManager.GetTab(t.ID)
			tabShort := strings.ToLower(t.ID[len(t.ID)-4:])
			if tState != nil {
				tabShort = tState.ShortID
			}
			tabs = append(tabs, protocol.TabInfo{
				Index:  idx,
				URL:    t.URL,
				Title:  t.Title,
				Active: t.ID == cdp.CurrentTargetID || (cdp.CurrentTargetID == "" && idx == 0),
				TabID:  t.ID,
				Tab:    tabShort,
			})
			idx++
		}
		activeIdx := 0
		for i, t := range tabs {
			if t.Active {
				activeIdx = i
				break
			}
		}
		return okResp(req.ID, &protocol.ResponseData{Tabs: tabs, ActiveIndex: intPtr(activeIdx)})

	case protocol.ActionTabSelect:
		targets, _ := cdp.GetTargets()
		var pages []CdpTargetInfo
		for _, t := range targets {
			if t.Type == "page" {
				pages = append(pages, t)
			}
		}
		var selected *CdpTargetInfo
		if req.TabID != nil {
			tabIDStr := fmt.Sprintf("%v", req.TabID)
			if resolved := cdp.TabManager.ResolveShortID(tabIDStr); resolved != "" {
				for i, t := range pages {
					if t.ID == resolved {
						selected = &pages[i]
						break
					}
				}
			}
			if selected == nil {
				for i, t := range pages {
					if t.ID == tabIDStr {
						selected = &pages[i]
						break
					}
				}
			}
			if selected == nil {
				if num, err := fmt.Sscanf(tabIDStr, "%d", new(int)); err == nil && num > 0 {
					var idx int
					fmt.Sscanf(tabIDStr, "%d", &idx)
					if idx >= 0 && idx < len(pages) {
						selected = &pages[idx]
					}
				}
			}
		} else {
			idx := 0
			if req.Index != nil {
				idx = *req.Index
			}
			if idx >= 0 && idx < len(pages) {
				selected = &pages[idx]
			}
		}
		if selected == nil {
			return failResp(req.ID, "tab not found")
		}
		cdp.CurrentTargetID = selected.ID
		cdp.AttachAndEnable(selected.ID)
		// tab_select is a focus switch — bring the tab to the foreground in
		// Chrome's UI, not just route the daemon's command stream.
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": selected.ID})
		cdp.SessionCommand(selected.ID, "Page.bringToFront", nil)
		selTab := cdp.TabManager.GetTab(selected.ID)
		tabShort := ""
		if selTab != nil {
			tabShort = selTab.ShortID
		}
		return okResp(req.ID, &protocol.ResponseData{
			TabID: selected.ID, URL: selected.URL, Title: selected.Title, Tab: tabShort,
		})

	case protocol.ActionTabClose:
		targets, _ := cdp.GetTargets()
		var pages []CdpTargetInfo
		for _, t := range targets {
			if t.Type == "page" {
				pages = append(pages, t)
			}
		}
		var selected *CdpTargetInfo
		if req.TabID != nil {
			tabIDStr := fmt.Sprintf("%v", req.TabID)
			if resolved := cdp.TabManager.ResolveShortID(tabIDStr); resolved != "" {
				for i, t := range pages {
					if t.ID == resolved {
						selected = &pages[i]
						break
					}
				}
			}
			if selected == nil {
				for i, t := range pages {
					if t.ID == tabIDStr {
						selected = &pages[i]
						break
					}
				}
			}
			if selected == nil {
				if num, err := fmt.Sscanf(tabIDStr, "%d", new(int)); err == nil && num > 0 {
					var idx int
					fmt.Sscanf(tabIDStr, "%d", &idx)
					if idx >= 0 && idx < len(pages) {
						selected = &pages[idx]
					}
				}
			}
		} else {
			idx := 0
			if req.Index != nil {
				idx = *req.Index
			}
			if idx >= 0 && idx < len(pages) {
				selected = &pages[idx]
			}
		}
		if selected == nil {
			return failResp(req.ID, "tab not found")
		}
		closedTab := cdp.TabManager.GetTab(selected.ID)
		closedShort := ""
		if closedTab != nil {
			closedShort = closedTab.ShortID
		}
		cdp.BrowserCommand("Target.closeTarget", map[string]interface{}{"targetId": selected.ID})
		if cdp.CurrentTargetID == selected.ID {
			cdp.CurrentTargetID = ""
		}
		return okResp(req.ID, &protocol.ResponseData{TabID: selected.ID, Tab: closedShort})

	// --- Frame ---
	case protocol.ActionFrame:
		if req.Selector == "" {
			return failResp(req.ID, "missing selector parameter")
		}
		seq := tab.RecordAction()
		docRaw, _ := cdp.PageCommand(target.ID, "DOM.getDocument", nil)
		var doc struct {
			Root struct {
				NodeID int `json:"nodeId"`
			} `json:"root"`
		}
		json.Unmarshal(docRaw, &doc)

		nodeRaw, _ := cdp.PageCommand(target.ID, "DOM.querySelector", map[string]interface{}{
			"nodeId": doc.Root.NodeID, "selector": req.Selector,
		})
		var node struct {
			NodeID int `json:"nodeId"`
		}
		json.Unmarshal(nodeRaw, &node)
		if node.NodeID == 0 {
			return failResp(req.ID, fmt.Sprintf("iframe not found: %s", req.Selector))
		}

		descRaw, _ := cdp.PageCommand(target.ID, "DOM.describeNode", map[string]interface{}{"nodeId": node.NodeID})
		var desc struct {
			Node struct {
				FrameID    string   `json:"frameId"`
				NodeName   string   `json:"nodeName"`
				Attributes []string `json:"attributes"`
			} `json:"node"`
		}
		json.Unmarshal(descRaw, &desc)

		if desc.Node.FrameID == "" {
			return failResp(req.ID, fmt.Sprintf("cannot get iframe frameId: %s", req.Selector))
		}
		nodeName := strings.ToLower(desc.Node.NodeName)
		if nodeName != "" && nodeName != "iframe" && nodeName != "frame" {
			return failResp(req.ID, fmt.Sprintf("element is not an iframe: %s", nodeName))
		}
		tab.ActiveFrameID = desc.Node.FrameID

		attrMap := make(map[string]string)
		for i := 0; i+1 < len(desc.Node.Attributes); i += 2 {
			attrMap[desc.Node.Attributes[i]] = desc.Node.Attributes[i+1]
		}
		return okResp(req.ID, &protocol.ResponseData{
			FrameInfo: map[string]interface{}{
				"selector": req.Selector, "name": attrMap["name"], "url": attrMap["src"], "frameId": desc.Node.FrameID,
			},
			Tab: shortID, Seq: intPtr(seq),
		})

	case protocol.ActionFrameMain:
		seq := tab.RecordAction()
		tab.ActiveFrameID = ""
		return okResp(req.ID, &protocol.ResponseData{
			FrameInfo: map[string]interface{}{"frameId": 0},
			Tab:       shortID, Seq: intPtr(seq),
		})

	// --- Dialog ---
	case protocol.ActionDialog:
		seq := tab.RecordAction()
		accept := req.DialogResponse != "dismiss"
		tab.DialogHandler = &DialogHandler{Accept: accept, PromptText: req.PromptText}
		cdp.SessionCommand(target.ID, "Page.enable", nil)
		resp := "accept"
		if !accept {
			resp = "dismiss"
		}
		return okResp(req.ID, &protocol.ResponseData{
			DialogInfo: map[string]interface{}{
				"type": "armed", "message": fmt.Sprintf("Dialog handler armed: %s", resp), "handled": false,
			},
			Tab: shortID, Seq: intPtr(seq),
		})

	// --- Network ---
	case protocol.ActionNetwork:
		subCmd := req.NetworkCommand
		if subCmd == "" {
			subCmd = "requests"
		}
		switch subCmd {
		case "requests":
			qr := tab.GetNetworkRequests(QueryOptions{
				Since: req.Since, Filter: req.Filter, Method: req.Method, Status: req.Status,
				Limit: derefInt(req.Limit),
			})
			// Fetch bodies if requested
			if req.WithBody {
				for i := range qr.Items {
					item := &qr.Items[i]
					if item.Failed || item.ResponseBody != "" || item.BodyError != "" {
						continue
					}
					bodyRaw, err := cdp.SessionCommand(target.ID, "Network.getResponseBody", map[string]interface{}{
						"requestId": item.RequestID,
					})
					if err != nil {
						item.BodyError = err.Error()
						continue
					}
					var body struct {
						Body          string `json:"body"`
						Base64Encoded bool   `json:"base64Encoded"`
					}
					json.Unmarshal(bodyRaw, &body)
					item.ResponseBody = body.Body
					item.ResponseBodyBase64 = body.Base64Encoded
				}
			}
			return okResp(req.ID, &protocol.ResponseData{
				NetworkRequests: qr.Items, Tab: shortID, Cursor: intPtr(qr.Cursor),
			})
		case "clear":
			tab.ClearNetwork()
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID})
		case "route", "unroute":
			return okResp(req.ID, &protocol.ResponseData{RouteCount: intPtr(0), Tab: shortID})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown network subcommand: %s", subCmd))
		}

	// --- Console ---
	case protocol.ActionConsole:
		subCmd := req.ConsoleCommand
		if subCmd == "" {
			subCmd = "get"
		}
		switch subCmd {
		case "get":
			qr := tab.GetConsoleMessages(QueryOptions{
				Since: req.Since, Filter: req.Filter, Limit: derefInt(req.Limit),
			})
			return okResp(req.ID, &protocol.ResponseData{
				ConsoleMessages: qr.Items, Tab: shortID, Cursor: intPtr(qr.Cursor),
			})
		case "clear":
			tab.ClearConsole()
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown console subcommand: %s", subCmd))
		}

	// --- Errors ---
	case protocol.ActionErrors:
		subCmd := req.ErrorsCommand
		if subCmd == "" {
			subCmd = "get"
		}
		switch subCmd {
		case "get":
			qr := tab.GetJSErrors(QueryOptions{
				Since: req.Since, Filter: req.Filter, Limit: derefInt(req.Limit),
			})
			return okResp(req.ID, &protocol.ResponseData{
				JSErrors: qr.Items, Tab: shortID, Cursor: intPtr(qr.Cursor),
			})
		case "clear":
			tab.ClearErrors()
			return okResp(req.ID, &protocol.ResponseData{Tab: shortID})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown errors subcommand: %s", subCmd))
		}

	// --- Trace ---
	case protocol.ActionTrace:
		subCmd := req.TraceCommand
		if subCmd == "" {
			subCmd = "status"
		}
		traceMu.Lock()
		defer traceMu.Unlock()
		switch subCmd {
		case "start":
			traceRecording = true
			traceEvents = nil
			return okResp(req.ID, &protocol.ResponseData{
				TraceStatus: &protocol.TraceStatus{Recording: true, EventCount: 0}, Tab: shortID,
			})
		case "stop":
			traceRecording = false
			events := make([]protocol.TraceEvent, len(traceEvents))
			copy(events, traceEvents)
			return okResp(req.ID, &protocol.ResponseData{
				TraceEvents: events,
				TraceStatus: &protocol.TraceStatus{Recording: false, EventCount: len(events)},
				Tab:         shortID,
			})
		case "status":
			return okResp(req.ID, &protocol.ResponseData{
				TraceStatus: &protocol.TraceStatus{Recording: traceRecording, EventCount: len(traceEvents)},
				Tab:         shortID,
			})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown trace subcommand: %s", subCmd))
		}

	// --- History ---
	case protocol.ActionHistory:
		return failResp(req.ID, "history command is not supported in daemon mode")

	default:
		return failResp(req.ID, fmt.Sprintf("unknown action: %s", req.Action))
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
