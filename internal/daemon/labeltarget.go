package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Label targeting (#171).
//
// Some controls never get a snapshot ref: a framework error screen may render
// its actions as plain elements, or a layer the hit test considers on top may
// sit over them, so `click <ref>` has nothing to aim at. `click --label <text>`
// finds the visible element whose own label (aria-label, rendered text, button
// value, or title) is exactly <text> — whitespace-collapsed, case-sensitive —
// and climbs to its nearest actionable ancestor. It refuses unless exactly one
// control matches, so "Retry" never lands on "Clear cache and retry".

const labelTargetScript = `(() => {
	const want = %s;
	const norm = (s) => String(s == null ? '' : s).replace(/\s+/g, ' ').trim();
	const parentOf = (el) => el.parentElement || (el.getRootNode && el.getRootNode() instanceof ShadowRoot ? el.getRootNode().host : null);
	const visible = (el) => {
		const r = el.getBoundingClientRect();
		if (r.width <= 0 || r.height <= 0) return false;
		const s = getComputedStyle(el);
		return s.visibility !== 'hidden' && s.display !== 'none';
	};
	const labels = (el) => {
		const out = [el.getAttribute('aria-label'), el.getAttribute('title')];
		if (el instanceof HTMLInputElement && /^(button|submit|reset)$/i.test(el.type)) out.push(el.value);
		out.push(el.innerText);
		return out.map(norm);
	};
	const ACTION_ROLES = new Set(['button', 'link', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'option', 'checkbox', 'radio', 'switch', 'treeitem']);
	const actionable = (el) => {
		const tag = el.tagName.toLowerCase();
		if (tag === 'button' || tag === 'summary' || tag === 'select' || tag === 'textarea') return true;
		if (tag === 'a' && el.hasAttribute('href')) return true;
		if (tag === 'input' && el.type !== 'hidden') return true;
		if (ACTION_ROLES.has((el.getAttribute('role') || '').toLowerCase())) return true;
		if (el.hasAttribute('onclick') || typeof el.onclick === 'function') return true;
		const tabindex = el.getAttribute('tabindex');
		if (tabindex !== null && Number(tabindex) >= 0) return true;
		return getComputedStyle(el).cursor === 'pointer';
	};
	const resolve = (el) => {
		let cur = el;
		for (let depth = 0; cur && depth < 8; depth++, cur = parentOf(cur)) {
			if (cur === document.body || cur === document.documentElement) break;
			if (actionable(cur)) return cur;
		}
		return el;
	};
	const all = [];
	const collect = (root) => {
		for (const el of root.querySelectorAll('*')) {
			all.push(el);
			if (el.shadowRoot) collect(el.shadowRoot);
		}
	};
	collect(document);
	const targets = new Set();
	for (const el of all) {
		if (el === document.body || el === document.documentElement) continue;
		if (!labels(el).includes(want) || !visible(el)) continue;
		targets.add(resolve(el));
	}
	// A matching wrapper and its matching child resolve to different
	// elements when only the child is actionable; keep the innermost targets.
	const list = [...targets].filter((t) => ![...targets].some((o) => o !== t && t.contains(o)));
	if (list.length === 1) return list[0];
	const describe = (el) => {
		const role = el.getAttribute('role');
		return '<' + el.tagName.toLowerCase() + (role ? ' role=' + role : '') + '> ' + JSON.stringify(norm(el.innerText).slice(0, 60));
	};
	return JSON.stringify({ count: list.length, matches: list.slice(0, 5).map(describe) });
})()`

// resolveBackendNodeIDByLabel returns the single actionable element labeled
// exactly label in the tab's active frame.
func resolveBackendNodeIDByLabel(cdp *CdpConnection, targetID, label string) (int, error) {
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		return 0, fmt.Errorf("--label must not be empty")
	}
	encoded, _ := json.Marshal(label)
	sessionTargetID, object, err := cdp.EvaluateObject(targetID, fmt.Sprintf(labelTargetScript, string(encoded)))
	if err != nil {
		return 0, fmt.Errorf("find element labeled %q: %w", label, err)
	}
	if object.ObjectID == "" {
		var summaryJSON string
		json.Unmarshal(object.Value, &summaryJSON)
		var summary struct {
			Count   int      `json:"count"`
			Matches []string `json:"matches"`
		}
		json.Unmarshal([]byte(summaryJSON), &summary)
		return 0, labelTargetError(label, summary.Count, summary.Matches)
	}
	defer cdp.SessionCommand(sessionTargetID, "Runtime.releaseObject", map[string]interface{}{"objectId": object.ObjectID})
	descRaw, err := cdp.SessionCommand(sessionTargetID, "DOM.describeNode", map[string]interface{}{"objectId": object.ObjectID})
	if err != nil {
		return 0, err
	}
	var desc struct {
		Node struct {
			BackendNodeID int `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := json.Unmarshal(descRaw, &desc); err != nil || desc.Node.BackendNodeID <= 0 {
		return 0, fmt.Errorf("element labeled %q has no backend node id", label)
	}
	return desc.Node.BackendNodeID, nil
}

func labelTargetError(label string, count int, matches []string) error {
	if count == 0 {
		return fmt.Errorf("no visible element is labeled exactly %q (aria-label, text, button value, or title; whitespace-collapsed, case-sensitive). Check the exact text with 'borz snapshot' or 'borz get text'", label)
	}
	return fmt.Errorf("%d visible controls are labeled exactly %q: %s. Refusing to guess; narrow it with --tab/frame or use a snapshot ref", count, label, strings.Join(matches, ", "))
}
