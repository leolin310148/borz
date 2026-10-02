package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
)

// mouseDragFixture has a scrollable listbox (the Salesforce combobox pattern
// from #137), selectable text, and a draggable card with a drop zone.
// "mouse down" + "mouse move --button left" + "mouse up" must return promptly
// and release the button every time. Before drag interception, the native
// HTML5 drag swallowed the later events: no drop, and the button stayed held.
const mouseDragFixture = `<!doctype html>
<meta charset="utf-8">
<title>Mouse drag fixture</title>
<style>body { margin: 0; font: 16px sans-serif; }
#list { position: absolute; left: 20px; top: 20px; width: 200px; height: 200px; overflow-y: scroll; border: 1px solid #999; }
#list div { height: 30px; }
#text { position: absolute; left: 20px; top: 260px; width: 400px; }</style>
<div id="list" role="listbox"></div>
<p id="text">Drag across this sentence to select some of its words.</p>
<div id="card" draggable="true" style="position:absolute;left:300px;top:20px;width:80px;height:40px;background:#cde">card</div>
<div id="drop" style="position:absolute;left:300px;top:120px;width:120px;height:80px;border:2px dashed #999">drop</div>
<output id="state">idle</output>
<script>
  const list = document.querySelector('#list');
  for (let i = 0; i < 60; i++) { const d = document.createElement('div'); d.textContent = 'Option ' + i; list.appendChild(d); }
  let downs = 0, ups = 0;
  addEventListener('pointerdown', () => downs++);
  addEventListener('pointerup', () => ups++);
  let dropped = 0;
  const drop = document.querySelector('#drop');
  drop.addEventListener('dragover', e => e.preventDefault());
  drop.addEventListener('drop', e => { e.preventDefault(); dropped++; });
  window.dragState = () => JSON.stringify({ dropped, scrollTop: Math.round(list.scrollTop), downs, ups, selection: String(getSelection()) });
</script>`

func TestE2EMouseDragReturnsPromptly(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(mouseDragFixture))
	}))
	defer site.Close()

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL, "--new", "--wait-for", "#state", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	timed := func(args ...string) {
		t.Helper()
		started := time.Now()
		runE2EJSON(t, env, append(args, "--json")...)
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("borz %s took %s; drag steps must not block until the command timeout", strings.Join(args, " "), elapsed)
		}
	}

	// Scrollbar thumb sits at the list's right edge (x≈213), near its top.
	timed("mouse", "down", "213", "30")
	timed("mouse", "move", "213", "90", "--button", "left")
	timed("mouse", "move", "213", "150", "--button", "left")
	timed("mouse", "up", "213", "150")

	// Text selection drag.
	timed("mouse", "down", "25", "285")
	timed("mouse", "move", "200", "285", "--button", "left")
	timed("mouse", "up", "200", "285")

	// Native HTML5 drag of a draggable element.
	timed("mouse", "down", "320", "40")
	timed("mouse", "move", "330", "60", "--button", "left")
	timed("mouse", "move", "340", "150", "--button", "left")
	timed("mouse", "up", "340", "150")

	state := runE2EJSON(t, env, "eval", "window.dragState()", "--json")
	t.Logf("drag state: %v", state.Data.Result)
	raw, _ := state.Data.Result.(string)
	if !strings.Contains(raw, `"downs":3`) || !strings.Contains(raw, `"ups":3`) {
		t.Fatalf("every mouse down must be matched by a released pointer: %s", raw)
	}
	if !strings.Contains(raw, `"dropped":1`) {
		t.Fatalf("native HTML5 drag must reach the drop target: %s", raw)
	}
	if strings.Contains(raw, `"selection":""`) {
		t.Fatalf("text drag did not select text: %s", raw)
	}
	// The scrollbar drag is timing-only: macOS overlay scrollbars do not
	// take synthetic hits, so scrollTop is environment dependent.
}
