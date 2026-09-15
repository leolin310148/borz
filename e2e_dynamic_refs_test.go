package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/leolin310148/borz/internal/client"
)

// TestE2EDynamicRefRecovery reproduces the dynamic-control races seen in
// SharePoint without depending on external state:
//   - a portal menu is rebuilt at a different absolute XPath after snapshot;
//   - an already-visible portal item must not be moved by scrollIntoView;
//   - a control is replaced during click hit testing, leaving the snapshotted
//     node detached while an exact semantic replacement occupies its box.
func TestE2EDynamicRefRecovery(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<meta charset="utf-8">
<title>Dynamic ref recovery fixture</title>
<style>
  body { font-family: sans-serif; margin: 24px; }
  button { display: block; width: 220px; height: 44px; margin: 12px 0; }
</style>
<div id="portal-before"><ul><li>One</li><li>Two</li><li><button role="menuitem" aria-label="File upload">File upload</button></li></ul></div>
<button id="visible-portal" role="menuitem" aria-label="Visible portal item">Visible portal item</button>
<button id="replace-during-hit" role="menuitem" aria-label="Open in browser"><span>Open in browser</span></button>
<output id="portal-result">portal idle</output>
<output id="visible-result">visible idle</output>
<output id="hit-result">hit idle</output>
<script>
  const activatePortal = button => button.addEventListener('click', () => {
    const input = document.createElement('input');
    input.type = 'file';
    input.addEventListener('change', () => {
      document.querySelector('#portal-result').textContent = input.files?.[0]?.name || 'no file';
    });
    document.body.appendChild(input);
    input.click();
  });
  activatePortal(document.querySelector('#portal-before button'));
  window.rebuildPortal = () => {
    const replacement = document.createElement('div');
    replacement.id = 'portal-after';
    replacement.innerHTML = '<button role="menuitem" aria-label="File upload">File upload</button>';
    document.querySelector('#portal-before').replaceWith(replacement);
    activatePortal(replacement.querySelector('button'));
  };
  window.armHitReplacement = () => {
    const oldButton = document.querySelector('#replace-during-hit');
    const rect = oldButton.getBoundingClientRect();
    oldButton.getBoundingClientRect = () => {
      const replacement = oldButton.cloneNode(true);
      replacement.addEventListener('click', () => { document.querySelector('#hit-result').textContent = 'replacement clicked'; });
      if (oldButton.isConnected) oldButton.replaceWith(replacement);
      return rect;
    };
  };
  const visiblePortal = document.querySelector('#visible-portal');
  visiblePortal.addEventListener('click', () => { document.querySelector('#visible-result').textContent = 'visible clicked'; });
  visiblePortal.scrollIntoView = () => {
    const rect = visiblePortal.getBoundingClientRect();
    const blocker = document.createElement('div');
    blocker.style.cssText = 'position:fixed;z-index:9999;background:#fff;left:' + rect.left + 'px;top:' + rect.top + 'px;width:' + rect.width + 'px;height:' + rect.height + 'px';
    document.body.appendChild(blocker);
  };
</script>`))
	}))
	defer site.Close()

	env := startE2EDaemon(t, home)
	opened := runE2EJSON(t, env, "open", site.URL, "--new", "--json")
	tab := opened.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	portalRef := refByName(t, snapshot, "File upload")
	// Test setup mutates the local fixture only; the production click still
	// resolves the preserved ref and uses the regular physical hit-test path.
	runE2EJSON(t, env, "eval", `rebuildPortal(); true`, "--tab", tab, "--json")
	uploadPath := filepath.Join(t.TempDir(), "dynamic-ref-upload.md")
	if err := os.WriteFile(uploadPath, []byte("dynamic ref regression\n"), 0o600); err != nil {
		t.Fatalf("write upload fixture: %v", err)
	}
	runE2EJSON(t, env, "filechooser", "accept", uploadPath, "--tab", tab, "--json")
	runE2EJSON(t, env, "click", portalRef, "--tab", tab, "--json")
	requireEvalString(t, env, `document.querySelector('#portal-result').textContent`, "dynamic-ref-upload.md")

	snapshot = runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	visibleRef := refByName(t, snapshot, "Visible portal item")
	runE2EJSON(t, env, "click", visibleRef, "--tab", tab, "--json")
	requireEvalString(t, env, `document.querySelector('#visible-result').textContent`, "visible clicked")

	snapshot = runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	hitRef := refByName(t, snapshot, "Open in browser")
	runE2EJSON(t, env, "eval", `armHitReplacement(); true`, "--tab", tab, "--json")
	runE2EJSON(t, env, "click", hitRef, "--tab", tab, "--json")
	requireEvalString(t, env, `document.querySelector('#hit-result').textContent`, "replacement clicked")
}
