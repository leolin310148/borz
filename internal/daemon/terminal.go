package daemon

// Terminal helpers for driving and reading xterm.js terminals (e.g. JumpServer
// Luna SSH sessions) that render onto a canvas — where per-character key
// streaming is slow and screenshots can only be read via OCR.
//
// These scripts are evaluated with Runtime.evaluate (returnByValue) and walk
// the page plus every *same-origin* iframe via contentDocument, mirroring the
// way the geometry/eval helpers reach into nested documents.

// termTextScript returns a JSON-serializable object describing the terminal's
// text contents:
//
//	{ found, source, text, lines, cols, rows, note? }
//
// It first looks for a live xterm.js Terminal instance and reads
// buffer.active line-by-line (including scrollback). If no instance is
// reachable it falls back to the accessibility layer, then the DOM-renderer
// rows, and finally reports found=false with a note explaining the limit.
const termTextScript = `(() => {
  const isTerm = (o) => {
    try {
      return !!(o && o.buffer && o.buffer.active &&
        typeof o.buffer.active.getLine === 'function' &&
        typeof o.buffer.active.length === 'number');
    } catch (e) { return false; }
  };

  // This document plus every same-origin iframe document, depth-first.
  const collectDocs = (rootDoc, rootWin) => {
    const out = [];
    const visit = (doc, win) => {
      if (!doc) return;
      out.push([doc, win]);
      let frames;
      try { frames = doc.querySelectorAll('iframe, frame'); } catch (e) { return; }
      for (const f of frames) {
        let cdoc = null, cwin = null;
        try { cdoc = f.contentDocument; cwin = f.contentWindow; } catch (e) { cdoc = null; }
        if (cdoc) visit(cdoc, cwin);
      }
    };
    visit(rootDoc, rootWin);
    return out;
  };

  const findTerminal = (docs) => {
    for (const [doc, win] of docs) {
      // Well-known globals some integrations expose on the frame window.
      for (const g of ['term', 'terminal', 'xterm', '_xterm', '_term']) {
        try { if (win && isTerm(win[g])) return win[g]; } catch (e) {}
      }
      // Property stashed on or near an .xterm element (common wrapper pattern:
      // the Terminal instance is kept as a field on the mount element or one of
      // its ancestors).
      let els;
      try { els = doc.querySelectorAll('.xterm, .terminal'); } catch (e) { els = []; }
      for (const el of els) {
        const probes = [el, el.parentElement,
          el.parentElement && el.parentElement.parentElement];
        for (const p of probes) {
          if (!p) continue;
          for (const key in p) {
            try { if (isTerm(p[key])) return p[key]; } catch (e) {}
          }
        }
      }
    }
    return null;
  };

  const docs = collectDocs(document, window);
  const term = findTerminal(docs);
  if (term) {
    const buf = term.buffer.active;
    const total = buf.length;
    const lines = [];
    for (let i = 0; i < total; i++) {
      const line = buf.getLine(i);
      lines.push(line ? line.translateToString(true) : '');
    }
    while (lines.length && lines[lines.length - 1] === '') lines.pop();
    return {
      found: true,
      source: 'xterm-buffer',
      text: lines.join('\n'),
      lines: lines.length,
      cols: term.cols || 0,
      rows: term.rows || 0
    };
  }

  // Fallback A: the xterm accessibility live region (present only when
  // screenReaderMode is enabled). Visible region only, no scrollback.
  for (const [doc] of docs) {
    let acc;
    try { acc = doc.querySelector('.xterm-accessibility'); } catch (e) { acc = null; }
    if (acc && acc.textContent && acc.textContent.trim()) {
      return {
        found: true,
        source: 'xterm-accessibility',
        text: acc.innerText || acc.textContent,
        note: 'Read from the xterm accessibility layer; only the visible region is captured, not scrollback.'
      };
    }
  }

  // Fallback B: DOM-renderer rows. Populated only by the xterm DOM renderer,
  // never the canvas/WebGL renderer.
  for (const [doc] of docs) {
    let rows;
    try { rows = doc.querySelector('.xterm-rows'); } catch (e) { rows = null; }
    if (rows && rows.textContent && rows.textContent.trim()) {
      const text = Array.from(rows.children)
        .map((r) => r.textContent).join('\n').replace(/\s+$/, '');
      return {
        found: true,
        source: 'xterm-rows',
        text: text,
        note: 'Read from the xterm DOM renderer rows; only the visible viewport is available, no scrollback.'
      };
    }
  }

  return {
    found: false,
    source: 'none',
    text: '',
    note: 'No xterm.js terminal found. The terminal may render to a canvas/WebGL surface with no reachable Terminal instance, or live in a cross-origin iframe that same-origin DOM access cannot read.'
  };
})()`

// termFocusScript focuses the xterm helper textarea (searching same-origin
// iframes too) so a subsequent native paste keystroke lands in the terminal.
// Returns true when a textarea was focused. Best-effort — failures are ignored
// by callers.
const termFocusScript = `(() => {
  const focusIn = (doc) => {
    let ta;
    try {
      ta = doc.querySelector('.xterm-helper-textarea') || doc.querySelector('.xterm textarea');
    } catch (e) { ta = null; }
    if (ta) { try { ta.focus(); return true; } catch (e) {} }
    return false;
  };
  const visit = (doc) => {
    if (!doc) return false;
    if (focusIn(doc)) return true;
    let frames;
    try { frames = doc.querySelectorAll('iframe, frame'); } catch (e) { return false; }
    for (const f of frames) {
      let cdoc = null;
      try { cdoc = f.contentDocument; } catch (e) { cdoc = null; }
      if (cdoc && visit(cdoc)) return true;
    }
    return false;
  };
  return visit(document);
})()`

// dispatchPasteShortcut fires the platform paste-as-plain-text shortcut
// against target so the browser
// performs a native paste of the clipboard into the focused element (an
// xterm.js terminal listens for the resulting `paste` event). Sending a real
// key event — rather than a synthetic ClipboardEvent — keeps the paste
// "trusted" so xterm's handler reads it.
func dispatchPasteShortcut(cdp *CdpConnection, targetID string) error {
	event := func(eventType string) map[string]interface{} {
		params := map[string]interface{}{
			"type":                  eventType,
			"key":                   "V",
			"code":                  "KeyV",
			"windowsVirtualKeyCode": 86,
			"nativeVirtualKeyCode":  86,
			"modifiers":             pasteModifierMask,
		}
		if eventType == "keyDown" {
			// CDP key events do not pass through the browser's accelerator
			// handling. Supplying Blink's editor command makes Chromium perform
			// the trusted plain-text paste and dispatch paste/input events.
			params["commands"] = []string{"PasteAndMatchStyle"}
		}
		return params
	}
	if _, err := cdp.SessionCommand(targetID, "Input.dispatchKeyEvent", event("keyDown")); err != nil {
		return err
	}
	_, err := cdp.SessionCommand(targetID, "Input.dispatchKeyEvent", event("keyUp"))
	return err
}
