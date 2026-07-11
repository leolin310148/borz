package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestE2ECLIFileUpload(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/file-upload", "--new", "--wait-for", "#file-upload-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("file upload open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("file upload snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	singleRef := refByName(t, snapshot.Data.SnapshotData, "Single file upload")
	multipleRef := refByName(t, snapshot.Data.SnapshotData, "Multiple file upload")

	filesDir := t.TempDir()
	singlePath := filepath.Join(filesDir, "single-note.txt")
	firstPath := filepath.Join(filesDir, "first-note.txt")
	secondPath := filepath.Join(filesDir, "second-note.txt")
	for path, content := range map[string]string{
		singlePath: "single file body",
		firstPath:  "first file body",
		secondPath: "second file body",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write upload fixture %s: %v", filepath.Base(path), err)
		}
	}

	runE2EJSON(t, env, "upload", singleRef, singlePath, "--wait-for", `#single-upload-state[data-file-count="1"]`, "--timeout", "5000", "--json")
	requireEvalString(t, env, `document.querySelector("#single-upload-state").textContent`, "single-note.txt: single file body")

	runE2EJSON(t, env, "upload", multipleRef, firstPath, secondPath, "--wait-for", `#multiple-upload-state[data-file-count="2"]`, "--timeout", "5000", "--json")
	requireEvalString(t, env, `document.querySelector("#multiple-upload-state").textContent`, "first-note.txt: first file body | second-note.txt: second file body")
}

func TestE2ECLIFrameInteraction(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("frame test open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	frameResp := runE2EJSON(t, env, "frame", "#verify-frame", "--json")
	if frameResp.Data.FrameInfo == nil {
		t.Fatalf("frame command returned no frameInfo: %+v", frameResp.Data)
	}
	frameSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if frameSnapshot.Data.SnapshotData == nil {
		t.Fatalf("frame snapshot returned no snapshot data: %+v", frameSnapshot.Data)
	}
	inputRef := refByName(t, frameSnapshot.Data.SnapshotData, "Frame text input")
	submitRef := refByName(t, frameSnapshot.Data.SnapshotData, "Submit frame input")

	runE2EJSON(t, env, "fill", inputRef, "inside iframe", "--json")
	runE2EJSON(t, env, "click", submitRef, "--json")
	resultSnapshot := runE2EJSON(t, env, "snapshot", "--json")
	if resultSnapshot.Data.SnapshotData == nil {
		t.Fatalf("frame result snapshot returned no snapshot data: %+v", resultSnapshot.Data)
	}
	requireContains(t, resultSnapshot.Data.SnapshotData.Snapshot, "Frame received: inside iframe", "frame result snapshot")

	runE2EJSON(t, env, "frame", "main", "--json")
	mainSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if mainSnapshot.Data.SnapshotData == nil {
		t.Fatalf("main-frame snapshot returned no snapshot data: %+v", mainSnapshot.Data)
	}
	mainClickRef := refByName(t, mainSnapshot.Data.SnapshotData, "Click counter")
	runE2EJSON(t, env, "click", mainClickRef, "--json")
	requireEvalString(t, env, `document.querySelector("#clicked-result").textContent`, "clicked 1")
	requireEvalBool(t, env, `document.querySelector("#frame-text-input") === null`, true)
}

func TestE2ECLIDialogHandling(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/dialogs", "--new", "--wait-for", "#dialogs-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("dialogs open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("dialogs snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	alertRef := refByName(t, snapshot.Data.SnapshotData, "Open alert dialog")
	confirmRef := refByName(t, snapshot.Data.SnapshotData, "Open confirm dialog")
	promptRef := refByName(t, snapshot.Data.SnapshotData, "Open prompt dialog")

	runE2EJSON(t, env, "dialog", "accept", "--json")
	runE2EJSON(t, env, "click", alertRef, "--json")
	requireEvalString(t, env, `document.querySelector("#alert-result").textContent`, "alert accepted")

	runE2EJSON(t, env, "dialog", "dismiss", "--json")
	runE2EJSON(t, env, "click", confirmRef, "--json")
	requireEvalString(t, env, `document.querySelector("#confirm-result").textContent`, "confirm: false")

	runE2EJSON(t, env, "dialog", "accept", "typed prompt text", "--json")
	runE2EJSON(t, env, "click", promptRef, "--json")
	requireEvalString(t, env, `document.querySelector("#prompt-result").textContent`, "prompt: typed prompt text")

	// A freshly armed handler without text must not reuse the prior prompt value.
	runE2EJSON(t, env, "dialog", "accept", "--json")
	runE2EJSON(t, env, "click", promptRef, "--json")
	requireEvalString(t, env, `document.querySelector("#prompt-result").textContent`, "prompt: ")
}

func TestE2ECLIKeyboardInteraction(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/keyboard", "--new", "--wait-for", "#keyboard-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("keyboard open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})
	tabArgs := []string{"--tab", tab}
	eval := func(script, want string) {
		requireEvalStringWithPrefix(t, env, tabArgs, script, want)
	}
	press := func(key string, extra ...string) {
		args := []string{"press", key, "--tab", tab, "--json"}
		args = append(args, extra...)
		runE2EJSON(t, env, args...)
	}

	eval(`document.activeElement.blur(); document.activeElement.id`, "")
	press("Tab")
	eval(`document.activeElement.id`, "focus-first")
	press("Tab")
	eval(`document.activeElement.id`, "enter-button")
	press("Tab", "--modifiers", "shift")
	eval(`document.activeElement.id`, "focus-first")

	eval(`document.querySelector("#enter-button").focus(); document.activeElement.id`, "enter-button")
	press("Enter")
	eval(`document.querySelector("#activation-result").textContent`, "enter activated")
	eval(`document.querySelector("#space-button").focus(); document.activeElement.id`, "space-button")
	press("Space")
	eval(`document.querySelector("#activation-result").textContent`, "space activated")

	eval(`document.querySelector("#open-panel").click(); String(document.querySelector("#dismissible-panel").hidden)`, "false")
	press("Escape")
	eval(`String(document.querySelector("#dismissible-panel").hidden)`, "true")

	eval(`document.querySelector("#arrow-list").focus(); document.activeElement.id`, "arrow-list")
	press("ArrowDown")
	eval(`document.querySelector("#arrow-result").textContent`, "Choice two")
	press("ArrowDown")
	eval(`document.querySelector("#arrow-list").getAttribute("aria-activedescendant")`, "arrow-three")
	press("ArrowUp")
	eval(`document.querySelector("#arrow-result").textContent`, "Choice two")

	press("k", "--modifiers", "ctrl,alt,shift")
	eval(`document.querySelector("#key-event-data").textContent`, `{"key":"k","target":"arrow-list","alt":true,"ctrl":true,"meta":false,"shift":true}`)
}

func TestE2ECLIClipboardWriteAndPaste(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", site.URL()+"/clipboard", "--new", "--wait-for", "#clipboard-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("clipboard open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	plainText := "plain-clipboard-secret"
	writeResp := runE2EJSON(t, env, "clipboard-write", plainText, "--tab", tab, "--json")
	if writeResp.Data.Value != plainText {
		t.Fatalf("clipboard-write value = %q, want %q", writeResp.Data.Value, plainText)
	}
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `await navigator.clipboard.readText()`, plainText)

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("clipboard snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	inputRef := refByName(t, snapshot.Data.SnapshotData, "Clipboard paste input")
	runE2EJSON(t, env, "click", inputRef, "--tab", tab, "--json")

	pastedText := "clipboard-secret-純文字\nsecond-line-🚀"
	pasteResp := runE2EJSON(t, env, "clipboard-write", pastedText, "--paste", "--tab", tab, "--json")
	result, ok := pasteResp.Data.Result.(map[string]interface{})
	if !ok || result["written"] != true || result["pasted"] != true {
		t.Fatalf("clipboard paste result = %#v", pasteResp.Data.Result)
	}
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#clipboard-input").value`, pastedText)
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#paste-event").textContent`, pastedText)
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#paste-event").dataset.count`, "1")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#input-event").textContent`, pastedText)
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#input-event").dataset.count`, "1")

	logs := runE2ECLI(t, env, "logs", "tail", "--lines", "200", "--json")
	for _, secret := range []string{plainText, "clipboard-secret-", "純文字", "second-line-", "🚀"} {
		requireNotContains(t, logs, secret, "operational logs")
	}
	var entries []struct {
		Action    string `json:"action"`
		TextBytes int    `json:"text_bytes"`
	}
	if err := json.Unmarshal([]byte(logs), &entries); err != nil {
		t.Fatalf("decode operational logs: %v\n%s", err, logs)
	}
	wantSizes := map[int]bool{len(plainText): false, len(pastedText): false}
	for _, entry := range entries {
		if entry.Action == string(protocol.ActionClipboardWrite) {
			if _, wanted := wantSizes[entry.TextBytes]; wanted {
				wantSizes[entry.TextBytes] = true
			}
		}
	}
	for size, found := range wantSizes {
		if !found {
			t.Errorf("operational logs missing clipboard_write metadata with text_bytes=%d", size)
		}
	}
}
