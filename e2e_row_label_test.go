package main

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
)

// rowLabelFixture covers two feedback reports:
//   - #162: a SharePoint-style list renders a same-named "選取資料列" checkbox in
//     every row and re-renders all rows (e.g. after a download), detaching
//     every snapshotted element; recovery must tell the rows apart.
//   - #171: an error screen whose "Retry" action is a plain element with an
//     addEventListener handler, so the snapshot shows its text but no ref.
const rowLabelFixture = `<!doctype html>
<meta charset="utf-8">
<title>Row and label fixture</title>
<style>body { font-family: sans-serif; margin: 24px; } td { padding: 4px 8px; }</style>
<table><tbody id="rows"></tbody></table>
<div id="error">
  <p>Something went wrong.</p>
  <div id="retry"><span>Retry</span></div>
  <button id="clear">Clear cache and retry</button>
  <span class="dismiss">Dismiss</span> <span class="dismiss">Dismiss</span>
</div>
<output id="result">idle</output>
<script>
  const files = ['Budget.xlsx', 'Plan.docx', 'Notes.txt'];
  const result = document.querySelector('#result');
  const checked = new Set();
  function render() {
    const body = document.querySelector('#rows');
    body.replaceChildren(...files.map(name => {
      const tr = document.createElement('tr');
      tr.innerHTML = '<td><input type="checkbox" aria-label="選取資料列"></td><td>' + name + '</td>';
      const box = tr.querySelector('input');
      box.checked = checked.has(name);
      box.addEventListener('change', () => {
        if (box.checked) checked.add(name); else checked.delete(name);
        result.textContent = 'checked ' + [...checked].sort().join(',');
      });
      return tr;
    }));
  }
  window.rerender = render;
  render();
  document.querySelector('#retry').addEventListener('click', () => { result.textContent = 'retried'; });
  document.querySelector('#clear').addEventListener('click', () => { result.textContent = 'cleared'; });
</script>`

func TestE2ERowCheckboxRecoveryAndLabelClick(t *testing.T) {
	skipUnlessE2E(t)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(rowLabelFixture))
	}))
	defer site.Close()

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL, "--new", "--wait-for", "#result", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("snapshot returned no data: %+v", snapshot.Data)
	}
	var boxes []int
	for ref, info := range snapshot.Data.SnapshotData.Refs {
		if info.Name == "選取資料列" {
			n, _ := strconv.Atoi(ref)
			boxes = append(boxes, n)
		}
		if info.Name == "Retry" {
			t.Fatalf("fixture invalid: Retry got ref %s, want no ref", ref)
		}
	}
	if len(boxes) != 3 {
		t.Fatalf("want three row checkboxes, got %v", boxes)
	}
	// Refs follow document order; the middle one is the Plan.docx row.
	sort.Ints(boxes)
	second := boxes[1]

	// #162: every row is rebuilt, so the snapshotted checkbox is detached and
	// three same-named candidates remain; the row text must pick Plan.docx.
	runE2EJSON(t, env, "eval", "rerender()", "--json")
	runE2EJSON(t, env, "click", strconv.Itoa(second), "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "checked Plan.docx")

	// #171: Retry has no ref; --label clicks it exactly, never the
	// "Clear cache and retry" button that also contains the word.
	runE2EJSON(t, env, "click", "--label", "Retry", "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "retried")

	// Ambiguous and missing labels refuse instead of guessing.
	if err, out := runE2ECLIError(t, env, "click", "--label", "Dismiss"); err == nil || !strings.Contains(out, "2 visible controls are labeled exactly") {
		t.Fatalf("ambiguous --label: err=%v out=%s", err, out)
	}
	if err, out := runE2ECLIError(t, env, "click", "--label", "retry"); err == nil || !strings.Contains(out, "no visible element is labeled exactly") {
		t.Fatalf("case-mismatched --label: err=%v out=%s", err, out)
	}
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "retried")
}
