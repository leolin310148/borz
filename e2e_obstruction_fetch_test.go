package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
)

// obstructionFixture: a transient loading overlay that covers a button for
// 600ms (#169), and a SharePoint-style row checkbox whose styled span covers
// the real input (#154).
const obstructionFixture = `<!doctype html>
<meta charset="utf-8">
<title>Obstruction fixture</title>
<style>body { margin: 0; font: 16px sans-serif; }
#go { position: absolute; left: 20px; top: 20px; width: 120px; height: 40px; }
#loading { position: fixed; inset: 0; background: transparent; }
.check { position: absolute; left: 20px; top: 100px; width: 24px; height: 24px; }
.check input { position: absolute; inset: 0; margin: 0; opacity: 0.01; }
.check span { position: absolute; inset: 0; border: 1px solid #333; background: #fff; }
.wide-row { position: absolute; left: 20px; top: 160px; width: 600px; height: 48px; }
.wide-row input { position: absolute; left: 8px; top: 12px; margin: 0; }
.wide-row .cover { position: absolute; left: 0; top: 0; width: 600px; height: 48px; }</style>
<button id="go">Continue</button>
<div class="check"><input type="checkbox" id="row" aria-label="選取資料列"><span></span></div>
<div class="wide-row"><input type="checkbox" id="wide" aria-label="Select file"><span>report.docx</span><div class="cover"></div></div>
<output id="result">idle</output>
<div id="loading"></div>
<script>
  document.querySelector('#go').addEventListener('click', () => { document.querySelector('#result').textContent = 'continued'; });
  window.hideLoading = () => setTimeout(() => document.querySelector('#loading').remove(), 600);
</script>`

func TestE2EClickObstructionAndFetchDiagnostics(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(obstructionFixture))
	}))
	defer site.Close()
	// A second origin that answers without CORS headers.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer other.Close()

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL, "--new", "--wait-for", "#result", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	goRef := refByRoleName(t, snapshot.Data.SnapshotData, "button", "Continue")
	rowRef := refByName(t, snapshot.Data.SnapshotData, "選取資料列")
	wideRef := refByName(t, snapshot.Data.SnapshotData, "Select file")

	// The overlay goes away 600ms after this; click must wait for it.
	runE2EJSON(t, env, "eval", "window.hideLoading()", "--json")
	runE2EJSON(t, env, "click", goRef, "--json")
	requireEvalString(t, env, `document.querySelector("#result").textContent`, "continued")

	// This layout already resolves to the input; the click must toggle it.
	runE2EJSON(t, env, "click", rowRef, "--json")
	requireEvalString(t, env, `String(document.querySelector("#row").checked)`, "true")
	runE2EJSON(t, env, "uncheck", rowRef, "--json")
	requireEvalString(t, env, `String(document.querySelector("#row").checked)`, "false")

	// A row-wide cover over the input (SharePoint #154) cannot be treated as
	// the control; the failure must point at check/uncheck, which works.
	_, out := runE2ECLIError(t, env, "click", wideRef)
	if !strings.Contains(out, "borz check <ref>") {
		t.Fatalf("covered checkbox click must point at check/uncheck:\n%s", out)
	}
	runE2EJSON(t, env, "check", wideRef, "--json")
	requireEvalString(t, env, `String(document.querySelector("#wide").checked)`, "true")

	_, out = runE2ECLIError(t, env, "fetch", other.URL+"/file.docx", "--output", t.TempDir()+"/f.docx")
	if !strings.Contains(out, "cors stage") || !strings.Contains(out, "--tab") {
		t.Fatalf("cross-origin fetch failure must name the CORS stage and an alternative:\n%s", out)
	}
}
