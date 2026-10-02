package daemon

import (
	"encoding/json"
	"strings"
)

// Profile title label.
//
// A managed Chrome launched for a named profile shows "[<profile>] " in
// front of every top-level page title, so the tab strip, the Window menu, and
// Mission Control tell several borz Chromes apart. Chrome itself has no
// visible per-window label on macOS (--window-name only reaches the OS
// window list), so the page title is the only surface the user actually
// sees.
//
// The prefix is a display aid only: every title borz reports (tab list, open,
// snapshot, get title, ...) has it stripped again, so callers such as
// teamsact keep seeing the page's own title. Page JavaScript that reads
// document.title does see the prefix.

// TitleLabelPrefix returns the title prefix for a profile name.
func TitleLabelPrefix(profileName string) string {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return ""
	}
	return "[" + profileName + "] "
}

// titleLabelScript keeps the prefix on the top-level document's title. It is
// idempotent across daemon restarts: a newer install replaces the prefix and
// reuses the existing observer instead of stacking another one. Observing
// only <head> children and the <title> element keeps it cheap on busy SPAs.
const titleLabelScript = `(() => {
	if (window.top !== window) return;
	const prefix = %s;
	const state = window.__borzTitleLabel || (window.__borzTitleLabel = {});
	const previous = state.prefix;
	state.prefix = prefix;
	const strip = (t) => {
		for (const p of [previous, prefix]) {
			if (p && t.startsWith(p)) return t.slice(p.length);
		}
		return t;
	};
	const apply = () => {
		const current = document.title || '';
		const p = state.prefix;
		if (!p || current.startsWith(p)) return;
		const own = strip(current) || location.hostname || location.href;
		document.title = p + own;
	};
	state.apply = apply;
	if (previous && previous !== prefix && document.title) {
		document.title = strip(document.title);
	}
	if (state.started) { apply(); return; }
	state.started = true;
	let titleEl = null;
	const titleObserver = new MutationObserver(() => state.apply());
	const watchTitle = () => {
		const el = document.querySelector('head > title') || document.querySelector('title');
		if (el && el !== titleEl) {
			titleObserver.disconnect();
			titleEl = el;
			titleObserver.observe(el, { childList: true, characterData: true, subtree: true });
		}
		state.apply();
	};
	const start = () => {
		watchTitle();
		const head = document.head || document.documentElement;
		if (head) new MutationObserver(watchTitle).observe(head, { childList: true });
	};
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', start, { once: true });
	} else {
		start();
	}
})()`

// SetTitleLabel sets the prefix installed on every page this connection
// attaches to. Empty disables labeling.
func (c *CdpConnection) SetTitleLabel(prefix string) {
	c.titleLabel = prefix
}

// TitleLabel returns the configured prefix ("" when labeling is off).
func (c *CdpConnection) TitleLabel() string {
	if c == nil {
		return ""
	}
	return c.titleLabel
}

// StripTitleLabel removes this connection's prefix from a page title.
func (c *CdpConnection) StripTitleLabel(title string) string {
	return stripTitleLabel(c.TitleLabel(), title)
}

func stripTitleLabel(prefix, title string) string {
	if prefix == "" {
		return title
	}
	return strings.TrimPrefix(title, prefix)
}

// installTitleLabel applies the label to the live document and to every
// future document in the target. Best-effort: a page that cannot run scripts
// (chrome://, PDF viewer) simply stays unlabeled.
func (c *CdpConnection) installTitleLabel(targetID string) {
	prefix := c.TitleLabel()
	if prefix == "" {
		return
	}
	encoded, err := json.Marshal(prefix)
	if err != nil {
		return
	}
	script := strings.Replace(titleLabelScript, "%s", string(encoded), 1)
	c.SessionCommand(targetID, "Page.addScriptToEvaluateOnNewDocument", map[string]interface{}{"source": script})
	c.SessionCommand(targetID, "Runtime.evaluate", map[string]interface{}{"expression": script, "returnByValue": true})
}
