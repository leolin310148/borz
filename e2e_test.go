package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpprotocol "github.com/mark3labs/mcp-go/mcp"
)

const (
	e2eEnabledEnv       = "BORZ_E2E"
	e2eLegacyEnabledEnv = "BB_BROWSER_E2E"
)

func TestE2ECLIHelper(t *testing.T) {
	if os.Getenv("BORZ_E2E_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"borz"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	fmt.Fprintln(os.Stderr, "missing -- before helper command args")
	os.Exit(2)
}

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
	pathResp := runE2EJSON(t, env, "screenshot", outputPath, "--tab", tab, "--json")
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

func TestE2ECLISnapshotModes(t *testing.T) {
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
		t.Fatalf("snapshot modes open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	textOnly := runE2EJSON(t, env, "snapshot", "--text-only", "--json").Data.SnapshotData
	if textOnly == nil {
		t.Fatal("text-only snapshot returned no snapshot data")
	}
	requireContains(t, textOnly.Snapshot, "# E2E Verify Home", "text-only snapshot")
	requireContains(t, textOnly.Snapshot, site.URL()+"/", "text-only snapshot")
	requireContains(t, textOnly.Snapshot, "E2E Verify Site", "text-only snapshot")
	requireNotContains(t, textOnly.Snapshot, "[ref=", "text-only snapshot")
	if len(textOnly.Elements) != 0 || len(textOnly.Refs) != 0 {
		t.Fatalf("text-only snapshot unexpectedly returned refs: %+v", textOnly)
	}

	interactive := runE2EJSON(t, env, "snapshot", "--interactive", "--json").Data.SnapshotData
	if interactive == nil {
		t.Fatal("interactive snapshot returned no snapshot data")
	}
	requireContains(t, interactive.Snapshot, "Click counter", "interactive snapshot")
	requireContains(t, interactive.Snapshot, "E2E text input", "interactive snapshot")
	requireNotContains(t, interactive.Snapshot, "not clicked", "interactive snapshot")

	compactDepth := runE2EJSON(t, env, "snapshot", "--compact", "--depth", "1", "--json").Data.SnapshotData
	if compactDepth == nil {
		t.Fatal("compact/depth snapshot returned no snapshot data")
	}
	requireContains(t, compactDepth.Snapshot, "Click counter", "compact/depth snapshot")
	requireNotContains(t, compactDepth.Snapshot, "<button>", "compact/depth snapshot")
	requireNotContains(t, compactDepth.Snapshot, `text "Click me"`, "compact/depth snapshot")

	selected := runE2EJSON(t, env, "snapshot", "--selector", "click-button", "--json").Data.SnapshotData
	if selected == nil {
		t.Fatal("selector-filtered snapshot returned no snapshot data")
	}
	requireContains(t, selected.Snapshot, "Click counter", "selector-filtered snapshot")
	requireNotContains(t, selected.Snapshot, "Hover target", "selector-filtered snapshot")
	if len(selected.Elements) != 1 || selected.Elements[0].Name != "Click counter" {
		t.Fatalf("selector-filtered elements = %+v", selected.Elements)
	}

	buttons := runE2EJSON(t, env, "snapshot", "--role", "button", "--json").Data.SnapshotData
	if buttons == nil {
		t.Fatal("role-filtered snapshot returned no snapshot data")
	}
	requireContains(t, buttons.Snapshot, "Click counter", "role-filtered snapshot")
	requireContains(t, buttons.Snapshot, "Hover target", "role-filtered snapshot")
	requireNotContains(t, buttons.Snapshot, "E2E text input", "role-filtered snapshot")
	for _, el := range buttons.Elements {
		if el.Role != "button" {
			t.Fatalf("role-filtered snapshot returned non-button element: %+v", el)
		}
	}
}

func TestE2ECLINetworkDiagnostics(t *testing.T) {
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
		t.Fatalf("network diagnostics open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `await Promise.all([
		fetch('/api/network/echo?status=201&response=created', {method: 'POST', body: 'post payload'}),
		fetch('/api/network/echo?status=404&response=missing'),
		fetch('/api/network/slow?status=202&response=slow')
	])`, "--tab", tab, "--json")

	filtered := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--method", "POST", "--status", "2xx", "--with-body", "--tab", tab, "--json")
	if len(filtered.Data.NetworkRequests) != 1 {
		t.Fatalf("filtered network requests = %+v, want one POST 2xx request", filtered.Data.NetworkRequests)
	}
	post := filtered.Data.NetworkRequests[0]
	if post.Method != http.MethodPost || post.Status == nil || *post.Status != http.StatusCreated || !strings.Contains(post.ResponseBody, `"requestBody":"post payload"`) || !strings.Contains(post.ResponseBody, `"response":"created"`) {
		t.Fatalf("filtered POST request = %+v", post)
	}

	limited := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--limit", "2", "--tab", tab, "--json")
	if len(limited.Data.NetworkRequests) != 2 {
		t.Fatalf("limited network requests = %+v, want newest two", limited.Data.NetworkRequests)
	}
	if !strings.Contains(limited.Data.NetworkRequests[0].URL, "status=404") || !strings.Contains(limited.Data.NetworkRequests[1].URL, "/api/network/slow") || limited.Data.NetworkRequests[1].Status == nil || *limited.Data.NetworkRequests[1].Status != http.StatusAccepted {
		t.Fatalf("limited network requests = %+v, want 404 echo then 202 slow response", limited.Data.NetworkRequests)
	}

	runE2EJSON(t, env, "eval", `await fetch('/api/network/echo?status=200&response=since-old')`, "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `await fetch('/api/network/echo?status=200&response=since-new')`, "--tab", tab, "--json")
	since := runE2EJSON(t, env, "network", "requests", "--filter", "response=since-", "--since", "last_action", "--tab", tab, "--json")
	if len(since.Data.NetworkRequests) != 1 || !strings.Contains(since.Data.NetworkRequests[0].URL, "response=since-new") {
		t.Fatalf("network requests since last_action = %+v, want only since-new", since.Data.NetworkRequests)
	}

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	cleared := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/", "--tab", tab, "--json")
	if len(cleared.Data.NetworkRequests) != 0 {
		t.Fatalf("network clear left requests: %+v", cleared.Data.NetworkRequests)
	}
}

func TestE2ECLITabDiagnosticsIsolation(t *testing.T) {
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
	tabOne := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	tabTwo := runE2EJSON(t, env, "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json").Data.Tab
	if tabOne == "" || tabTwo == "" || tabOne == tabTwo {
		t.Fatalf("diagnostics tabs are not distinct: tab one=%q tab two=%q", tabOne, tabTwo)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tabOne, "--json")
		runE2ECLI(t, env, "close", "--tab", tabTwo, "--json")
	})

	clearE2EDiagnostics(t, env, tabOne)
	clearE2EDiagnostics(t, env, tabTwo)
	emitE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one")
	emitE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two")
	requireE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one", "e2e-diag-tab-two", "")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two", "e2e-diag-tab-one", "")

	clearE2EDiagnostics(t, env, tabOne)
	requireNoE2EDiagnostics(t, env, tabOne, "")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two", "e2e-diag-tab-one", "")

	clearE2EDiagnostics(t, env, tabOne)
	clearE2EDiagnostics(t, env, tabTwo)
	for _, tc := range []struct {
		tab   string
		label string
	}{
		{tabOne, "e2e-diag-tab-one"},
		{tabTwo, "e2e-diag-tab-two"},
	} {
		emitE2EDiagnostics(t, env, tc.tab, tc.label+"-old")
		emitE2EDiagnostics(t, env, tc.tab, tc.label+"-new")
	}
	requireE2EDiagnostics(t, env, tabOne, "e2e-diag-tab-one-new", "e2e-diag-tab-two-new", "last_action")
	requireE2EDiagnostics(t, env, tabTwo, "e2e-diag-tab-two-new", "e2e-diag-tab-one-new", "last_action")
}

func clearE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab string) {
	t.Helper()
	runE2EJSON(t, env, "console", "--clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "errors", "--clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
}

func emitE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, label string) {
	t.Helper()
	script := fmt.Sprintf(`console.log(%q);
		setTimeout(() => { throw new Error(%q); }, 0);
		await fetch(%q);
		true`, label, label, "/api/ping?diagnostics="+label)
	runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp := runE2EJSON(t, env, "errors", "--filter", label, "--tab", tab, "--json")
		if len(resp.Data.JSErrors) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for JavaScript error %q on tab %s", label, tab)
}

func requireE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, own, other, since string) {
	t.Helper()
	args := func(command ...string) []string {
		command = append(command, "--filter", "e2e-diag-", "--tab", tab)
		if since != "" {
			command = append(command, "--since", since)
		}
		return append(command, "--json")
	}

	console := runE2EJSON(t, env, args("console")...).Data.ConsoleMessages
	if len(console) != 1 || !strings.Contains(console[0].Text, own) || strings.Contains(console[0].Text, other) {
		t.Fatalf("console diagnostics for tab %s = %+v, want only %q", tab, console, own)
	}
	errors := runE2EJSON(t, env, args("errors")...).Data.JSErrors
	if len(errors) != 1 || !strings.Contains(errors[0].Message, own) || strings.Contains(errors[0].Message, other) {
		t.Fatalf("error diagnostics for tab %s = %+v, want only %q", tab, errors, own)
	}
	network := runE2EJSON(t, env, args("network", "requests")...).Data.NetworkRequests
	if len(network) != 1 || !strings.Contains(network[0].URL, own) || strings.Contains(network[0].URL, other) {
		t.Fatalf("network diagnostics for tab %s = %+v, want only %q", tab, network, own)
	}
}

func requireNoE2EDiagnostics(t *testing.T, env e2eDaemonEnv, tab, since string) {
	t.Helper()
	args := []string{"--filter", "e2e-diag-", "--tab", tab}
	if since != "" {
		args = append(args, "--since", since)
	}
	if got := runE2EJSON(t, env, append([]string{"console"}, append(args, "--json")...)...).Data.ConsoleMessages; len(got) != 0 {
		t.Fatalf("console clear left diagnostics on tab %s: %+v", tab, got)
	}
	if got := runE2EJSON(t, env, append([]string{"errors"}, append(args, "--json")...)...).Data.JSErrors; len(got) != 0 {
		t.Fatalf("errors clear left diagnostics on tab %s: %+v", tab, got)
	}
	if got := runE2EJSON(t, env, append([]string{"network", "requests"}, append(args, "--json")...)...).Data.NetworkRequests; len(got) != 0 {
		t.Fatalf("network clear left diagnostics on tab %s: %+v", tab, got)
	}
}

func TestE2ECLITabLifecycle(t *testing.T) {
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
	primary := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	guard := runE2EJSON(t, env, "open", baseURL+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json").Data.Tab
	if primary == "" || guard == "" || primary == guard {
		t.Fatalf("tab lifecycle fixtures are not distinct: primary=%q guard=%q", primary, guard)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", primary, "--json")
		runE2ECLI(t, env, "close", "--tab", guard, "--json")
	})

	runE2EJSON(t, env, "eval", `window.__borzTabReuseSentinel = "preserved"`, "--tab", primary, "--json")
	reused := runE2EJSON(t, env, "open", baseURL+"/", "--wait-for", "#ready", "--timeout", "5000", "--json")
	if reused.Data.Tab != primary {
		t.Fatalf("exact-URL open selected tab %q, want reused tab %q", reused.Data.Tab, primary)
	}
	reuseState := runE2EJSON(t, env, "eval", `window.__borzTabReuseSentinel`, "--tab", primary, "--json")
	if reuseState.Data.Result != "preserved" {
		t.Fatalf("exact-URL reuse reloaded tab state: result=%#v", reuseState.Data.Result)
	}

	forced := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if forced == "" || forced == primary || forced == guard {
		t.Fatalf("--new returned non-distinct tab %q (primary=%q guard=%q)", forced, primary, guard)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", forced, "--json")
	})

	selectedByID := runE2EJSON(t, env, "tab", "select", primary, "--json")
	if selectedByID.Data.Tab != primary || selectedByID.Data.URL != baseURL+"/" {
		t.Fatalf("select by short id returned %+v", selectedByID.Data)
	}

	tabs := runE2EJSON(t, env, "tab", "list", "--json").Data.Tabs
	forcedIndex := -1
	for _, tab := range tabs {
		if tab.Tab == forced {
			forcedIndex = tab.Index
			break
		}
	}
	if forcedIndex < 0 {
		t.Fatalf("forced tab %q missing from tab list: %+v", forced, tabs)
	}
	selectedByIndex := runE2EJSON(t, env, "tab", strconv.Itoa(forcedIndex), "--json")
	if selectedByIndex.Data.Tab != forced || selectedByIndex.Data.URL != baseURL+"/" {
		t.Fatalf("select by index %d returned %+v", forcedIndex, selectedByIndex.Data)
	}

	closed := runE2EJSON(t, env, "tab", "close", forced, "--json")
	if closed.Data.Tab != forced {
		t.Fatalf("closed tab response = %+v, want tab %q", closed.Data, forced)
	}
	afterClose := runE2EJSON(t, env, "tab", "list", "--json").Data.Tabs
	foundPrimary, foundGuard, foundForced := false, false, false
	for _, tab := range afterClose {
		foundPrimary = foundPrimary || tab.Tab == primary
		foundGuard = foundGuard || tab.Tab == guard
		foundForced = foundForced || tab.Tab == forced
	}
	if !foundPrimary || !foundGuard || foundForced {
		t.Fatalf("tab list after close = %+v, want primary and guard only among test tabs", afterClose)
	}
	missingClose := runE2EJSONResponse(t, env, "tab", "close", forced, "--json")
	if missingClose.Success || !strings.Contains(missingClose.Error, "tab not found") {
		t.Fatalf("second close response = %+v, want structured tab-not-found error", missingClose)
	}

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", primary, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("primary tab snapshot returned no snapshot data")
	}
	staleRef := refByName(t, snapshot, "Click counter")
	runE2EJSON(t, env, "open", baseURL+"/page2", "--tab", primary, "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")
	staleAction := runE2EJSONResponse(t, env, "click", staleRef, "--tab", primary, "--json")
	if staleAction.Success || !strings.Contains(staleAction.Error, "unknown ref: "+staleRef) || !strings.Contains(staleAction.Error, "Run snapshot first") {
		t.Fatalf("stale ref response = %+v, want structured ref-not-found error", staleAction)
	}
	guardTitle := runE2EJSON(t, env, "get", "title", "--tab", guard, "--json")
	if guardTitle.Data.Value != "E2E Verify Page Two" {
		t.Fatalf("guard tab changed or closed: title=%q", guardTitle.Data.Value)
	}
}

func TestE2ECLIDelaySemantics(t *testing.T) {
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
	tab := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if tab == "" {
		t.Fatal("delay semantics open response did not include a tab id")
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("delay semantics snapshot returned no snapshot data")
	}
	clickRef := refByName(t, snapshot, "Click counter")

	const successDelay = 750 * time.Millisecond
	started := time.Now()
	runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--pre-delay", strconv.Itoa(int(successDelay/time.Millisecond)), "--json")
	if elapsed := time.Since(started); elapsed < 600*time.Millisecond {
		t.Fatalf("click with %s pre-delay returned after %s, want at least 600ms", successDelay, elapsed)
	}
	requireEvalString(t, env, `document.querySelector("#clicked-result").textContent`, "clicked 1")

	started = time.Now()
	runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--post-delay", strconv.Itoa(int(successDelay/time.Millisecond)), "--json")
	if elapsed := time.Since(started); elapsed < 600*time.Millisecond {
		t.Fatalf("click with %s post-delay returned after %s, want at least 600ms", successDelay, elapsed)
	}
	requireEvalString(t, env, `document.querySelector("#clicked-result").textContent`, "clicked 2")

	const failedActionPostDelay = 5 * time.Second
	started = time.Now()
	failed := runE2EJSONResponse(t, env, "click", "e999999", "--tab", tab, "--post-delay", strconv.Itoa(int(failedActionPostDelay/time.Millisecond)), "--json")
	failedElapsed := time.Since(started)
	if failed.Success || !strings.Contains(failed.Error, "unknown ref") {
		t.Fatalf("failed click response = %+v, want structured unknown-ref error", failed)
	}
	if failedElapsed >= 3500*time.Millisecond {
		t.Fatalf("failed click took %s with %s post-delay; post-delay should be skipped", failedElapsed, failedActionPostDelay)
	}
}

func TestE2EClientModeAgainstServer(t *testing.T) {
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

	token := "e2e-remote-token"
	env, serverURL := startE2EServer(t, home, token)
	runE2ECLI(t, env, "client", "setup", serverURL, "--token", token)

	statusOut := runE2ECLI(t, env, "--remote", "status")
	requireContains(t, statusOut, `"cdpConnected": true`, "remote status")

	openResp := runE2EJSON(t, env, "--remote", "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	if openResp.Data == nil || openResp.Data.Tab == "" {
		t.Fatalf("remote open response did not include tab: %+v", openResp.Data)
	}
	homeTab := openResp.Data.Tab
	pageTwo := runE2EJSON(t, env, "--remote", "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json")
	if pageTwo.Data == nil || pageTwo.Data.Tab == "" {
		t.Fatalf("remote second open response did not include tab: %+v", pageTwo.Data)
	}
	pageTwoTab := pageTwo.Data.Tab
	for _, tab := range []string{homeTab, pageTwoTab} {
		tab := tab
		t.Cleanup(func() {
			runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
		})
	}

	snapshot := runE2EJSON(t, env, "--remote", "snapshot", "-i", "--tab", homeTab, "--json")
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	inputRef := refByName(t, snapshot.Data.SnapshotData, "E2E text input")
	runE2EJSON(t, env, "--remote", "click", clickRef, "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "fill", inputRef, "remote form value", "--tab", homeTab, "--wait-for", "#input-state", "--timeout", "5000", "--json")
	value := runE2EJSON(t, env, "--remote", "eval", `document.querySelector("#text-input").value`, "--tab", homeTab, "--json")
	if value.Data.Result != "remote form value" {
		t.Fatalf("remote explicitly targeted form value = %#v", value.Data.Result)
	}
	requireEvalStringWithPrefix(t, env, []string{"--remote"}, "document.title", "E2E Verify Page Two")

	runE2EJSON(t, env, "--remote", "console", "--clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "errors", "--clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "network", "clear", "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "eval", `console.log("e2e-remote-console"); setTimeout(() => { throw new Error("e2e remote error"); }, 0); await fetch("/api/ping?from=remote-client"); true`, "--tab", homeTab, "--json")
	runE2EJSON(t, env, "--remote", "wait", "200", "--tab", homeTab, "--json")
	consoleResp := runE2EJSON(t, env, "--remote", "console", "--filter", "e2e-remote-console", "--tab", homeTab, "--json")
	if len(consoleResp.Data.ConsoleMessages) == 0 {
		t.Fatalf("remote console diagnostics missing targeted message: %+v", consoleResp.Data)
	}
	errorsResp := runE2EJSON(t, env, "--remote", "errors", "--filter", "e2e remote error", "--tab", homeTab, "--json")
	if len(errorsResp.Data.JSErrors) == 0 {
		t.Fatalf("remote error diagnostics missing targeted error: %+v", errorsResp.Data)
	}
	networkResp := runE2EJSON(t, env, "--remote", "network", "requests", "--filter", "from=remote-client", "--tab", homeTab, "--json")
	if len(networkResp.Data.NetworkRequests) == 0 {
		t.Fatalf("remote network diagnostics missing targeted request: %+v", networkResp.Data)
	}

	tabs := runE2EJSON(t, env, "--remote", "tab", "list", "--json")
	if len(tabs.Data.Tabs) < 2 {
		t.Fatalf("remote tab list returned fewer than two tabs: %+v", tabs.Data)
	}

	runE2ECLI(t, env, "client", "setup", serverURL, "--no-check")
	_, unauthorized := runE2ECLIError(t, env, "--remote", "status")
	requireContains(t, unauthorized, "borz HTTP 401", "remote missing-token failure")
	runE2ECLI(t, env, "client", "setup", serverURL, "--token", token)
}

func TestE2ERESTAgainstServer(t *testing.T) {
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

	token := "e2e-rest-token"
	_, serverURL := startE2EServer(t, home, token)

	healthResp, err := http.Get(serverURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthResp.Body.Close()
	var health struct {
		OK           bool `json:"ok"`
		CDPConnected bool `json:"cdpConnected"`
	}
	if err := json.NewDecoder(healthResp.Body).Decode(&health); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if healthResp.StatusCode != http.StatusOK || !health.OK || !health.CDPConnected {
		t.Fatalf("GET /healthz = status %d, body %+v", healthResp.StatusCode, health)
	}

	openAPIResp, err := http.Get(serverURL + "/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /openapi.yaml: %v", err)
	}
	defer openAPIResp.Body.Close()
	var openAPIBody bytes.Buffer
	if _, err := openAPIBody.ReadFrom(openAPIResp.Body); err != nil {
		t.Fatalf("read /openapi.yaml: %v", err)
	}
	if openAPIResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", openAPIResp.StatusCode)
	}
	requireContains(t, openAPIBody.String(), "openapi: 3.1.0", "OpenAPI document")
	requireContains(t, openAPIBody.String(), "/v1/open:", "OpenAPI document")

	unauthorizedReq, err := http.NewRequest(http.MethodPost, serverURL+"/v1/snapshot", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build unauthorized REST request: %v", err)
	}
	unauthorizedResp, err := http.DefaultClient.Do(unauthorizedReq)
	if err != nil {
		t.Fatalf("POST /v1/snapshot without bearer token: %v", err)
	}
	defer unauthorizedResp.Body.Close()
	if unauthorizedResp.StatusCode != http.StatusUnauthorized || unauthorizedResp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("unauthorized REST response = status %d, WWW-Authenticate %q", unauthorizedResp.StatusCode, unauthorizedResp.Header.Get("WWW-Authenticate"))
	}

	first := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/", "new": true, "waitFor": "#ready", "timeoutMs": 10000,
	})
	firstTab := first.Data.Tab
	if firstTab == "" {
		t.Fatalf("REST open response did not include tab: %+v", first.Data)
	}
	second := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/page2", "new": true, "waitFor": "#page-two-ready", "timeoutMs": 10000,
	})
	secondTab := second.Data.Tab
	if secondTab == "" || secondTab == firstTab {
		t.Fatalf("second REST open tab = %q, want non-empty tab distinct from %q", secondTab, firstTab)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": firstTab})
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": secondTab})
	})

	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": firstTab,
	})
	if snapshot.Data.Tab != firstTab || snapshot.Data.SnapshotData == nil {
		t.Fatalf("targeted REST snapshot = %+v, want tab %q with snapshot data", snapshot.Data, firstTab)
	}
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	clicked := runE2ERESTJSON(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": clickRef, "tab": firstTab,
	})
	if clicked.Data.Tab != firstTab {
		t.Fatalf("targeted REST click tab = %q, want %q", clicked.Data.Tab, firstTab)
	}
	clickResult := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`, "tab": firstTab,
	})
	if clickResult.Data.Tab != firstTab || clickResult.Data.Result != "clicked 1" {
		t.Fatalf("targeted REST eval = tab %q result %#v, want tab %q result %q", clickResult.Data.Tab, clickResult.Data.Result, firstTab, "clicked 1")
	}
	secondTitle := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": "document.title", "tab": secondTab,
	})
	if secondTitle.Data.Tab != secondTab || secondTitle.Data.Result != "E2E Verify Page Two" {
		t.Fatalf("second targeted REST eval = tab %q result %#v, want tab %q title %q", secondTitle.Data.Tab, secondTitle.Data.Result, secondTab, "E2E Verify Page Two")
	}
}

func TestE2ESiteAdapterTrustAgainstVerifySite(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	verifySite, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = verifySite.Close(ctx)
	})

	adapterDir := filepath.Join(home, "bb-sites", "e2e")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatalf("create community adapter directory: %v", err)
	}
	adapterPath := filepath.Join(adapterDir, "verify.js")
	baseURL := verifySite.URL()
	writeAdapter := func(version string) {
		t.Helper()
		adapter := fmt.Sprintf(`/* @meta
{
  "name": "e2e/verify",
  "description": "Local E2E fixture adapter",
  "domain": "127.0.0.1",
  "startUrl": %q,
  "args": {},
  "readOnly": false
}
*/
async function() {
  const version = %q;
  document.body.dataset.e2eAdapterVersion = version;
  return {version, title: document.title, origin: location.origin};
}`, baseURL+"/", version)
		if err := os.WriteFile(adapterPath, []byte(adapter), 0o644); err != nil {
			t.Fatalf("write community adapter %q: %v", version, err)
		}
	}
	writeAdapter("trusted-v1")

	env := startE2EDaemon(t, home)
	openResp := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("fixture open response did not include a tab: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})
	type adapterInfo struct {
		SHA256  string `json:"sha256"`
		Source  string `json:"source"`
		Trusted bool   `json:"trusted"`
	}
	readInfo := func() adapterInfo {
		t.Helper()
		out := runE2ECLI(t, env, "site", "info", "e2e/verify", "--json")
		var info adapterInfo
		if err := json.Unmarshal([]byte(out), &info); err != nil {
			t.Fatalf("decode site info: %v\n%s", err, out)
		}
		return info
	}

	initial := readInfo()
	if initial.SHA256 == "" || initial.Source != "community" || initial.Trusted {
		t.Fatalf("initial adapter info = %+v, want untrusted community adapter with SHA256", initial)
	}
	_, untrustedOut := runE2ECLIError(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	requireContains(t, untrustedOut, "not trusted yet", "untrusted adapter error")
	requireContains(t, untrustedOut, initial.SHA256, "untrusted adapter error")

	trustOut := runE2ECLI(t, env, "site", "trust", "e2e/verify")
	requireContains(t, trustOut, initial.SHA256, "site trust output")
	trusted := readInfo()
	if trusted.SHA256 != initial.SHA256 || !trusted.Trusted {
		t.Fatalf("trusted adapter info = %+v, want trusted SHA256 %q", trusted, initial.SHA256)
	}

	firstRun := runE2EJSON(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	firstResult, ok := firstRun.Data.Result.(map[string]interface{})
	if !ok || fmt.Sprint(firstResult["version"]) != "trusted-v1" || fmt.Sprint(firstResult["title"]) != "E2E Verify Home" || fmt.Sprint(firstResult["origin"]) != baseURL {
		t.Fatalf("trusted adapter result = %#v, want version trusted-v1, title E2E Verify Home, origin %q", firstRun.Data.Result, baseURL)
	}
	if firstRun.Data.Tab != tab {
		t.Fatalf("trusted adapter tab = %q, want fixture tab %q", firstRun.Data.Tab, tab)
	}

	writeAdapter("changed-version-two")
	changed := readInfo()
	if changed.SHA256 == "" || changed.SHA256 == initial.SHA256 || changed.Trusted {
		t.Fatalf("changed adapter info = %+v, want a new untrusted SHA256", changed)
	}
	_, changedOut := runE2ECLIError(t, env, "site", "run", "e2e/verify", "--tab", tab, "--json")
	requireContains(t, changedOut, "changed hash", "changed adapter error")
	requireContains(t, changedOut, initial.SHA256, "changed adapter error")
	requireContains(t, changedOut, changed.SHA256, "changed adapter error")

	forcedRun := runE2EJSON(t, env, "site", "run", "e2e/verify", "--tab", tab, "--force", "--json")
	forcedResult, ok := forcedRun.Data.Result.(map[string]interface{})
	if !ok || fmt.Sprint(forcedResult["version"]) != "changed-version-two" {
		t.Fatalf("forced changed adapter result = %#v", forcedRun.Data.Result)
	}
	if afterForce := readInfo(); afterForce.Trusted || afterForce.SHA256 != changed.SHA256 {
		t.Fatalf("one-off force unexpectedly changed trust state: %+v", afterForce)
	}
}

func TestE2EMCPStdioAgainstVerifySite(t *testing.T) {
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

	startE2EDaemon(t, home)
	stdio := transport.NewStdio(os.Args[0], []string{
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME=" + home,
	}, "-test.run=TestE2ECLIHelper", "--", "mcp")
	mcpClient := mcpclient.NewClient(stdio)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("start MCP stdio client: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e", Version: "test"}
	initialized, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}
	if initialized.ServerInfo.Name != "borz" || initialized.ProtocolVersion == "" || initialized.Capabilities.Tools == nil {
		t.Fatalf("unexpected MCP initialize result: %+v", initialized)
	}

	baseURL := site.URL() + "/"
	navigate := callE2EMCPTool(t, ctx, mcpClient, "browser_navigate", map[string]interface{}{
		"url": baseURL, "new": true, "waitFor": "#ready", "timeout": 10000,
	})
	requireContains(t, navigate, "Navigated to "+baseURL, "MCP navigate result")
	requireContains(t, navigate, "Page: "+baseURL, "MCP navigate result")

	t.Cleanup(func() {
		if !closed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_close", nil)
		}
	})
	snapshot := callE2EMCPTool(t, ctx, mcpClient, "browser_snapshot", map[string]interface{}{"interactive": true})
	clickRef := refFromMCPSnapshot(t, snapshot, "Click counter")
	clicked := callE2EMCPTool(t, ctx, mcpClient, "browser_click", map[string]interface{}{"ref": clickRef})
	requireContains(t, clicked, "Clicked element @"+clickRef, "MCP click result")

	evaluated := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`,
	})
	if evaluated != `"clicked 1"` {
		t.Fatalf("MCP eval result = %q, want JSON-shaped string %q", evaluated, `"clicked 1"`)
	}
	closedTab := callE2EMCPTool(t, ctx, mcpClient, "browser_close", nil)
	if closedTab != "Tab closed" {
		t.Fatalf("MCP close result = %q, want %q", closedTab, "Tab closed")
	}

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func callE2EMCPTool(t *testing.T, ctx context.Context, client *mcpclient.Client, name string, args map[string]interface{}) string {
	t.Helper()
	result, err := callMCPTool(ctx, client, name, args)
	if err != nil {
		t.Fatalf("call MCP tool %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("MCP tool %s returned an error result: %+v", name, result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("MCP tool %s returned %d content items, want 1: %+v", name, len(result.Content), result)
	}
	text, ok := result.Content[0].(mcpprotocol.TextContent)
	if !ok || text.Type != "text" {
		t.Fatalf("MCP tool %s returned non-text content: %#v", name, result.Content[0])
	}
	return text.Text
}

func callMCPTool(ctx context.Context, client *mcpclient.Client, name string, args map[string]interface{}) (*mcpprotocol.CallToolResult, error) {
	request := mcpprotocol.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = args
	return client.CallTool(ctx, request)
}

func refFromMCPSnapshot(t *testing.T, snapshot, name string) string {
	t.Helper()
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, `"`+name+`"`) {
			continue
		}
		_, after, ok := strings.Cut(line, "[ref=")
		if !ok {
			break
		}
		ref, _, ok := strings.Cut(after, "]")
		if ok && ref != "" {
			return ref
		}
	}
	t.Fatalf("MCP snapshot did not contain a ref for %q:\n%s", name, snapshot)
	return ""
}

type e2eDaemonEnv struct {
	home string
}

func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("local Chrome e2e tests are disabled in GitHub Actions")
	}
	if os.Getenv(e2eEnabledEnv) != "1" && os.Getenv(e2eLegacyEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run local Chrome e2e tests", e2eEnabledEnv)
	}
}

func startE2EDaemon(t *testing.T, home string) e2eDaemonEnv {
	t.Helper()

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	port := freeTCPPort(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(os.Args[0],
		"-test.run=TestE2ECLIHelper",
		"--",
		"daemon",
		"--port", strconv.Itoa(port),
		"--cdp-host", ep.Host,
		"--cdp-port", strconv.Itoa(ep.Port),
		"--idle-tab-timeout", "0",
	)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start borz daemon helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		if t.Failed() {
			t.Logf("daemon stdout:\n%s", stdout.String())
			t.Logf("daemon stderr:\n%s", stderr.String())
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			var health struct {
				OK           bool `json:"ok"`
				CDPConnected bool `json:"cdpConnected"`
			}
			if json.NewDecoder(resp.Body).Decode(&health) == nil && health.OK && health.CDPConnected {
				_ = resp.Body.Close()
				return e2eDaemonEnv{home: home}
			}
			_ = resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("daemon did not become ready; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	return e2eDaemonEnv{home: home}
}

func startE2EServer(t *testing.T, home, token string) (e2eDaemonEnv, string) {
	t.Helper()

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	port := freeTCPPort(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(os.Args[0],
		"-test.run=TestE2ECLIHelper",
		"--",
		"server",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--token", token,
		"--cdp-host", ep.Host,
		"--cdp-port", strconv.Itoa(ep.Port),
		"--idle-tab-timeout", "0",
	)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start borz server helper: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
		}
		if t.Failed() {
			t.Logf("server stdout:\n%s", stdout.String())
			t.Logf("server stderr:\n%s", stderr.String())
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			var health struct {
				OK           bool `json:"ok"`
				CDPConnected bool `json:"cdpConnected"`
			}
			if json.NewDecoder(resp.Body).Decode(&health) == nil && health.OK && health.CDPConnected {
				_ = resp.Body.Close()
				return e2eDaemonEnv{home: home}, fmt.Sprintf("http://127.0.0.1:%d", port)
			}
			_ = resp.Body.Close()
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("server did not become ready; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	return e2eDaemonEnv{home: home}, ""
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local TCP port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func runE2ECLI(t *testing.T, env e2eDaemonEnv, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestE2ECLIHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+env.home,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func runE2ECLIError(t *testing.T, env e2eDaemonEnv, args ...string) (error, string) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestE2ECLIHelper", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"BORZ_E2E_HELPER=1",
		"BORZ_HOME="+env.home,
	)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("borz %s unexpectedly succeeded:\n%s", strings.Join(args, " "), string(out))
	}
	return err, string(out)
}

func runE2EJSON(t *testing.T, env e2eDaemonEnv, args ...string) protocol.Response {
	t.Helper()
	out := runE2ECLI(t, env, args...)
	var resp protocol.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
	}
	if !resp.Success {
		t.Fatalf("borz %s returned unsuccessful response: %s\n%s", strings.Join(args, " "), resp.Error, out)
	}
	if resp.Data == nil {
		t.Fatalf("borz %s returned empty data: %s", strings.Join(args, " "), out)
	}
	return resp
}

func runE2EJSONResponse(t *testing.T, env e2eDaemonEnv, args ...string) protocol.Response {
	t.Helper()
	out := runE2ECLI(t, env, args...)
	var resp protocol.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
	}
	return resp
}

func runE2ERESTJSON(t *testing.T, serverURL, token, path string, body interface{}) protocol.Response {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode REST body for %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build REST request for %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer httpResp.Body.Close()

	var resp protocol.Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		t.Fatalf("decode POST %s response: %v", path, err)
	}
	if httpResp.StatusCode != http.StatusOK || !resp.Success || resp.ID == "" || resp.Data == nil || resp.Error != "" {
		t.Fatalf("POST %s envelope = status %d, response %+v", path, httpResp.StatusCode, resp)
	}
	return resp
}

func refByName(t *testing.T, snapshot *protocol.SnapshotData, name string) string {
	t.Helper()
	for _, el := range snapshot.Elements {
		if el.Name == name {
			return el.Ref
		}
	}
	var got []string
	for _, el := range snapshot.Elements {
		got = append(got, fmt.Sprintf("%s:%s:%s", el.Ref, el.Role, el.Name))
	}
	t.Fatalf("ref %q not found in snapshot elements: %s", name, strings.Join(got, ", "))
	return ""
}

func requireEvalString(t *testing.T, env e2eDaemonEnv, script, want string) {
	t.Helper()
	requireEvalStringWithPrefix(t, env, nil, script, want)
}

func requireEvalStringWithPrefix(t *testing.T, env e2eDaemonEnv, prefix []string, script, want string) {
	t.Helper()
	args := append(append([]string{}, prefix...), "eval", script, "--json")
	resp := runE2EJSON(t, env, args...)
	got, ok := resp.Data.Result.(string)
	if !ok || got != want {
		t.Fatalf("eval %q = %#v, want %q", script, resp.Data.Result, want)
	}
}

func requireEvalBool(t *testing.T, env e2eDaemonEnv, script string, want bool) {
	t.Helper()
	resp := runE2EJSON(t, env, "eval", script, "--json")
	got, ok := resp.Data.Result.(bool)
	if !ok || got != want {
		t.Fatalf("eval %q = %#v, want %v", script, resp.Data.Result, want)
	}
}

func requireContains(t *testing.T, got, want, label string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s missing %q in:\n%s", label, want, got)
	}
}

func requireNotContains(t *testing.T, got, unwanted, label string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("%s unexpectedly contains %q in:\n%s", label, unwanted, got)
	}
}
