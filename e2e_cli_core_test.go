package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
)

func TestE2ECLICommandsAgainstVerifySite(t *testing.T) {
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
	baseURL := site.URL()

	openResp := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include short tab id: %+v", openResp.Data)
	}

	t.Run("status_and_page_metadata", func(t *testing.T) {
		statusOut := runE2ECLI(t, env, "status")
		requireContains(t, statusOut, `"cdpConnected": true`, "status")
		daemonStatusOut := runE2ECLI(t, env, "daemon", "status")
		requireContains(t, daemonStatusOut, `"running": true`, "daemon status")
		doctorOut := runE2ECLI(t, env, "doctor", "--json")
		requireContains(t, doctorOut, `"name": "CDP connected"`, "doctor")

		requireEvalString(t, env, "document.title", "E2E Verify Home")
		urlResp := runE2EJSON(t, env, "get", "url", "--json")
		requireContains(t, urlResp.Data.Value, baseURL+"/", "get url")
		titleResp := runE2EJSON(t, env, "get", "title", "--json")
		if titleResp.Data.Value != "E2E Verify Home" {
			t.Fatalf("get title = %q", titleResp.Data.Value)
		}
	})

	t.Run("element_actions", func(t *testing.T) {
		snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
		if snapshot.Data.SnapshotData == nil || len(snapshot.Data.SnapshotData.Elements) == 0 {
			t.Fatalf("snapshot returned no elements: %+v", snapshot.Data)
		}
		requireContains(t, snapshot.Data.SnapshotData.Snapshot, "Click counter", "snapshot")

		clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
		hoverRef := refByName(t, snapshot.Data.SnapshotData, "Hover target")
		inputRef := refByName(t, snapshot.Data.SnapshotData, "E2E text input")
		checkRef := refByName(t, snapshot.Data.SnapshotData, "E2E checkbox")
		selectRef := refByName(t, snapshot.Data.SnapshotData, "E2E color select")
		customSelectRef := refByName(t, snapshot.Data.SnapshotData, "E2E custom combobox")
		textareaRef := refByName(t, snapshot.Data.SnapshotData, "Live description")
		nestedRef := refByName(t, snapshot.Data.SnapshotData, "Nested create action")
		frameworkButtonRef := refByName(t, snapshot.Data.SnapshotData, "Framework press action")
		frameworkCheckboxRef := refByName(t, snapshot.Data.SnapshotData, "Framework checkbox")

		getTextResp := runE2EJSON(t, env, "get", "text", clickRef, "--json")
		if getTextResp.Data.Value != "Click me" {
			t.Fatalf("get text on click button = %q", getTextResp.Data.Value)
		}

		runE2EJSON(t, env, "click", clickRef, "--json")
		requireEvalString(t, env, `document.querySelector("#clicked-result").textContent`, "clicked 1")

		runE2EJSON(t, env, "hover", hoverRef, "--json")
		requireEvalString(t, env, `document.querySelector("#hover-result").textContent`, "hovered")

		runE2EJSON(t, env, "fill", inputRef, "hello", "--json")
		requireEvalString(t, env, `document.querySelector("#text-input").value`, "hello")
		runE2EJSON(t, env, "type", inputRef, " world", "--json")
		requireEvalString(t, env, `document.querySelector("#text-input").value`, "hello world")
		runE2EJSON(t, env, "press", "!", "--json")
		requireEvalString(t, env, `document.querySelector("#text-input").value`, "hello world!")

		runE2EJSON(t, env, "check", checkRef, "--json")
		requireEvalBool(t, env, `document.querySelector("#check-box").checked`, true)
		runE2EJSON(t, env, "uncheck", checkRef, "--json")
		requireEvalBool(t, env, `document.querySelector("#check-box").checked`, false)

		runE2EJSON(t, env, "select", selectRef, "green", "--json")
		requireEvalString(t, env, `document.querySelector("#color-select").value`, "green")
		runE2EJSON(t, env, "select", customSelectRef, "繁體中文", "--json")
		requireEvalString(t, env, `document.querySelector("#custom-select-state").textContent`, "繁體中文")

		valueResp := runE2EJSON(t, env, "get", "value", textareaRef, "--json")
		if valueResp.Data.Value != "description from live property" {
			t.Fatalf("get textarea live value = %q", valueResp.Data.Value)
		}

		runE2EJSON(t, env, "click", nestedRef, "--json")
		requireEvalString(t, env, `document.querySelector("#nested-action-state").textContent`, "clicked")

		runE2EJSON(t, env, "click", frameworkButtonRef, "--json")
		requireEvalString(t, env, `document.querySelector("#framework-press-state").textContent`, "pressed")

		runE2EJSON(t, env, "check", frameworkCheckboxRef, "--json")
		requireEvalBool(t, env, `document.querySelector("#framework-checkbox").checked`, true)
		requireEvalString(t, env, `document.querySelector("#framework-checkbox-state").textContent`, "checked")

		scoped := runE2EJSON(t, env, "snapshot", "--interactive", "--selector", "[data-testid=node-settings-panel]", "--json")
		if scoped.Data.SnapshotData == nil {
			t.Fatal("CSS-scoped snapshot returned no data")
		}
		requireContains(t, scoped.Data.SnapshotData.Snapshot, "Live description", "CSS-scoped snapshot")
		requireContains(t, scoped.Data.SnapshotData.Snapshot, "Save scoped panel", "CSS-scoped snapshot")
		requireNotContains(t, scoped.Data.SnapshotData.Snapshot, "Click counter", "CSS-scoped snapshot")
	})

	t.Run("scroll_and_screenshot", func(t *testing.T) {
		runE2EJSON(t, env, "wait", "10", "--json")
		runE2EJSON(t, env, "scroll", "down", "900", "--json")
		runE2EJSON(t, env, "wait", "200", "--json")
		requireEvalBool(t, env, "document.scrollingElement.scrollTop > 0 || window.scrollY > 0", true)

		screenshot := runE2EJSON(t, env, "screenshot", "--json")
		if !strings.HasPrefix(screenshot.Data.DataURL, "data:image/png;base64,") {
			t.Fatalf("screenshot data URL prefix mismatch: %.40q", screenshot.Data.DataURL)
		}
	})

	t.Run("diagnostics_and_fetch", func(t *testing.T) {
		runE2EJSON(t, env, "console", "--clear", "--json")
		runE2EJSON(t, env, "eval", `console.log("e2e-console-from-test"); true`, "--json")
		runE2EJSON(t, env, "wait", "100", "--json")
		consoleResp := runE2EJSON(t, env, "console", "--filter", "e2e-console-from-test", "--json")
		if len(consoleResp.Data.ConsoleMessages) == 0 {
			t.Fatalf("console command did not return e2e-console-from-test: %+v", consoleResp.Data)
		}

		runE2EJSON(t, env, "errors", "--clear", "--json")
		runE2EJSON(t, env, "eval", `setTimeout(() => { throw new Error("e2e thrown error"); }, 0); true`, "--json")
		runE2EJSON(t, env, "wait", "200", "--json")
		errorsResp := runE2EJSON(t, env, "errors", "--filter", "e2e thrown error", "--json")
		if len(errorsResp.Data.JSErrors) == 0 {
			t.Fatalf("errors command did not return e2e thrown error: %+v", errorsResp.Data)
		}

		runE2EJSON(t, env, "network", "clear", "--json")
		runE2EJSON(t, env, "eval", `await fetch("/api/ping?from=e2e").then(r => r.json())`, "--json")
		runE2EJSON(t, env, "wait", "100", "--json")
		networkResp := runE2EJSON(t, env, "network", "requests", "--filter", "/api/ping", "--json")
		if len(networkResp.Data.NetworkRequests) == 0 {
			t.Fatalf("network command did not return /api/ping: %+v", networkResp.Data)
		}

		fetchResp := runE2EJSON(t, env, "fetch", baseURL+"/api/data", "--json")
		fetchResult, ok := fetchResp.Data.Result.(map[string]interface{})
		if !ok || fetchResult["status"].(float64) != 200 {
			t.Fatalf("fetch result = %#v", fetchResp.Data.Result)
		}
		body, ok := fetchResult["body"].(map[string]interface{})
		if !ok || body["message"] != "hello from e2e verify site" {
			t.Fatalf("fetch body = %#v", fetchResp.Data.Result)
		}
	})

	t.Run("dialog_frame_and_trace", func(t *testing.T) {
		runE2EJSON(t, env, "dialog", "accept", "--json")
		dialogEval := runE2EJSON(t, env, "eval", `confirm("e2e confirm dialog")`, "--json")
		if dialogEval.Data.Result != true {
			t.Fatalf("dialog confirm result = %#v", dialogEval.Data.Result)
		}

		frameResp := runE2EJSON(t, env, "frame", "#verify-frame", "--json")
		if frameResp.Data.FrameInfo == nil {
			t.Fatalf("frame command returned no frameInfo: %+v", frameResp.Data)
		}
		runE2EJSON(t, env, "frame", "main", "--json")

		runE2EJSON(t, env, "trace", "start", "--json")
		traceStatus := runE2EJSON(t, env, "trace", "status", "--json")
		if traceStatus.Data.TraceStatus == nil || !traceStatus.Data.TraceStatus.Recording {
			t.Fatalf("trace status not recording: %+v", traceStatus.Data.TraceStatus)
		}
		traceStop := runE2EJSON(t, env, "trace", "stop", "--json")
		if traceStop.Data.TraceStatus == nil || traceStop.Data.TraceStatus.Recording {
			t.Fatalf("trace stop still recording: %+v", traceStop.Data.TraceStatus)
		}
	})

	t.Run("history_and_tabs", func(t *testing.T) {
		runE2EJSON(t, env, "open", baseURL+"/page2", "--tab", tab, "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")
		requireEvalString(t, env, "document.title", "E2E Verify Page Two")
		runE2EJSON(t, env, "back", "--wait-for", "#ready", "--timeout", "5000", "--json")
		requireEvalString(t, env, "document.title", "E2E Verify Home")
		runE2EJSON(t, env, "forward", "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")
		requireEvalString(t, env, "document.title", "E2E Verify Page Two")
		runE2EJSON(t, env, "refresh", "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")

		tabList := runE2EJSON(t, env, "tab", "list", "--json")
		if len(tabList.Data.Tabs) == 0 {
			t.Fatalf("tab list returned no tabs: %+v", tabList.Data)
		}
		newTabResp := runE2EJSON(t, env, "tab", "new", baseURL+"/tab", "--json")
		newTab := newTabResp.Data.Tab
		if newTab == "" {
			t.Fatalf("tab new response did not include short id: %+v", newTabResp.Data)
		}
		runE2EJSON(t, env, "tab", "select", newTab, "--json")
		requireEvalString(t, env, "document.title", "E2E Verify Tab")
		runE2EJSON(t, env, "tab", "close", newTab, "--json")

		runE2EJSON(t, env, "close", "--tab", tab, "--json")
	})
}

func TestE2ECLISPAHistoryNavigation(t *testing.T) {
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
	baseURL := site.URL()
	openResp := runE2EJSON(t, env, "open", baseURL+"/spa", "--new", "--wait-for", `#spa-ready[data-route="home"]`, "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("open SPA response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	homeSnapshot := runE2EJSON(t, env, "snapshot", "--json")
	if homeSnapshot.Data.SnapshotData == nil {
		t.Fatalf("SPA home snapshot returned no snapshot data: %+v", homeSnapshot.Data)
	}
	requireContains(t, homeSnapshot.Data.SnapshotData.Snapshot, "SPA home", "SPA home snapshot")
	detailsRef := refByName(t, homeSnapshot.Data.SnapshotData, "Go to SPA details")

	runE2EJSON(t, env, "eval", `window.__borzSPALoadSentinel = "same-document"`, "--json")
	runE2EJSON(t, env, "click", detailsRef, "--wait-for", `#spa-ready[data-route="details"]`, "--timeout", "5000", "--json")
	requireEvalBool(t, env, `window.__borzSPALoadSentinel === "same-document"`, true)
	requireEvalString(t, env, `document.querySelector("#spa-route-heading").textContent`, "SPA details")
	requireEvalString(t, env, "document.title", "E2E SPA Details")
	detailsURL := runE2EJSON(t, env, "get", "url", "--json")
	if detailsURL.Data.Value != baseURL+"/spa/details" {
		t.Fatalf("SPA details URL = %q, want %q", detailsURL.Data.Value, baseURL+"/spa/details")
	}

	detailsSnapshot := runE2EJSON(t, env, "snapshot", "--json")
	if detailsSnapshot.Data.SnapshotData == nil {
		t.Fatalf("SPA details snapshot returned no snapshot data: %+v", detailsSnapshot.Data)
	}
	requireContains(t, detailsSnapshot.Data.SnapshotData.Snapshot, "Details route content", "SPA details snapshot")

	runE2EJSON(t, env, "back", "--wait-for", `#spa-ready[data-route="home"]`, "--timeout", "5000", "--json")
	requireEvalBool(t, env, `window.__borzSPALoadSentinel === "same-document"`, true)
	requireEvalString(t, env, `document.querySelector("#spa-route-heading").textContent`, "SPA home")
	requireEvalString(t, env, "document.title", "E2E SPA Home")
	homeURL := runE2EJSON(t, env, "get", "url", "--json")
	if homeURL.Data.Value != baseURL+"/spa" {
		t.Fatalf("SPA home URL after back = %q, want %q", homeURL.Data.Value, baseURL+"/spa")
	}

	runE2EJSON(t, env, "forward", "--wait-for", `#spa-ready[data-route="details"]`, "--timeout", "5000", "--json")
	requireEvalBool(t, env, `window.__borzSPALoadSentinel === "same-document"`, true)
	requireEvalString(t, env, `document.querySelector("#spa-route-heading").textContent`, "SPA details")
	requireEvalString(t, env, "document.title", "E2E SPA Details")
	forwardURL := runE2EJSON(t, env, "get", "url", "--json")
	if forwardURL.Data.Value != baseURL+"/spa/details" {
		t.Fatalf("SPA details URL after forward = %q, want %q", forwardURL.Data.Value, baseURL+"/spa/details")
	}
}

func TestE2ECLISnapshotDiff(t *testing.T) {
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
	baseURL := site.URL()
	openResp := runE2EJSON(t, env, "open", baseURL+"/spa", "--new", "--wait-for", `#spa-ready[data-route="home"]`, "--timeout", "10000", "--json")
	firstTab := openResp.Data.Tab
	if firstTab == "" {
		t.Fatalf("open first SPA response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", firstTab, "--json")
	})

	baseline := runE2EJSON(t, env, "snapshot", "--tab", firstTab, "--json")
	if baseline.Data.SnapshotData == nil {
		t.Fatalf("first SPA baseline returned no snapshot data: %+v", baseline.Data)
	}

	runE2EJSON(t, env, "eval", `document.querySelector('[data-spa-route="details"]').setAttribute("aria-disabled", "true")`, "--tab", firstTab, "--json")
	mutation := runE2EJSON(t, env, "snapshot", "--diff", "--tab", firstTab, "--json")
	mutationDiff := mutation.Data.SnapshotDiffData
	if mutationDiff == nil {
		t.Fatalf("mutated SPA snapshot returned no diff data: %+v", mutation.Data)
	}
	if mutationDiff.BaselineReset {
		t.Fatalf("same-document DOM mutation unexpectedly reset baseline: %+v", mutationDiff)
	}
	if len(mutationDiff.Added) != 0 || len(mutationDiff.Removed) != 0 || len(mutationDiff.Changed) != 1 {
		t.Fatalf("SPA mutation diff = %+v, want exactly one changed node", mutationDiff)
	}
	change := mutationDiff.Changed[0]
	disabledDelta, ok := change.AttrChanges["aria-disabled"]
	if change.Name != "Go to SPA details" || change.Role != "link" || change.NameChanged != nil || !ok || disabledDelta.Old != "" || disabledDelta.New != "true" || len(change.AttrChanges) != 1 {
		t.Fatalf("SPA mutation change = %+v", change)
	}

	secondOpen := runE2EJSON(t, env, "open", baseURL+"/spa", "--new", "--wait-for", `#spa-ready[data-route="home"]`, "--timeout", "10000", "--json")
	secondTab := secondOpen.Data.Tab
	if secondTab == "" || secondTab == firstTab {
		t.Fatalf("open second SPA returned invalid tab id %q (first %q)", secondTab, firstTab)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", secondTab, "--json")
	})

	secondDiff := runE2EJSON(t, env, "snapshot", "--diff", "--tab", secondTab, "--json").Data.SnapshotDiffData
	if secondDiff == nil || !secondDiff.BaselineReset || len(secondDiff.Added) == 0 {
		t.Fatalf("second tab did not receive an isolated baseline reset: %+v", secondDiff)
	}
	firstUnchanged := runE2EJSON(t, env, "snapshot", "--diff", "--tab", firstTab, "--json").Data.SnapshotDiffData
	if firstUnchanged == nil || firstUnchanged.BaselineReset || firstUnchanged.Stats.Added != 0 || firstUnchanged.Stats.Removed != 0 || firstUnchanged.Stats.Changed != 0 {
		t.Fatalf("second tab disturbed first tab baseline: %+v", firstUnchanged)
	}

	firstSnapshot := runE2EJSON(t, env, "snapshot", "--tab", firstTab, "--json").Data.SnapshotData
	if firstSnapshot == nil {
		t.Fatal("first SPA returned no snapshot before navigation")
	}
	detailsRef := refByName(t, firstSnapshot, "Go to SPA details")
	runE2EJSON(t, env, "click", detailsRef, "--tab", firstTab, "--wait-for", `#spa-ready[data-route="details"]`, "--timeout", "5000", "--json")
	navigated := runE2EJSON(t, env, "snapshot", "--diff", "--tab", firstTab, "--json")
	navigationDiff := navigated.Data.SnapshotDiffData
	if navigationDiff == nil || !navigationDiff.BaselineReset || len(navigationDiff.Added) == 0 || len(navigationDiff.Removed) != 0 || len(navigationDiff.Changed) != 0 {
		t.Fatalf("SPA navigation did not reset the regenerated snapshot baseline: %+v", navigationDiff)
	}
	if navigated.Data.SnapshotData == nil {
		t.Fatalf("SPA navigation returned no regenerated snapshot: %+v", navigated.Data)
	}
	requireContains(t, navigated.Data.SnapshotData.Snapshot, "Details route content", "SPA navigation snapshot")
}

func TestE2ECLIScreenshotOutput(t *testing.T) {
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
		t.Fatalf("screenshot open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	outputPath := filepath.Join(t.TempDir(), "nested", "screenshot.png")
	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json")
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	requireEvalBool(t, env, `document.querySelector("#playwright-highlight-container .playwright-highlight-label") !== null`, true)

	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(`{"snapshot":{"showRefs":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	hiddenSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json")
	if refByName(t, hiddenSnapshot.Data.SnapshotData, "Click counter") == "" {
		t.Fatal("settings-hidden snapshot did not return actionable refs")
	}
	requireEvalBool(t, env, `document.querySelector("#playwright-highlight-container") === null`, true)

	shownSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--show-refs", "--tab", tab, "--json")
	clickRef = refByName(t, shownSnapshot.Data.SnapshotData, "Click counter")
	requireEvalBool(t, env, `document.querySelector("#playwright-highlight-container .playwright-highlight-label") !== null`, true)
	runE2EJSON(t, env, "clear-refs", "--tab", tab, "--json")
	requireEvalBool(t, env, `document.querySelector("#playwright-highlight-container") === null`, true)

	pathResp := runE2EJSON(t, env, "screenshot", outputPath, "--annotate", "@"+clickRef+"=Click here to continue", "--tab", tab, "--json")
	if pathResp.Data.ScreenshotPath != outputPath {
		t.Fatalf("screenshot path = %q, want %q", pathResp.Data.ScreenshotPath, outputPath)
	}
	pngData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read screenshot output: %v", err)
	}
	pngSignature := []byte("\x89PNG\r\n\x1a\n")
	if len(pngData) < len(pngSignature) || !bytes.Equal(pngData[:len(pngSignature)], pngSignature) {
		t.Fatalf("screenshot output does not have a PNG signature: % x", pngData[:min(len(pngData), len(pngSignature))])
	}
	config, err := png.DecodeConfig(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("decode screenshot output PNG: %v", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		t.Fatalf("screenshot output dimensions = %dx%d, want non-empty dimensions", config.Width, config.Height)
	}
	image, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("decode annotated screenshot PNG: %v", err)
	}
	redPixels := 0
	for y := image.Bounds().Min.Y; y < image.Bounds().Max.Y; y++ {
		for x := image.Bounds().Min.X; x < image.Bounds().Max.X; x++ {
			r, g, b, _ := image.At(x, y).RGBA()
			if r > 50000 && g > 3000 && g < 12000 && b > 9000 && b < 26000 {
				redPixels++
			}
		}
	}
	if redPixels < 100 {
		t.Fatalf("annotated screenshot has only %d callout-red pixels, want at least 100", redPixels)
	}
	requireEvalBool(t, env, `document.querySelectorAll("[data-borz-screenshot-annotation]").length === 0`, true)

	inlineResp := runE2EJSON(t, env, "screenshot", "--tab", tab, "--json")
	if !strings.HasPrefix(inlineResp.Data.DataURL, "data:image/png;base64,") {
		t.Fatalf("inline screenshot data URL prefix mismatch: %.40q", inlineResp.Data.DataURL)
	}
}

func TestE2ECLIViewportEmulation(t *testing.T) {
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
		t.Fatalf("viewport open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	baseline := runE2EJSON(t, env, "viewport", "status", "--tab", tab, "--json").Data.Viewport
	if baseline == nil || baseline.Width <= 0 || baseline.Height <= 0 || baseline.DPR <= 0 {
		t.Fatalf("normal viewport status = %+v", baseline)
	}

	mobile := runE2EJSON(t, env, "viewport", "mobile", "--tab", tab, "--json").Data.Viewport
	if mobile == nil || mobile.Width != 390 || mobile.Height != 844 || mobile.DPR != 3 || !mobile.Mobile || !mobile.Touch {
		t.Fatalf("mobile viewport = %+v", mobile)
	}
	mobileMetrics := runE2EJSON(t, env, "eval", `devicePixelRatio === 3 && navigator.maxTouchPoints > 0`, "--tab", tab, "--json")
	if mobileMetrics.Data.Result != true {
		t.Fatalf("mobile page metrics = %#v, want true", mobileMetrics.Data.Result)
	}

	custom := runE2EJSON(t, env, "viewport", "--width", "640", "--height", "480", "--dpr", "1.5", "--touch", "--tab", tab, "--json").Data.Viewport
	if custom == nil || custom.Width != 640 || custom.Height != 480 || custom.DPR != 1.5 || custom.Mobile || !custom.Touch {
		t.Fatalf("custom viewport = %+v", custom)
	}
	customMetrics := runE2EJSON(t, env, "eval", `innerWidth === 640 && innerHeight === 480 && devicePixelRatio === 1.5 && navigator.maxTouchPoints > 0`, "--tab", tab, "--json")
	if customMetrics.Data.Result != true {
		t.Fatalf("custom page metrics = %#v, want true", customMetrics.Data.Result)
	}

	screenshot := runE2EJSON(t, env, "screenshot", "--tab", tab, "--json")
	const pngDataURLPrefix = "data:image/png;base64,"
	if !strings.HasPrefix(screenshot.Data.DataURL, pngDataURLPrefix) {
		t.Fatalf("emulated screenshot data URL prefix mismatch: %.40q", screenshot.Data.DataURL)
	}
	pngData, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(screenshot.Data.DataURL, pngDataURLPrefix))
	if err != nil {
		t.Fatalf("decode emulated screenshot: %v", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("decode emulated screenshot PNG: %v", err)
	}
	if config.Width != 960 || config.Height != 720 {
		t.Fatalf("emulated screenshot dimensions = %dx%d, want 960x720", config.Width, config.Height)
	}

	reset := runE2EJSON(t, env, "viewport", "reset", "--tab", tab, "--json").Data.Viewport
	if reset == nil || !reset.Reset || reset.Width <= 0 || reset.Height <= 0 || reset.DPR <= 0 {
		t.Fatalf("reset viewport = %+v", reset)
	}
	if reset.Width != baseline.Width || reset.Height != baseline.Height || reset.DPR != baseline.DPR || reset.Mobile != baseline.Mobile || reset.Touch != baseline.Touch {
		t.Fatalf("reset viewport = %+v, want dynamically observed normal metrics %+v", reset, baseline)
	}
}

func TestE2ECLIWaitForDelayedRenderAndTimeout(t *testing.T) {
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
	openResp := runE2EJSON(t, env, "open", site.URL()+"/delayed-render", "--new", "--wait-for", "#delayed-marker", "--timeout", "5000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("delayed render open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})
	requireEvalString(t, env, `document.querySelector("#delayed-marker").textContent`, "Delayed marker ready")

	timeoutResp := runE2EJSONResponse(t, env, "open", site.URL()+"/delayed-render", "--tab", tab, "--wait-for", "#never-rendered", "--timeout", "600", "--json")
	if timeoutResp.Success {
		t.Fatalf("wait-for missing selector unexpectedly succeeded: %+v", timeoutResp)
	}
	requireContains(t, timeoutResp.Error, `wait-for selector "#never-rendered"`, "wait-for timeout error")
	requireContains(t, timeoutResp.Error, "timeout after 600ms", "wait-for timeout error")
}

func TestE2ECLIActionWaitForAndTimeout(t *testing.T) {
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
	openResp := runE2EJSON(t, env, "open", site.URL()+"/async-action", "--new", "--wait-for", "#async-action-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("async action open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--json")
	if snapshot.Data.SnapshotData == nil {
		t.Fatalf("async action snapshot returned no snapshot data: %+v", snapshot.Data)
	}
	actionRef := refByName(t, snapshot.Data.SnapshotData, "Start async action")
	runE2EJSON(t, env, "click", actionRef, "--wait-for", "#async-action-result", "--timeout", "5000", "--json")
	requireEvalString(t, env, `document.querySelector("#async-action-result").textContent`, "Async action 1 complete")

	timeoutResp := runE2EJSONResponse(t, env, "click", actionRef, "--wait-for", "#never-rendered-after-action", "--timeout", "600", "--json")
	if timeoutResp.Success {
		t.Fatalf("post-action wait-for unexpectedly succeeded: %+v", timeoutResp)
	}
	requireContains(t, timeoutResp.Error, `wait-for selector "#never-rendered-after-action"`, "post-action wait-for timeout error")
	requireContains(t, timeoutResp.Error, "timeout after 600ms", "post-action wait-for timeout error")
	requireEvalBool(t, env, `document.querySelector("#async-action-result") === null`, true)
}
