package main

import (
	"github.com/leolin310148/borz/internal/client"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestE2EFeedbackRegressions(t *testing.T) {
	skipUnlessE2E(t)
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><title>Feedback fixture</title>
<input id="live" value="initial"><input id="date" type="date" aria-label="Date">
<input id="password" type="password" value="private-initial" aria-label="Password">
<textarea id="description">initial description</textarea>
<div role="checkbox" tabindex="0" aria-label="Inert checkbox" aria-checked="false">Inert</div>
<div class="monaco-editor"><textarea aria-label="Editor">model text</textarea><span>MEASUREMENT_JUNK</span></div>
<button id="pointer" style="position:fixed;left:10px;top:250px;width:150px;height:100px">Pointer</button>
<script>live.value='current value';description.value='current description';window.moves=[];pointer.onpointermove=e=>moves.push(e.buttons);pointer.onclick=()=>window.pointerClicked=true;</script>`))
	}))
	defer site.Close()
	env := startE2EDaemon(t, home)
	opened := runE2EJSON(t, env, "open", site.URL, "--new", "--json")
	tab := opened.Data.Tab
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })
	snapshot := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	if !strings.Contains(snapshot.Snapshot, "current value") || !strings.Contains(snapshot.Snapshot, "current description") || strings.Contains(snapshot.Snapshot, "private-initial") {
		t.Fatalf("live snapshot: %s", snapshot.Snapshot)
	}
	dateRef := refByName(t, snapshot, "Date")
	runE2EJSON(t, env, "fill", dateRef, "2026-09-07", "--tab", tab, "--json")
	requireEvalString(t, env, `document.querySelector('#date').value`, "2026-09-07")
	snapshot = runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	passwordRef := refByName(t, snapshot, "Password")
	out := runE2ECLI(t, env, "fill", passwordRef, "private-new", "--tab", tab, "--json")
	if strings.Contains(out, "private-new") {
		t.Fatal("fill echoed password in JSON")
	}
	snapshot = runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	editor := refByName(t, snapshot, "Editor")
	out = runE2ECLI(t, env, "fill", editor, "replacement", "--tab", tab, "--json")
	if !strings.Contains(out, `"success": false`) || !strings.Contains(out, "Monaco") {
		t.Fatalf("Monaco fill: %s", out)
	}
	requireEvalString(t, env, `document.querySelector('.monaco-editor textarea').value`, "model text")
	snapshot = runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	checkbox := refByName(t, snapshot, "Inert checkbox")
	out = runE2ECLI(t, env, "check", checkbox, "--tab", tab, "--json")
	if !strings.Contains(out, `"success": false`) {
		t.Fatalf("inert checkbox: %s", out)
	}
	requireEvalString(t, env, `document.querySelector('[role=checkbox]').getAttribute('aria-checked')`, "false")
	text := runE2EJSON(t, env, "snapshot", "--text-only", "--tab", tab, "--json").Data.SnapshotData.Snapshot
	if strings.Contains(text, "MEASUREMENT_JUNK") || !strings.Contains(text, "Monaco editor omitted") {
		t.Fatalf("text snapshot: %s", text)
	}
	runE2EJSON(t, env, "mouse", "click", "50", "280", "--tab", tab, "--json")
	requireEvalString(t, env, `String(window.pointerClicked)`, "true")
	runE2EJSON(t, env, "mouse", "down", "50", "280", "--tab", tab, "--json")
	runE2EJSON(t, env, "mouse", "move", "70", "290", "--button", "left", "--tab", tab, "--json")
	runE2EJSON(t, env, "mouse", "up", "70", "290", "--tab", tab, "--json")
	requireEvalString(t, env, `String(window.moves.includes(1))`, "true")
}
