package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

// textSnapshotScript runs in the page and returns a {title, url, text} object
// representing the visible textual content with obvious chrome (nav/header/
// footer/script/style) stripped out. It is a lightweight reader mode — not
// Mozilla Readability, but sufficient for "what does this page say".
const textSnapshotScript = `(() => {
  const stripSelectors = [
    'script', 'style', 'noscript', 'template',
    'nav', 'header', 'footer', 'aside',
    '[role=navigation]', '[role=banner]', '[role=contentinfo]', '[role=complementary]',
    '[aria-hidden=true]', '[hidden]'
  ];
  const root = document.body ? document.body.cloneNode(true) : null;
  let text = '';
  if (root) {
    for (const sel of stripSelectors) {
      for (const el of root.querySelectorAll(sel)) {
        el.remove();
      }
    }
    // innerText respects layout/visibility; textContent does not. innerText
    // is preferable here because it preserves natural line breaks.
    text = root.innerText || root.textContent || '';
  }
  // Collapse runs of blank lines to keep the output tight.
  text = text.replace(/\n[ \t]+/g, '\n').replace(/\n{3,}/g, '\n\n').trim();
  return { title: document.title || '', url: location.href || '', text };
})()`

// buildTextSnapshot runs textSnapshotScript in the target and returns a
// SnapshotData whose Snapshot field carries the formatted plain-text dump.
// Refs and Elements are intentionally empty: text mode is for human/LLM
// reading, not for follow-up interaction.
func buildTextSnapshot(cdp *CdpConnection, targetID string) (*protocol.SnapshotData, error) {
	raw, err := cdp.Evaluate(targetID, textSnapshotScript, true)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse text snapshot: %w", err)
	}
	return &protocol.SnapshotData{
		Snapshot: formatTextSnapshot(payload.Title, payload.URL, payload.Text),
		Refs:     map[string]*protocol.RefInfo{},
		Elements: []*protocol.ElementInfo{},
	}, nil
}

// formatTextSnapshot composes the title, URL, and body text into a single
// string. Kept as a pure function so it is unit-testable without CDP.
func formatTextSnapshot(title, url, text string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteByte('\n')
	}
	if url != "" {
		b.WriteString(url)
		b.WriteByte('\n')
	}
	if b.Len() > 0 && text != "" {
		b.WriteByte('\n')
	}
	b.WriteString(strings.TrimSpace(text))
	return b.String()
}
