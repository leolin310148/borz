package daemon

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

//go:embed embed/buildDomTree.js
var embeddedFS embed.FS

var (
	buildDomTreeScript     string
	buildDomTreeScriptOnce sync.Once
	screenshotMaskSequence atomic.Uint64
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

func resolvePageTarget(cdp *CdpConnection, pages []CdpTargetInfo, tabID interface{}, index *int) *CdpTargetInfo {
	if tabID != nil {
		tabIDStr := fmt.Sprintf("%v", tabID)
		if resolved := cdp.TabManager.ResolveShortID(tabIDStr); resolved != "" {
			for i, t := range pages {
				if t.ID == resolved {
					return &pages[i]
				}
			}
		}
		for i, t := range pages {
			if t.ID == tabIDStr {
				return &pages[i]
			}
		}
		if idx, err := strconv.Atoi(tabIDStr); err == nil && idx >= 0 && idx < len(pages) {
			return &pages[idx]
		}
		return nil
	}

	idx := 0
	if index != nil {
		idx = *index
	}
	if idx >= 0 && idx < len(pages) {
		return &pages[idx]
	}
	return nil
}

func siteDomainMatchesURL(domain, rawURL string) bool {
	domain = normalizeSiteDomain(domain)
	if domain == "" {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func normalizeSiteDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" || domain == "*" {
		return ""
	}
	if strings.Contains(domain, "://") {
		if u, err := url.Parse(domain); err == nil {
			domain = u.Hostname()
		}
	}
	domain = strings.TrimPrefix(domain, "*.")
	domain = strings.TrimPrefix(domain, ".")
	if h, _, ok := strings.Cut(domain, ":"); ok {
		domain = h
	}
	if slash := strings.IndexByte(domain, '/'); slash >= 0 {
		domain = domain[:slash]
	}
	return strings.TrimSpace(domain)
}

func siteAdapterStartURL(req *protocol.Request) string {
	if req == nil {
		return ""
	}
	if strings.TrimSpace(req.SiteStartURL) != "" {
		return strings.TrimSpace(req.SiteStartURL)
	}
	domain := normalizeSiteDomain(req.SiteDomain)
	if domain == "" {
		return ""
	}
	return "https://" + domain + "/"
}

func selectSiteAdapterTarget(cdp *CdpConnection, targetID string) {
	cdp.SetCurrentTargetID(targetID)
	cdp.AttachAndEnable(targetID)
	cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": targetID})
	cdp.SessionCommand(targetID, "Page.bringToFront", nil)
}

func ensureSiteAdapterTarget(cdp *CdpConnection, req *protocol.Request) (*CdpTargetInfo, error) {
	domain := normalizeSiteDomain(req.SiteDomain)
	if domain == "" {
		return nil, nil
	}

	targets, err := cdp.GetTargets()
	if err != nil {
		return nil, err
	}

	var pages []CdpTargetInfo
	for _, t := range targets {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}

	var current *CdpTargetInfo
	if currentID := cdp.GetCurrentTargetID(); currentID != "" {
		for i := range pages {
			if pages[i].ID == currentID {
				current = &pages[i]
				break
			}
		}
	}
	if current == nil && len(pages) > 0 {
		current = &pages[0]
	}
	if current != nil && siteDomainMatchesURL(domain, current.URL) {
		return nil, nil
	}

	for i := range pages {
		if siteDomainMatchesURL(domain, pages[i].URL) {
			selectSiteAdapterTarget(cdp, pages[i].ID)
			return &pages[i], nil
		}
	}

	startURL := siteAdapterStartURL(req)
	if startURL == "" {
		return nil, nil
	}
	result, err := cdp.BrowserCommand("Target.createTarget", map[string]interface{}{
		"url": startURL,
	})
	if err != nil {
		return nil, err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	json.Unmarshal(result, &created)
	if created.TargetID == "" {
		return nil, fmt.Errorf("Target.createTarget returned empty targetId")
	}
	selectSiteAdapterTarget(cdp, created.TargetID)
	waitForTabNavigated(cdp, created.TargetID, startURL, newTabReadyTimeout)
	return &CdpTargetInfo{ID: created.TargetID, Type: "page", URL: startURL}, nil
}

func intPtr(v int) *int { return &v }

// applyWaitFor honors req.WaitFor / req.TimeoutMs by polling for the selector.
// Returns nil immediately if WaitFor is empty.
func applyWaitFor(cdp *CdpConnection, targetID string, req *protocol.Request) error {
	if req.WaitFor == "" {
		return nil
	}
	timeout := 10 * time.Second
	if req.TimeoutMs != nil {
		if *req.TimeoutMs >= 0 {
			timeout = time.Duration(*req.TimeoutMs) * time.Millisecond
		}
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
// 100ms ticks until truthy or timeout. Runtime probes use a short bounded CDP
// timeout so an unresponsive renderer cannot consume the entire command
// deadline in one call. Navigation context errors are retried and the final
// error includes the last page state/probe failure for diagnosis.
func waitForSelector(cdp *CdpConnection, targetID, selector string, timeout time.Duration) error {
	selJSON, _ := json.Marshal(selector)
	expr := fmt.Sprintf(`JSON.stringify({
		found: !!document.querySelector(%s),
		href: location.href,
		title: document.title,
		readyState: document.readyState
	})`, string(selJSON))
	deadline := time.Now().Add(timeout)
	var lastState struct {
		Found      bool   `json:"found"`
		Href       string `json:"href"`
		Title      string `json:"title"`
		ReadyState string `json:"readyState"`
	}
	var lastProbeErr error
	for {
		probeTimeout := time.Second
		if remaining := time.Until(deadline); remaining > 0 && remaining < probeTimeout {
			probeTimeout = remaining
		} else if remaining <= 0 {
			probeTimeout = 250 * time.Millisecond
		}
		raw, err := cdp.EvaluateWithTimeout(targetID, expr, true, probeTimeout)
		if err == nil {
			var encoded string
			if decodeErr := json.Unmarshal(raw, &encoded); decodeErr == nil {
				if decodeErr = json.Unmarshal([]byte(encoded), &lastState); decodeErr == nil {
					lastProbeErr = nil
					if lastState.Found {
						return nil
					}
				} else {
					lastProbeErr = fmt.Errorf("decode page state: %w", decodeErr)
				}
			} else {
				// Keep compatibility with older/fake CDP responders that return
				// the selector boolean directly instead of our diagnostic object.
				var found bool
				if boolErr := json.Unmarshal(raw, &found); boolErr == nil {
					lastProbeErr = nil
					if found {
						return nil
					}
				} else {
					lastProbeErr = fmt.Errorf("decode wait probe: %w", decodeErr)
				}
			}
		} else {
			lastProbeErr = err
		}
		if !time.Now().Before(deadline) {
			if lastState.Href == "" {
				lastState.Href, lastState.Title = waitForTargetInfo(cdp, targetID)
			}
			details := make([]string, 0, 4)
			if lastState.Href != "" {
				details = append(details, fmt.Sprintf("current URL %q", lastState.Href))
			}
			if lastState.Title != "" {
				details = append(details, fmt.Sprintf("title %q", truncateDiagnostic(lastState.Title, 160)))
			}
			if lastState.ReadyState != "" {
				details = append(details, fmt.Sprintf("readyState %q", lastState.ReadyState))
			}
			if lastProbeErr != nil {
				details = append(details, fmt.Sprintf("last probe error: %s", truncateDiagnostic(lastProbeErr.Error(), 240)))
			}
			if len(details) == 0 {
				return fmt.Errorf("wait-for selector %q: timeout after %s", selector, timeout)
			}
			return fmt.Errorf("wait-for selector %q: timeout after %s (%s)", selector, timeout, strings.Join(details, ", "))
		}
		pause := min(100*time.Millisecond, time.Until(deadline))
		if pause > 0 {
			time.Sleep(pause)
		}
	}
}

func waitForTargetInfo(cdp *CdpConnection, targetID string) (url, title string) {
	raw, err := cdp.BrowserCommandWithTimeout("Target.getTargets", nil, 500*time.Millisecond)
	if err != nil {
		return "", ""
	}
	var data struct {
		TargetInfos []CdpTargetInfo `json:"targetInfos"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return "", ""
	}
	for _, target := range data.TargetInfos {
		if target.ID == targetID {
			return target.URL, target.Title
		}
	}
	return "", ""
}

func truncateDiagnostic(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// --- Snapshot ---

// buildSnapshot returns the regular SnapshotData for the request and, when
// req.Diff is true, also a SnapshotDiffData computed against the tab's
// previous baseline. The previous baseline (tab.PrevDiffSnapshot) is
// rewritten on every successful tree-mode call so the next --diff has
// something to compare against.
//
// Text-mode snapshots cannot be diffed (they have no structural ancestors)
// and clear the baseline so a subsequent --diff returns a clean reset
// rather than diffing against a stale tree-mode tree.
func buildSnapshot(cdp *CdpConnection, targetID, url string, tab *TabState, req *protocol.Request) (*protocol.SnapshotData, *protocol.SnapshotDiffData, error) {
	if req.Mode == "text" {
		if req.Diff {
			return nil, nil, fmt.Errorf("snapshot --diff is not supported in text mode")
		}
		snap, err := buildTextSnapshot(cdp, targetID)
		if err != nil {
			return nil, nil, err
		}
		// Text mode is observation-only and does not replace the actionable
		// ref map established by the latest tree snapshot. It may clear the
		// diff baseline, but callers can still act on the previous refs.
		tab.PrevDiffSnapshot = nil
		return snap, nil, nil
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
		tab.PrevDiffSnapshot = nil
		return &protocol.SnapshotData{Snapshot: title, Refs: map[string]*protocol.RefInfo{}}, nil, nil
	}

	var result buildDomTreeResult
	if err := json.Unmarshal(raw, &result); err != nil || result.RootID == "" {
		tab.Refs = map[string]*protocol.RefInfo{}
		tab.PrevDiffSnapshot = nil
		return &protocol.SnapshotData{Snapshot: "", Refs: map[string]*protocol.RefInfo{}}, nil, nil
	}

	snapshot := ConvertBuildDomTreeResult(&result, req.Interactive, req.Compact, req.MaxDepth, req.Selector, req.Role)
	tab.Refs = snapshot.Refs

	// Always build the structural DiffSnapshot — it's cheap and being
	// always-on means whether or not the *current* call asked for --diff,
	// the *next* call has a baseline.
	currDiff := BuildDiffSnapshot(&result, url)
	var diffData *protocol.SnapshotDiffData
	if req.Diff {
		diffData = DiffSnapshots(tab.PrevDiffSnapshot, currDiff)
	}
	tab.PrevDiffSnapshot = currDiff
	return snapshot, diffData, nil
}

// captureScreenshot hides the DOM overlay produced by snapshot while Chrome
// captures the page, then restores it. The temporary stylesheet is unique per
// capture so concurrent screenshots of the same tab cannot unmask each other.
// Masking is best-effort: a page without an execution context should still be
// screenshot-able through CDP.
func captureScreenshot(cdp *CdpConnection, targetID string, tab *TabState, annotations []protocol.ScreenshotAnnotation) (json.RawMessage, error) {
	token := strconv.FormatUint(screenshotMaskSequence.Add(1), 10)
	tokenJSON, _ := json.Marshal(token)
	hideScript := fmt.Sprintf(`((token) => {
		const style = document.createElement('style');
		style.setAttribute('data-borz-screenshot-mask', token);
		style.textContent = '#playwright-highlight-container, [data-borz-snapshot-highlight-container] { visibility: hidden !important; }';
		(document.head || document.documentElement).appendChild(style);
	})(%s)`, tokenJSON)
	if _, err := cdp.Evaluate(targetID, hideScript, false); err == nil {
		defer func() {
			restoreScript := fmt.Sprintf(`((token) => {
				for (const style of document.querySelectorAll('style[data-borz-screenshot-mask]')) {
					if (style.getAttribute('data-borz-screenshot-mask') === token) style.remove();
				}
			})(%s)`, tokenJSON)
			_, _ = cdp.Evaluate(targetID, restoreScript, false)
		}()
	}

	var annotationObjectIDs []string
	defer func() {
		cleanupScript := `function(token) {
			const doc = this.ownerDocument;
			for (const node of doc.querySelectorAll('[data-borz-screenshot-annotation]')) {
				if (node.getAttribute('data-borz-screenshot-annotation') === token) node.remove();
			}
		}`
		for _, objectID := range annotationObjectIDs {
			_, _ = cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
				"objectId":            objectID,
				"functionDeclaration": cleanupScript,
				"arguments":           []interface{}{map[string]interface{}{"value": token}},
			})
		}
	}()

	if len(annotations) > 20 {
		return nil, fmt.Errorf("screenshot supports at most 20 annotations")
	}
	for _, annotation := range annotations {
		ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(annotation.Ref), "@"))
		if ref == "" {
			return nil, fmt.Errorf("screenshot annotation ref is required")
		}
		if strings.TrimSpace(annotation.Text) == "" {
			return nil, fmt.Errorf("screenshot annotation text is required for ref %s", ref)
		}
		backendNodeID, err := parseRef(cdp, targetID, tab, ref)
		if err != nil {
			return nil, fmt.Errorf("screenshot annotation %s: %w", ref, err)
		}
		objectID, err := addScreenshotAnnotation(cdp, targetID, backendNodeID, token, annotation.Text)
		if err != nil {
			return nil, fmt.Errorf("screenshot annotation %s: %w", ref, err)
		}
		annotationObjectIDs = append(annotationObjectIDs, objectID)
	}

	return cdp.SessionCommand(targetID, "Page.captureScreenshot", map[string]interface{}{
		"format": "png", "fromSurface": true,
	})
}

func addScreenshotAnnotation(cdp *CdpConnection, targetID string, backendNodeID int, token, text string) (string, error) {
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
	if err := json.Unmarshal(resolvedRaw, &resolved); err != nil {
		return "", fmt.Errorf("decode resolved element: %w", err)
	}
	if resolved.Object.ObjectID == "" {
		return "", fmt.Errorf("DOM.resolveNode returned no object for backend node %d", backendNodeID)
	}

	callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": `function(token, text) {
			if (!(this instanceof Element)) throw new Error('Ref does not resolve to an element');
			const rect = this.getBoundingClientRect();
			const doc = this.ownerDocument;
			const view = doc.defaultView;
			if (!rect || rect.width <= 0 || rect.height <= 0) throw new Error('Element is not visible');
			if (rect.bottom <= 0 || rect.right <= 0 || rect.top >= view.innerHeight || rect.left >= view.innerWidth) {
				throw new Error('Element is outside the visible viewport');
			}
			const root = doc.body || doc.documentElement;
			const box = doc.createElement('div');
			box.setAttribute('data-borz-screenshot-annotation', token);
			Object.assign(box.style, {
				position: 'fixed', pointerEvents: 'none', boxSizing: 'border-box',
				left: (rect.left - 4) + 'px', top: (rect.top - 4) + 'px',
				width: (rect.width + 8) + 'px', height: (rect.height + 8) + 'px',
				border: '3px solid #e11d48', borderRadius: '6px',
				background: 'rgba(225, 29, 72, 0.08)', zIndex: '2147483646'
			});
			const label = doc.createElement('div');
			label.setAttribute('data-borz-screenshot-annotation', token);
			label.textContent = text;
			Object.assign(label.style, {
				position: 'fixed', pointerEvents: 'none', boxSizing: 'border-box',
				maxWidth: Math.max(1, Math.min(360, view.innerWidth - 16)) + 'px',
				padding: '7px 10px', color: '#ffffff', background: '#e11d48',
				borderRadius: '6px', boxShadow: '0 2px 8px rgba(0,0,0,.28)',
				font: '600 14px/1.35 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
				whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', zIndex: '2147483647'
			});
			root.append(box, label);
			const labelRect = label.getBoundingClientRect();
			const left = Math.max(8, Math.min(rect.left, view.innerWidth - labelRect.width - 8));
			const above = rect.top - labelRect.height - 10;
			const top = above >= 8 ? above : Math.min(view.innerHeight - labelRect.height - 8, rect.bottom + 10);
			label.style.left = left + 'px';
			label.style.top = Math.max(8, top) + 'px';
			return true;
		}`,
		"arguments": []interface{}{
			map[string]interface{}{"value": token},
			map[string]interface{}{"value": text},
		},
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var call struct {
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(callRaw, &call); err != nil {
		return "", fmt.Errorf("decode annotation result: %w", err)
	}
	if call.ExceptionDetails != nil {
		message := strings.TrimSpace(call.ExceptionDetails.Exception.Description)
		if message == "" {
			message = strings.TrimSpace(call.ExceptionDetails.Text)
		}
		if message == "" {
			message = "failed to render annotation"
		}
		return "", fmt.Errorf("%s", message)
	}
	return resolved.Object.ObjectID, nil
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
	if resolved.Object.ObjectID == "" {
		return 0, 0, fmt.Errorf("DOM.resolveNode returned no object for backend node %d", backendNodeID)
	}

	callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": `function() {
			if (!(this instanceof Element)) throw new Error('Ref does not resolve to an element');
			this.scrollIntoView({ behavior: 'instant', block: 'center', inline: 'center' });
			const rect = this.getBoundingClientRect();
			if (!rect || rect.width <= 0 || rect.height <= 0) throw new Error('Element is not visible');
			let x = rect.left + rect.width / 2;
			let y = rect.top + rect.height / 2;
			const describe = (element) => {
				if (!element || element.nodeType !== 1) return String(element);
				let out = element.tagName.toLowerCase();
				if (element.id) out += '#' + element.id;
				else if (element.classList.length) out += '.' + Array.from(element.classList).slice(0, 2).join('.');
				return out;
			};
			const hitBelongsToControl = (expected, hit) => {
				if (!hit || hit.nodeType !== 1) return false;
				if (hit === expected || expected.contains(hit)) return true;
				const label = expected.closest('label');
				if (label && label.contains(hit)) return true;
				// Component libraries commonly expose the inner input as the
				// accessible combobox while rendering tags/placeholders as sibling
				// elements above it. Treat a nearby sibling inside the same control
				// as an intentional hit target, but keep the search tightly bounded.
				if (expected.matches('[role="combobox"], input[aria-haspopup="listbox"]')) {
					let root = expected.parentElement;
					for (let depth = 0; root && depth < 4; depth += 1, root = root.parentElement) {
						if (root.contains(hit)) return true;
					}
				}
				return false;
			};
			const candidates = [
				[0.5, 0.5], [0.15, 0.5], [0.85, 0.5],
				[0.5, 0.2], [0.5, 0.8], [0.15, 0.2], [0.85, 0.8]
			];
			let initialHit = null;
			let foundPoint = false;
			for (const [rx, ry] of candidates) {
				const candidateX = rect.left + rect.width * rx;
				const candidateY = rect.top + rect.height * ry;
				const hit = this.ownerDocument.elementFromPoint(candidateX, candidateY);
				if (hitBelongsToControl(this, hit)) {
					x = candidateX;
					y = candidateY;
					initialHit = hit;
					foundPoint = true;
					break;
				}
			}
			if (!foundPoint) {
				initialHit = this.ownerDocument.elementFromPoint(x, y);
				throw new Error('Element is not clickable at its center; hit ' + describe(initialHit) + ' instead of ' + describe(this));
			}
			let expected = this;
			let view = this.ownerDocument.defaultView;
			while (view) {
				const hit = view === this.ownerDocument.defaultView ? initialHit : view.document.elementFromPoint(x, y);
				if (!hitBelongsToControl(expected, hit)) {
					throw new Error('Element is not clickable at its center; hit ' + describe(hit) + ' instead of ' + describe(expected));
				}
				if (!view.frameElement) break;
				const frame = view.frameElement;
				const frameRect = frame.getBoundingClientRect();
				x += frameRect.left + frame.clientLeft;
				y += frameRect.top + frame.clientTop;
				expected = frame;
				view = frame.ownerDocument.defaultView;
			}
			return { x, y };
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
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	json.Unmarshal(callRaw, &call)

	if call.ExceptionDetails != nil {
		message := strings.TrimSpace(call.ExceptionDetails.Exception.Description)
		if message == "" {
			message = strings.TrimSpace(call.ExceptionDetails.Text)
		}
		if message == "" {
			message = "failed to calculate an interactable point"
		}
		return 0, 0, fmt.Errorf("%s", message)
	}
	return call.Result.Value.X, call.Result.Value.Y, nil
}

func mouseClick(cdp *CdpConnection, targetID string, x, y float64) error {
	if _, err := cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseMoved", "x": x, "y": y, "button": "none",
	}); err != nil {
		return fmt.Errorf("move pointer before click: %w", err)
	}
	if _, err := cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1,
	}); err != nil {
		return fmt.Errorf("press mouse button: %w", err)
	}
	if _, err := cdp.SessionCommand(targetID, "Input.dispatchMouseEvent", map[string]interface{}{
		"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1,
	}); err != nil {
		return fmt.Errorf("release mouse button: %w", err)
	}
	return nil
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

	if resolved.Object.ObjectID == "" {
		return fmt.Errorf("DOM.resolveNode returned no object for backend node %d", backendNodeID)
	}

	if clearFirst {
		callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
			"objectId": resolved.Object.ObjectID,
			"functionDeclaration": `function(value) {
			if (typeof this.scrollIntoView === 'function') this.scrollIntoView({ behavior: 'auto', block: 'center', inline: 'center' });
			if (typeof this.focus === 'function') this.focus();
			if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement) {
				const prototype = this instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
				const descriptor = Object.getOwnPropertyDescriptor(prototype, 'value');
				if (descriptor && typeof descriptor.set === 'function') descriptor.set.call(this, value);
				else this.value = value;
				if (typeof this.setSelectionRange === 'function') this.setSelectionRange(value.length, value.length);
				const inputEvent = typeof InputEvent === 'function'
					? new InputEvent('input', { bubbles: true, inputType: value ? 'insertText' : 'deleteContentBackward', data: value || null })
					: new Event('input', { bubbles: true });
				this.dispatchEvent(inputEvent);
				this.dispatchEvent(new Event('change', { bubbles: true }));
				return { ok: true };
			}
			if (this instanceof HTMLElement && this.isContentEditable) {
				this.textContent = value;
				const selection = window.getSelection();
				if (selection) { const range = document.createRange(); range.selectNodeContents(this); range.collapse(false); selection.removeAllRanges(); selection.addRange(range); }
				this.dispatchEvent(new Event('input', { bubbles: true }));
				this.dispatchEvent(new Event('change', { bubbles: true }));
				return { ok: true };
			}
			return { ok: false, error: 'element is not an input, textarea, or contenteditable' };
		}`,
			"arguments":     []map[string]interface{}{{"value": text}},
			"returnByValue": true,
		})
		if err != nil {
			return err
		}
		var call struct {
			Result struct {
				Value struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(callRaw, &call); err != nil {
			return fmt.Errorf("decode fill result: %w", err)
		}
		if !call.Result.Value.OK {
			return fmt.Errorf("fill action failed: %s", call.Result.Value.Error)
		}
		return nil
	}

	prepRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": `function() {
			if (typeof this.scrollIntoView === 'function') this.scrollIntoView({ behavior: 'auto', block: 'center', inline: 'center' });
			if (typeof this.focus === 'function') this.focus();
			if (this instanceof HTMLInputElement || this instanceof HTMLTextAreaElement) {
				if (typeof this.setSelectionRange === 'function') { const end = this.value.length; this.setSelectionRange(end, end); }
				return true;
			}
			if (this instanceof HTMLElement && this.isContentEditable) {
				const selection = window.getSelection();
				if (selection) { const range = document.createRange(); range.selectNodeContents(this); range.collapse(false); selection.removeAllRanges(); selection.addRange(range); }
				return true;
			}
			return false;
		}`,
		"returnByValue": true,
	})
	if err != nil {
		return err
	}
	var prep struct {
		Result struct {
			Value interface{} `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(prepRaw, &prep) != nil {
		return fmt.Errorf("decode type preparation result")
	}
	if ok, isBool := prep.Result.Value.(bool); isBool && !ok {
		return fmt.Errorf("type action failed: element is not an input, textarea, or contenteditable")
	}

	if text != "" {
		if _, err := cdp.SessionCommand(targetID, "DOM.focus", map[string]interface{}{"backendNodeId": backendNodeID}); err != nil {
			return err
		}
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

// resolveLocalFiles validates and absolutizes file paths against the daemon's
// filesystem (where Chrome runs). Shared by upload and filechooser.
func resolveLocalFiles(paths []string) ([]string, error) {
	absFiles := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", p, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", abs, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%q is a directory, not a file", abs)
		}
		absFiles = append(absFiles, abs)
	}
	return absFiles, nil
}

// resolveFileInputBackendID accepts either a file input ref or a ref for its
// associated label/label descendant. Accessibility snapshots often expose the
// visible label while omitting the hidden input, so requiring the ref to point
// directly at the input makes otherwise ordinary upload controls unusable.
func resolveFileInputBackendID(cdp *CdpConnection, targetID string, backendNodeID int) (int, error) {
	resolvedRaw, err := cdp.SessionCommand(targetID, "DOM.resolveNode", map[string]interface{}{
		"backendNodeId": backendNodeID,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve upload ref: %w", err)
	}
	var resolved struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if err := json.Unmarshal(resolvedRaw, &resolved); err != nil || resolved.Object.ObjectID == "" {
		return 0, fmt.Errorf("upload ref could not be resolved to a DOM node")
	}

	callRaw, err := cdp.SessionCommand(targetID, "Runtime.callFunctionOn", map[string]interface{}{
		"objectId": resolved.Object.ObjectID,
		"functionDeclaration": `function() {
			const isFileInput = node => node instanceof HTMLInputElement && node.type === 'file';
			if (isFileInput(this)) return this;
			if (this instanceof HTMLLabelElement) {
				if (isFileInput(this.control)) return this.control;
				const nested = this.querySelector('input[type="file"]');
				if (isFileInput(nested)) return nested;
			}
			const label = typeof this.closest === 'function' ? this.closest('label') : null;
			if (label) {
				if (isFileInput(label.control)) return label.control;
				const nested = label.querySelector('input[type="file"]');
				if (isFileInput(nested)) return nested;
			}
			return null;
		}`,
		"returnByValue": false,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve associated file input: %w", err)
	}
	var call struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(callRaw, &call); err != nil || call.Result.ObjectID == "" {
		return 0, fmt.Errorf("upload ref is not an <input type=file> or an associated <label>")
	}

	describedRaw, err := cdp.SessionCommand(targetID, "DOM.describeNode", map[string]interface{}{
		"objectId": call.Result.ObjectID,
	})
	if err != nil {
		return 0, fmt.Errorf("describe associated file input: %w", err)
	}
	var described struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := json.Unmarshal(describedRaw, &described); err != nil || described.Node.BackendNodeID == 0 {
		return 0, fmt.Errorf("associated file input has no backend DOM node")
	}
	return described.Node.BackendNodeID, nil
}

// editingCommandsFor auto-maps meta-modifier key combos to CDP editing
// commands (Input.dispatchKeyEvent `commands`). Synthesized key events never
// reach the browser-process shortcut layer — on macOS that layer owns
// Cmd+A/C/X/V/Z — so without the commands parameter these combos are no-ops
// in editable content. Only meta combos are mapped: on non-mac browsers the
// meta combo has no native default handler, so the command still executes
// exactly once (ctrl combos are left alone — they already work natively via
// the renderer and would double-execute if mapped).
func editingCommandsFor(key string, mods []string) []string {
	meta, shift, other := false, false, false
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "meta", "cmd", "command":
			meta = true
		case "shift":
			shift = true
		default:
			other = true
		}
	}
	if !meta || other {
		return nil
	}
	switch strings.ToLower(key) {
	case "a":
		if !shift {
			return []string{"selectAll"}
		}
	case "c":
		if !shift {
			return []string{"copy"}
		}
	case "x":
		if !shift {
			return []string{"cut"}
		}
	case "v":
		if !shift {
			return []string{"paste"}
		}
	case "z":
		if shift {
			return []string{"redo"}
		}
		return []string{"undo"}
	case "y":
		if !shift {
			return []string{"redo"}
		}
	}
	return nil
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

// DispatchRequest handles all browser commands via CDP. It wraps
// dispatchAction with PreDelayMs / PostDelayMs honoring so every action
// can absorb the "sleep <ms> && borz <cmd>" / "borz <cmd> && sleep <ms>"
// shell pattern without a second process round-trip.
func DispatchRequest(cdp *CdpConnection, req *protocol.Request) *protocol.Response {
	if req != nil {
		if req.PreDelayMs != nil && *req.PreDelayMs > 0 {
			delaySleep(time.Duration(*req.PreDelayMs) * time.Millisecond)
		}
	}
	resp := dispatchAction(cdp, req)
	recordTraceEvent(req, resp)
	if req != nil && resp != nil && resp.Success && req.PostDelayMs != nil && *req.PostDelayMs > 0 {
		delaySleep(time.Duration(*req.PostDelayMs) * time.Millisecond)
	}
	return resp
}

// delaySleep is a package var so tests can stub the wall-clock pause without
// adding seconds to the suite. Defaults to time.Sleep.
var delaySleep = time.Sleep

func recordTraceEvent(req *protocol.Request, resp *protocol.Response) {
	if req == nil || resp == nil || !resp.Success || req.Action == protocol.ActionTrace {
		return
	}
	switch req.Action {
	case protocol.ActionOpen, protocol.ActionBack, protocol.ActionForward, protocol.ActionRefresh,
		protocol.ActionClick, protocol.ActionHover, protocol.ActionFill, protocol.ActionType_,
		protocol.ActionCheck, protocol.ActionUncheck, protocol.ActionSelect, protocol.ActionUpload,
		protocol.ActionPress, protocol.ActionScroll, protocol.ActionClose, protocol.ActionTabNew,
		protocol.ActionTabSelect, protocol.ActionTabClose, protocol.ActionFrame, protocol.ActionFrameMain,
		protocol.ActionDialog, protocol.ActionKey, protocol.ActionMouse, protocol.ActionClipboardWrite:
	default:
		return
	}

	traceMu.Lock()
	defer traceMu.Unlock()
	if !traceRecording {
		return
	}
	event := protocol.TraceEvent{
		Type: string(req.Action), Timestamp: time.Now().UnixMilli(), URL: req.URL,
		Key: req.Key, Direction: req.Direction, Pixels: req.Pixels,
	}
	if resp.Data != nil && resp.Data.URL != "" {
		event.URL = resp.Data.URL
	}
	if ref, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(req.Ref), "@")); err == nil {
		event.Ref = &ref
	}
	switch req.Action {
	case protocol.ActionFill, protocol.ActionType_, protocol.ActionClipboardWrite:
		event.Value = req.Text
	case protocol.ActionSelect:
		event.Value = req.Value
	case protocol.ActionCheck:
		checked := true
		event.Checked = &checked
	case protocol.ActionUncheck:
		checked := false
		event.Checked = &checked
	}
	traceEvents = append(traceEvents, event)
}

// dispatchAction is the real CDP dispatcher. Pre/post delay logic lives in
// the public wrapper above so each action body stays focused on the action.
func dispatchAction(cdp *CdpConnection, req *protocol.Request) *protocol.Response {
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
			currentTargetID := cdp.GetCurrentTargetID()
			tabs = append(tabs, protocol.TabInfo{
				Index:  idx,
				URL:    t.URL,
				Title:  t.Title,
				Active: t.ID == currentTargetID || (currentTargetID == "" && idx == 0),
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
		createURL := url
		if req.Viewport != nil && url != "about:blank" {
			createURL = "about:blank"
		}
		result, err := cdp.BrowserCommand("Target.createTarget", map[string]interface{}{
			"url": createURL, "background": true,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var created struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(result, &created)
		cdp.AttachAndEnable(created.TargetID)
		var viewport *protocol.ViewportInfo
		if req.Viewport != nil {
			viewport, err = applyViewport(cdp, created.TargetID, req.Viewport)
			if err != nil {
				return failResp(req.ID, err)
			}
		}
		if createURL != url {
			if _, err := cdp.PageCommand(created.TargetID, "Page.navigate", map[string]interface{}{"url": url}); err != nil {
				return failResp(req.ID, err)
			}
		}
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
			TabID: created.TargetID, URL: url, Tab: shortID, Seq: seq, Viewport: viewport,
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
				cdp.SetCurrentTargetID(existing.ID)
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
				var viewport *protocol.ViewportInfo
				if req.Viewport != nil {
					var err error
					viewport, err = applyViewport(cdp, existing.ID, req.Viewport)
					if err != nil {
						return failResp(req.ID, err)
					}
					if reused != nil {
						reused.Refs = map[string]*protocol.RefInfo{}
					}
				}
				return withWaitFor(req, cdp, existing.ID, okResp(req.ID, &protocol.ResponseData{
					TabID: existing.ID, URL: existing.URL, Title: existing.Title,
					Tab: shortID, Seq: seq, Viewport: viewport, Reused: true,
				}))
			}
		}

		createURL := req.URL
		if req.Viewport != nil {
			createURL = "about:blank"
		}
		result, err := cdp.BrowserCommand("Target.createTarget", map[string]interface{}{
			"url": createURL,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var created struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(result, &created)
		cdp.AttachAndEnable(created.TargetID)
		var viewport *protocol.ViewportInfo
		if req.Viewport != nil {
			viewport, err = applyViewport(cdp, created.TargetID, req.Viewport)
			if err != nil {
				return failResp(req.ID, err)
			}
		}
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": created.TargetID})
		if createURL != req.URL {
			if _, err := cdp.PageCommand(created.TargetID, "Page.navigate", map[string]interface{}{"url": req.URL}); err != nil {
				return failResp(req.ID, err)
			}
		}
		// Same readiness wait as ActionTabNew — see waitForTabNavigated.
		waitForTabNavigated(cdp, created.TargetID, req.URL, newTabReadyTimeout)
		cdp.SetCurrentTargetID(created.TargetID)
		newTab := cdp.TabManager.GetTab(created.TargetID)
		shortID := ""
		var seq *int
		if newTab != nil {
			shortID = newTab.ShortID
			s := newTab.RecordAction()
			seq = &s
		}
		return withWaitFor(req, cdp, created.TargetID, okResp(req.ID, &protocol.ResponseData{
			TabID: created.TargetID, URL: req.URL, Tab: shortID, Seq: seq, Viewport: viewport,
		}))
	}

	if req.Action == protocol.ActionEval && req.Script != "" && req.SiteDomain != "" && req.TabID == nil {
		if siteTarget, err := ensureSiteAdapterTarget(cdp, req); err != nil {
			return failResp(req.ID, err)
		} else if siteTarget != nil {
			tabRef = siteTarget.ID
		}
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
	// Any dispatched action against this tab — read or write — counts as
	// activity for idle-tab reaping. Mutating handlers also call RecordAction
	// below; the extra timestamp write is harmless.
	tab.TouchActivity()

	// Per-request foreground request. Honored for any action that fell through
	// to EnsurePageTarget — handy for fetch/eval against pages that throttle
	// backgrounded tabs, or for clipboard/paste shortcuts that need real focus.
	// Explicit Activate also updates CurrentTargetID since the caller asked to
	// switch focus to this tab.
	if req.Activate {
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": target.ID})
		cdp.SessionCommand(target.ID, "Page.bringToFront", nil)
		cdp.SetCurrentTargetID(target.ID)
	}

	switch req.Action {
	// --- Navigation ---
	case protocol.ActionOpen:
		// tabRef == "" is handled above (hoisted so it works with no existing pages).
		if req.URL == "" {
			return failResp(req.ID, "missing url parameter")
		}
		seq := tab.RecordAction()
		var viewport *protocol.ViewportInfo
		if req.Viewport != nil {
			viewport, err = applyViewport(cdp, target.ID, req.Viewport)
			if err != nil {
				return failResp(req.ID, err)
			}
		}
		cdp.PageCommand(target.ID, "Page.navigate", map[string]interface{}{"url": req.URL})
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": target.ID})
		// open always activates — pin CurrentTargetID since EnsurePageTarget
		// no longer mutates it for explicit-tab requests.
		cdp.SetCurrentTargetID(target.ID)
		tab.Refs = map[string]*protocol.RefInfo{}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{
			URL: req.URL, Title: target.Title, TabID: target.ID, Tab: shortID, Seq: intPtr(seq), Viewport: viewport,
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
		snapshotData, diffData, err := buildSnapshot(cdp, target.ID, target.URL, tab, req)
		if err != nil {
			return failResp(req.ID, err)
		}
		return okResp(req.ID, &protocol.ResponseData{
			Title: target.Title, URL: target.URL,
			SnapshotData:     snapshotData,
			SnapshotDiffData: diffData,
			Tab:              shortID,
		})

	case protocol.ActionScreenshot:
		result, err := captureScreenshot(cdp, target.ID, tab, req.Annotations)
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

	case protocol.ActionViewport:
		var seq *int
		if req.Viewport != nil {
			s := tab.RecordAction()
			seq = &s
			tab.Refs = map[string]*protocol.RefInfo{}
		}
		viewport, err := applyViewport(cdp, target.ID, req.Viewport)
		if err != nil {
			return failResp(req.ID, err)
		}
		return okResp(req.ID, &protocol.ResponseData{Viewport: viewport, Tab: shortID, Seq: seq})

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
		if req.Action == protocol.ActionClick {
			if err := mouseClick(cdp, target.ID, x, y); err != nil {
				return failResp(req.ID, err)
			}
		} else if _, err := cdp.SessionCommand(target.ID, "Input.dispatchMouseEvent", map[string]interface{}{
			"type": "mouseMoved", "x": x, "y": y, "button": "none",
		}); err != nil {
			return failResp(req.ID, fmt.Errorf("move pointer for hover: %w", err))
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
		callRaw, err := cdp.SessionCommand(target.ID, "Runtime.callFunctionOn", map[string]interface{}{
			"objectId": resolved.Object.ObjectID,
			"functionDeclaration": `function(value) {
				if (!(this instanceof HTMLSelectElement)) return { ok: false, error: 'element is not a select' };
				if (!Array.from(this.options).some((option) => option.value === value)) {
					return { ok: false, error: 'select value not found: ' + value };
				}
				this.value = value;
				this.dispatchEvent(new Event('input', { bubbles: true }));
				this.dispatchEvent(new Event('change', { bubbles: true }));
				return { ok: true };
			}`,
			"arguments":     []map[string]interface{}{{"value": req.Value}},
			"returnByValue": true,
		})
		if err != nil {
			return failResp(req.ID, err)
		}
		var call struct {
			Result struct {
				Value struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				} `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(callRaw, &call); err != nil {
			return failResp(req.ID, fmt.Errorf("decode select result: %w", err))
		}
		if !call.Result.Value.OK {
			if call.Result.Value.Error == "" {
				call.Result.Value.Error = "select action failed"
			}
			return failResp(req.ID, call.Result.Value.Error)
		}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Value: req.Value, Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionUpload:
		if req.Ref == "" {
			return failResp(req.ID, "missing ref parameter")
		}
		if len(req.Files) == 0 {
			return failResp(req.ID, "missing files parameter")
		}
		absFiles, err := resolveLocalFiles(req.Files)
		if err != nil {
			return failResp(req.ID, err)
		}
		seq := tab.RecordAction()
		backendID, err := parseRef(cdp, target.ID, tab, req.Ref)
		if err != nil {
			return failResp(req.ID, err)
		}
		backendID, err = resolveFileInputBackendID(cdp, target.ID, backendID)
		if err != nil {
			return failResp(req.ID, err)
		}
		if _, err := cdp.SessionCommand(target.ID, "DOM.setFileInputFiles", map[string]interface{}{
			"backendNodeId": backendID,
			"files":         absFiles,
		}); err != nil {
			return failResp(req.ID, err)
		}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

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
		commands := req.Commands
		if len(commands) == 0 {
			commands = editingCommandsFor(req.Key, req.Modifiers)
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
			if eventType == "keyDown" && len(commands) > 0 {
				params["commands"] = commands
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

	case protocol.ActionClipboardWrite:
		if req.Text == "" {
			return failResp(req.ID, "missing text parameter")
		}
		// Best-effort permission grant; ignore errors (already granted or unsupported).
		cdp.BrowserCommand("Browser.grantPermissions", map[string]interface{}{
			"permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"},
		})
		// navigator.clipboard.writeText requires the page to be focused.
		cdp.SessionCommand(target.ID, "Page.bringToFront", nil)
		seq := tab.RecordAction()
		// JSON-encode the text so arbitrary content (quotes, newlines, base64)
		// survives embedding in the expression.
		textJSON, _ := json.Marshal(req.Text)
		writeScript := fmt.Sprintf(
			`navigator.clipboard.writeText(%s).then(() => "ok").catch(e => { throw new Error((e && e.message) || String(e)); })`,
			string(textJSON))
		if _, err := cdp.Evaluate(target.ID, writeScript, true); err != nil {
			return failResp(req.ID, err)
		}
		pasted := false
		if req.Paste {
			// Best-effort: focus the xterm textarea (incl. same-origin iframes)
			// so the native paste lands in the terminal, then fire Ctrl+Shift+V.
			cdp.Evaluate(target.ID, termFocusScript, true)
			if err := dispatchPasteShortcut(cdp, target.ID); err != nil {
				return failResp(req.ID, err)
			}
			pasted = true
		}
		return okResp(req.ID, &protocol.ResponseData{
			Value:  req.Text,
			Result: map[string]interface{}{"written": true, "pasted": pasted},
			Tab:    shortID,
			Seq:    intPtr(seq),
		})

	case protocol.ActionPress:
		if req.Key == "" {
			return failResp(req.ID, "missing key parameter")
		}
		seq := tab.RecordAction()
		def := resolveKey(req.Key)
		mods := modifierMask(req.Modifiers)
		commands := req.Commands
		if len(commands) == 0 {
			commands = editingCommandsFor(req.Key, req.Modifiers)
		}
		send := func(eventType string, withText bool) error {
			params := map[string]interface{}{"type": eventType, "key": def.Key, "modifiers": mods}
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
			if eventType == "keyDown" && len(commands) > 0 {
				params["commands"] = commands
			}
			_, err := cdp.SessionCommand(target.ID, "Input.dispatchKeyEvent", params)
			return err
		}
		if err := send("keyDown", true); err != nil {
			return failResp(req.ID, err)
		}
		if err := send("keyUp", false); err != nil {
			return failResp(req.ID, err)
		}
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
		expr := fmt.Sprintf("window.scrollBy({left: %d, top: %d, behavior: 'instant'})", deltaX, deltaY)
		if _, err := cdp.Evaluate(target.ID, expr, true); err != nil {
			return failResp(req.ID, err)
		}
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{Tab: shortID, Seq: intPtr(seq)}))

	case protocol.ActionWait:
		ms := 1000
		if req.Ms != nil {
			ms = *req.Ms
		}
		if ms < 0 {
			return failResp(req.ID, "wait ms must be a non-negative integer")
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return okResp(req.ID, &protocol.ResponseData{Tab: shortID})

	case protocol.ActionEval:
		if req.Script == "" {
			return failResp(req.ID, "missing script parameter")
		}
		if req.SiteDomain != "" && !req.Force && !siteDomainMatchesURL(req.SiteDomain, target.URL) {
			return failResp(req.ID, fmt.Sprintf("site adapter domain guard blocked execution: active tab URL %q does not match %q (pass --force/force=true to override)", target.URL, req.SiteDomain))
		}
		seq := tab.RecordAction()
		timeout := 30 * time.Second
		if req.EvalTimeoutMs != nil && *req.EvalTimeoutMs > 0 {
			timeout = time.Duration(*req.EvalTimeoutMs) * time.Millisecond
		}
		raw, err := cdp.EvaluateWithTimeout(target.ID, req.Script, true, timeout)
		if err != nil {
			return failResp(req.ID, err)
		}
		var result interface{}
		json.Unmarshal(raw, &result)
		return withWaitFor(req, cdp, target.ID, okResp(req.ID, &protocol.ResponseData{
			Result: result, Tab: shortID, Seq: intPtr(seq),
		}))

	case protocol.ActionTermText:
		seq := tab.RecordAction()
		raw, err := cdp.Evaluate(target.ID, termTextScript, true)
		if err != nil {
			return failResp(req.ID, err)
		}
		// Flat text goes in Value for OCR-free reading; the full structured
		// metadata ({found, source, lines, cols, rows, note}) goes in Result.
		// A terminal that isn't found is still a successful response — the note
		// explains why — so callers get a clear signal instead of an error.
		var meta struct {
			Text string `json:"text"`
		}
		json.Unmarshal(raw, &meta)
		var result interface{}
		json.Unmarshal(raw, &result)
		return okResp(req.ID, &protocol.ResponseData{
			Value: meta.Text, Result: result, Tab: shortID, Seq: intPtr(seq),
		})

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
			currentTargetID := cdp.GetCurrentTargetID()
			tabs = append(tabs, protocol.TabInfo{
				Index:  idx,
				URL:    t.URL,
				Title:  t.Title,
				Active: t.ID == currentTargetID || (currentTargetID == "" && idx == 0),
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
		selected := resolvePageTarget(cdp, pages, req.TabID, req.Index)
		if selected == nil {
			return failResp(req.ID, "tab not found")
		}
		cdp.SetCurrentTargetID(selected.ID)
		cdp.AttachAndEnable(selected.ID)
		// tab_select is a focus switch — bring the tab to the foreground in
		// Chrome's UI, not just route the daemon's command stream.
		cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": selected.ID})
		cdp.SessionCommand(selected.ID, "Page.bringToFront", nil)
		selTab := cdp.TabManager.GetTab(selected.ID)
		tabShort := ""
		if selTab != nil {
			tabShort = selTab.ShortID
			selTab.TouchActivity()
		}
		return okResp(req.ID, &protocol.ResponseData{
			TabID: selected.ID, URL: selected.URL, Title: selected.Title, Tab: tabShort,
		})

	case protocol.ActionTabFront:
		// tab_select activates the tab inside Chrome, but a minimized or
		// occluded Chrome window still reports visibilityState "hidden" and
		// pages throttle accordingly (uploads, timers, media). tab_front
		// additionally restores the OS window so the page is really visible.
		seq := tab.RecordAction()
		result := bringTabToFront(cdp, target.ID)
		return okResp(req.ID, &protocol.ResponseData{
			Result: result, TabID: target.ID, URL: target.URL, Title: target.Title,
			Tab: shortID, Seq: intPtr(seq),
		})

	case protocol.ActionPageVisibility:
		state := strings.ToLower(strings.TrimSpace(req.Visibility))
		switch state {
		case "":
			// Status: report the page's current belief and any active override.
			raw, err := cdp.Evaluate(target.ID, "document.visibilityState", true)
			if err != nil {
				return failResp(req.ID, err)
			}
			var current string
			json.Unmarshal(raw, &current)
			override, _ := tab.GetVisibilityOverride()
			return okResp(req.ID, &protocol.ResponseData{
				Result: map[string]interface{}{
					"visibilityState": current,
					"override":        override,
					"overridden":      override != "",
				},
				Tab: shortID,
			})
		case "visible", "hidden":
			seq := tab.RecordAction()
			result, err := applyVisibilityOverride(cdp, target.ID, tab, state)
			if err != nil {
				return failResp(req.ID, err)
			}
			return okResp(req.ID, &protocol.ResponseData{Result: result, Tab: shortID, Seq: intPtr(seq)})
		case "reset":
			seq := tab.RecordAction()
			result, err := resetVisibilityOverride(cdp, target.ID, tab)
			if err != nil {
				return failResp(req.ID, err)
			}
			return okResp(req.ID, &protocol.ResponseData{Result: result, Tab: shortID, Seq: intPtr(seq)})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown visibility state: %s (expected visible, hidden, or reset)", req.Visibility))
		}

	case protocol.ActionWebAuthn:
		result, err := dispatchWebAuthn(cdp, target.ID, req)
		if err != nil {
			return failResp(req.ID, err)
		}
		seq := tab.RecordAction()
		return okResp(req.ID, &protocol.ResponseData{
			Result: result,
			TabID:  target.ID,
			Tab:    shortID,
			Seq:    intPtr(seq),
		})

	case protocol.ActionFileChooser:
		subCmd := req.FileChooserCommand
		if subCmd == "" {
			subCmd = "status"
		}
		switch subCmd {
		case "accept":
			if len(req.Files) == 0 {
				return failResp(req.ID, "missing files parameter")
			}
			absFiles, err := resolveLocalFiles(req.Files)
			if err != nil {
				return failResp(req.ID, err)
			}
			seq := tab.RecordAction()
			if _, err := cdp.SessionCommand(target.ID, "Page.setInterceptFileChooserDialog", map[string]interface{}{"enabled": true}); err != nil {
				return failResp(req.ID, err)
			}
			tab.SetFileChooserHandler(&FileChooserHandler{Accept: true, Files: absFiles})
			return okResp(req.ID, &protocol.ResponseData{
				Result: map[string]interface{}{"armed": true, "action": "accept", "files": absFiles},
				Tab:    shortID, Seq: intPtr(seq),
			})
		case "cancel":
			seq := tab.RecordAction()
			if _, err := cdp.SessionCommand(target.ID, "Page.setInterceptFileChooserDialog", map[string]interface{}{"enabled": true}); err != nil {
				return failResp(req.ID, err)
			}
			tab.SetFileChooserHandler(&FileChooserHandler{Accept: false})
			return okResp(req.ID, &protocol.ResponseData{
				Result: map[string]interface{}{"armed": true, "action": "cancel"},
				Tab:    shortID, Seq: intPtr(seq),
			})
		case "disarm":
			seq := tab.RecordAction()
			tab.ConsumeFileChooserHandler()
			cdp.SessionCommand(target.ID, "Page.setInterceptFileChooserDialog", map[string]interface{}{"enabled": false})
			return okResp(req.ID, &protocol.ResponseData{
				Result: map[string]interface{}{"armed": false},
				Tab:    shortID, Seq: intPtr(seq),
			})
		case "status":
			handler := tab.PeekFileChooserHandler()
			result := map[string]interface{}{"armed": handler != nil}
			if handler != nil {
				if handler.Accept {
					result["action"] = "accept"
					result["files"] = handler.Files
				} else {
					result["action"] = "cancel"
				}
			}
			return okResp(req.ID, &protocol.ResponseData{Result: result, Tab: shortID})
		default:
			return failResp(req.ID, fmt.Sprintf("unknown filechooser subcommand: %s", subCmd))
		}

	case protocol.ActionTabClose:
		targets, _ := cdp.GetTargets()
		var pages []CdpTargetInfo
		for _, t := range targets {
			if t.Type == "page" {
				pages = append(pages, t)
			}
		}
		selected := resolvePageTarget(cdp, pages, req.TabID, req.Index)
		if selected == nil {
			return failResp(req.ID, "tab not found")
		}
		closedTab := cdp.TabManager.GetTab(selected.ID)
		closedShort := ""
		if closedTab != nil {
			closedShort = closedTab.ShortID
		}
		cdp.BrowserCommand("Target.closeTarget", map[string]interface{}{"targetId": selected.ID})
		cdp.ClearCurrentTargetIDIf(selected.ID)
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

		nodeRaw, err := cdp.PageCommand(target.ID, "DOM.querySelector", map[string]interface{}{
			"nodeId": doc.Root.NodeID, "selector": req.Selector,
		})
		if err != nil {
			return failResp(req.ID, fmt.Errorf("invalid selector %q: %w", req.Selector, err))
		}
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
		tab.SetActiveFrame(desc.Node.FrameID)

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
		tab.SetActiveFrame("")
		return okResp(req.ID, &protocol.ResponseData{
			FrameInfo: map[string]interface{}{"frameId": 0},
			Tab:       shortID, Seq: intPtr(seq),
		})

	// --- Dialog ---
	case protocol.ActionDialog:
		subCmd := req.DialogResponse
		if subCmd == "" {
			subCmd = "accept"
		}
		switch subCmd {
		case "accept", "dismiss":
			accept := subCmd == "accept"
			// If a dialog is already open, answer it now. Arming would leave
			// the open one blocking the renderer forever.
			if pending := tab.PeekPendingDialog(); pending != nil && !pending.AutoHandled {
				seq := tab.RecordAction()
				args := map[string]interface{}{"accept": accept}
				if req.PromptText != "" {
					args["promptText"] = req.PromptText
				}
				if _, err := cdp.SessionCommand(target.ID, "Page.handleJavaScriptDialog", args); err != nil {
					return failResp(req.ID, err)
				}
				tab.MarkPendingDialogHandled(accept, req.PromptText)
				return okResp(req.ID, &protocol.ResponseData{
					DialogInfo: map[string]interface{}{
						"type":    "handled",
						"message": fmt.Sprintf("Open %s dialog %sed: %s", pending.Type, subCmd, pending.Message),
						"handled": true,
						"armed":   false,
						"action":  subCmd,
						"dialog":  pending,
					},
					Tab: shortID, Seq: intPtr(seq),
				})
			}
			seq := tab.RecordAction()
			tab.SetDialogHandler(&DialogHandler{Accept: accept, PromptText: req.PromptText})
			cdp.SessionCommand(target.ID, "Page.enable", nil)
			return okResp(req.ID, &protocol.ResponseData{
				DialogInfo: map[string]interface{}{
					"type": "armed", "message": fmt.Sprintf("Dialog handler armed: %s", subCmd),
					"handled": false, "armed": true, "action": subCmd,
				},
				Tab: shortID, Seq: intPtr(seq),
			})

		case "disarm":
			seq := tab.RecordAction()
			tab.ConsumeDialogHandler()
			return okResp(req.ID, &protocol.ResponseData{
				DialogInfo: map[string]interface{}{
					"type": "disarmed", "message": "Dialog handler disarmed", "handled": false, "armed": false,
				},
				Tab: shortID, Seq: intPtr(seq),
			})

		case "status":
			info := map[string]interface{}{"type": "status", "armed": false, "handled": false}
			if handler := tab.PeekDialogHandler(); handler != nil {
				info["armed"] = true
				info["action"] = dialogHandledAs(handler.Accept)
				if handler.PromptText != "" {
					info["promptText"] = handler.PromptText
				}
			}
			if pending := tab.PeekPendingDialog(); pending != nil {
				info["pending"] = pending
				info["blocked"] = !pending.AutoHandled
				info["message"] = fmt.Sprintf("Open %s dialog: %s", pending.Type, pending.Message)
			} else {
				info["blocked"] = false
				info["message"] = "No dialog open"
			}
			if history := tab.DialogHistory(dialogHistoryLimit); len(history) > 0 {
				info["history"] = history
			}
			return okResp(req.ID, &protocol.ResponseData{DialogInfo: info, Tab: shortID})

		default:
			return failResp(req.ID, fmt.Sprintf(
				"unknown dialog subcommand: %s (want accept, dismiss, disarm, or status)", subCmd))
		}

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
			if traceRecording {
				return failResp(req.ID, "trace is already recording")
			}
			traceRecording = true
			traceEvents = nil
			return okResp(req.ID, &protocol.ResponseData{
				TraceStatus: &protocol.TraceStatus{Recording: true, EventCount: 0}, Tab: shortID,
			})
		case "stop":
			if !traceRecording {
				return failResp(req.ID, "trace is not recording")
			}
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
