package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Page-visibility override.
//
// CDP has no Emulation.setPageVisibilityOverride — the only way to make a
// backgrounded/occluded page believe it is visible is to override the
// document getters in JS and fire a synthetic visibilitychange event. That
// convinces apps that gate work on document.visibilityState (uploads, timers,
// polling). It cannot un-throttle requestAnimationFrame or compositor-driven
// rendering — for those the window must really be brought forward (tab_front).

// visibilityApplyScript overrides the visibility getters on the live document
// and fires visibilitychange so listeners re-evaluate. %q is "visible"/"hidden".
const visibilityApplyScript = `(() => {
	const state = %q;
	try {
		Object.defineProperty(document, 'visibilityState', { get: () => state, configurable: true });
		Object.defineProperty(document, 'hidden', { get: () => state === 'hidden', configurable: true });
		document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
		return document.visibilityState;
	} catch (e) {
		return 'error: ' + ((e && e.message) || String(e));
	}
})()`

// visibilityPersistScript is installed via Page.addScriptToEvaluateOnNewDocument
// so the override survives navigations. No visibilitychange dispatch — at
// document-start there are no listeners yet.
const visibilityPersistScript = `(() => {
	const state = %q;
	try {
		Object.defineProperty(document, 'visibilityState', { get: () => state, configurable: true });
		Object.defineProperty(document, 'hidden', { get: () => state === 'hidden', configurable: true });
	} catch (e) {}
})()`

// visibilityResetScript deletes the own-property overrides (restoring the
// prototype getters) and fires visibilitychange with the native state.
const visibilityResetScript = `(() => {
	try {
		delete document.visibilityState;
		delete document.hidden;
		document.dispatchEvent(new Event('visibilitychange', { bubbles: true }));
		return document.visibilityState;
	} catch (e) {
		return 'error: ' + ((e && e.message) || String(e));
	}
})()`

// applyVisibilityOverride makes the page report the given visibility state
// ("visible" or "hidden") and keeps the override alive across navigations.
func applyVisibilityOverride(cdp *CdpConnection, targetID string, tab *TabState, state string) (map[string]interface{}, error) {
	// Drop any previous new-document script so overrides don't stack.
	if _, prevScript := tab.GetVisibilityOverride(); prevScript != "" {
		cdp.SessionCommand(targetID, "Page.removeScriptToEvaluateOnNewDocument", map[string]interface{}{"identifier": prevScript})
	}

	raw, err := cdp.Evaluate(targetID, fmt.Sprintf(visibilityApplyScript, state), true)
	if err != nil {
		return nil, err
	}
	var applied string
	json.Unmarshal(raw, &applied)
	if strings.HasPrefix(applied, "error:") {
		return nil, fmt.Errorf("visibility override failed: %s", strings.TrimSpace(strings.TrimPrefix(applied, "error:")))
	}

	scriptID := ""
	if rawAdd, err := cdp.SessionCommand(targetID, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{
		"source": fmt.Sprintf(visibilityPersistScript, state),
	}); err == nil {
		var added struct {
			Identifier string `json:"identifier"`
		}
		json.Unmarshal(rawAdd, &added)
		scriptID = added.Identifier
	}

	// Best-effort reinforcement for pages that also check document.hasFocus()
	// or freeze when backgrounded. Both commands are experimental; errors are
	// ignored on Chrome versions that lack them.
	cdp.SessionCommand(targetID, "Emulation.setFocusEmulationEnabled", map[string]interface{}{"enabled": state == "visible"})
	if state == "visible" {
		cdp.SessionCommand(targetID, "Page.setWebLifecycleState", map[string]interface{}{"state": "active"})
	}

	tab.SetVisibilityOverride(state, scriptID)
	return map[string]interface{}{
		"visibilityState": applied,
		"override":        state,
		"persisted":       scriptID != "",
	}, nil
}

// resetVisibilityOverride removes the override and returns to native visibility.
func resetVisibilityOverride(cdp *CdpConnection, targetID string, tab *TabState) (map[string]interface{}, error) {
	if _, scriptID := tab.GetVisibilityOverride(); scriptID != "" {
		cdp.SessionCommand(targetID, "Page.removeScriptToEvaluateOnNewDocument", map[string]interface{}{"identifier": scriptID})
	}
	raw, err := cdp.Evaluate(targetID, visibilityResetScript, true)
	if err != nil {
		return nil, err
	}
	var current string
	json.Unmarshal(raw, &current)
	cdp.SessionCommand(targetID, "Emulation.setFocusEmulationEnabled", map[string]interface{}{"enabled": false})
	tab.SetVisibilityOverride("", "")
	return map[string]interface{}{
		"visibilityState": current,
		"override":        "",
	}, nil
}

// bringTabToFront makes the tab really visible at the OS level: restores the
// Chrome window if minimized, activates the tab, and focuses the page. All
// steps are best-effort; the returned map reports what happened plus the
// page's resulting document.visibilityState so callers can verify.
func bringTabToFront(cdp *CdpConnection, targetID string) map[string]interface{} {
	windowState := ""
	restored := false
	if raw, err := cdp.BrowserCommand("Browser.getWindowForTarget", map[string]interface{}{"targetId": targetID}); err == nil {
		var win struct {
			WindowID int `json:"windowId"`
			Bounds   struct {
				WindowState string `json:"windowState"`
			} `json:"bounds"`
		}
		if json.Unmarshal(raw, &win) == nil {
			windowState = win.Bounds.WindowState
			if win.Bounds.WindowState == "minimized" {
				if _, err := cdp.BrowserCommand("Browser.setWindowBounds", map[string]interface{}{
					"windowId": win.WindowID,
					"bounds":   map[string]interface{}{"windowState": "normal"},
				}); err == nil {
					restored = true
					windowState = "normal"
				}
			}
		}
	}

	cdp.BrowserCommand("Target.activateTarget", map[string]interface{}{"targetId": targetID})
	cdp.SessionCommand(targetID, "Page.bringToFront", nil)
	cdp.SetCurrentTargetID(targetID)

	visibility := ""
	if raw, err := cdp.Evaluate(targetID, "document.visibilityState", true); err == nil {
		json.Unmarshal(raw, &visibility)
	}

	result := map[string]interface{}{
		"activated":       true,
		"visibilityState": visibility,
	}
	if windowState != "" {
		result["windowState"] = windowState
	}
	if restored {
		result["restoredFromMinimized"] = true
	}
	return result
}
