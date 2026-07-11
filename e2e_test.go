package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func TestE2ECLIShadowDOMBoundary(t *testing.T) {
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
	openResp := runE2EJSON(t, env, "open", site.URL()+"/shadow-dom", "--new", "--wait-for", `[data-shadow-ready="true"]`, "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("shadow DOM open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	// The tree snapshot and its substring selector traverse an open shadow root,
	// while page CSS selectors remain scoped to the document unless eval enters
	// host.shadowRoot explicitly.
	requireEvalBool(t, env, `document.querySelector("#shadow-action-button") === null`, true)
	requireEvalBool(t, env, `document.querySelector("#shadow-host").shadowRoot.mode === "open"`, true)

	selected := runE2EJSON(t, env, "snapshot", "--interactive", "--selector", "shadow-action-button", "--tab", tab, "--json").Data.SnapshotData
	if selected == nil || len(selected.Elements) != 1 || selected.Elements[0].Name != "Shadow action button" {
		t.Fatalf("shadow DOM selector-filtered elements = %+v", selected)
	}

	snapshot := runE2EJSON(t, env, "snapshot", "--interactive", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("shadow DOM snapshot returned no snapshot data")
	}
	requireContains(t, snapshot.Snapshot, "Shadow action button", "shadow DOM snapshot")
	requireContains(t, snapshot.Snapshot, "Shadow text input", "shadow DOM snapshot")
	buttonRef := refByName(t, snapshot, "Shadow action button")
	inputRef := refByName(t, snapshot, "Shadow text input")

	runE2EJSON(t, env, "click", buttonRef, "--tab", tab, "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-result").textContent`, "clicked 1")

	runE2EJSON(t, env, "fill", inputRef, "shadow value 純文字", "--tab", tab, "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-text-input").value`, "shadow value 純文字")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, `document.querySelector("#shadow-host").shadowRoot.querySelector("#shadow-result").textContent`, "value: shadow value 純文字")
}

func TestE2ECLIAccessibilityState(t *testing.T) {
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
	openResp := runE2EJSON(t, env, "open", site.URL()+"/accessibility-state", "--new", "--wait-for", "#accessibility-state-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("accessibility state open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	initial := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	if initial == nil {
		t.Fatal("initial accessibility state snapshot returned no data")
	}
	for _, want := range []string{
		`button "Disabled action" [disabled]`,
		`button [ref=`,
		`"State disclosure" [expanded=false]`,
		`"State checkbox" [checked=false]`,
		`"Choice one" [selected=true]`,
		`"Choice two" [selected=false]`,
		`"State updates" [live=polite]`,
		`State idle`,
	} {
		requireContains(t, initial.Snapshot, want, "initial accessibility state snapshot")
	}
	requireNotContains(t, initial.Snapshot, "Revealed accessibility details", "initial accessibility state snapshot")
	for _, element := range initial.Elements {
		if element.Name == "Disabled action" {
			t.Fatalf("disabled action unexpectedly received interactive ref %q", element.Ref)
		}
	}
	mutateRef := refByName(t, initial, "Mutate accessibility state")

	runE2EJSON(t, env, "click", mutateRef, "--tab", tab, "--wait-for", `#state-live[data-state="updated"]`, "--timeout", "5000", "--json")
	updated := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	for _, want := range []string{
		`button [ref=`,
		`"Disabled action"`,
		`"State disclosure" [expanded=true]`,
		`Revealed accessibility details`,
		`"State checkbox" [checked=true]`,
		`"Choice one" [selected=false]`,
		`"Choice two" [selected=true]`,
		`Accessibility state updated`,
	} {
		requireContains(t, updated.Snapshot, want, "updated accessibility state snapshot")
	}
	requireNotContains(t, updated.Snapshot, `"Disabled action" [disabled]`, "updated accessibility state snapshot")
	if refByName(t, updated, "Disabled action") == "" {
		t.Fatal("enabled action did not receive a refreshed ref")
	}
	refreshedMutateRef := refByName(t, updated, "Mutate accessibility state")
	if refreshedMutateRef == mutateRef {
		t.Fatalf("mutation ref was not regenerated after interactive state changed: %q", mutateRef)
	}

	runE2EJSON(t, env, "click", refreshedMutateRef, "--tab", tab, "--json")
	reset := runE2EJSON(t, env, "snapshot", "--tab", tab, "--json").Data.SnapshotData
	requireContains(t, reset.Snapshot, `"State disclosure" [expanded=false]`, "reset accessibility state snapshot")
	requireNotContains(t, reset.Snapshot, "Revealed accessibility details", "reset accessibility state snapshot")
}

func TestE2ECLINestedScrolling(t *testing.T) {
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
	openResp := runE2EJSON(t, env, "open", site.URL()+"/scrolling", "--new", "--wait-for", `[data-initialized="true"]`, "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("scrolling open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "viewport", "800x600", "--dpr", "1", "--tab", tab, "--json")
	runE2EJSON(t, env, "eval", `(() => {
      window.scrollTo(0, 0);
      document.querySelector('#outer-scroll').scrollTo(40, 50);
      document.querySelector('#inner-scroll').scrollTo(60, 70);
      return true;
    })()`, "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")

	check := func(label, script string) {
		t.Helper()
		resp := runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")
		if got, ok := resp.Data.Result.(bool); !ok || !got {
			state := runE2EJSON(t, env, "eval", `(() => {
          const root = document.scrollingElement;
          const outer = document.querySelector('#outer-scroll');
          const inner = document.querySelector('#inner-scroll');
          const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
          return {
            x: scrollX, y: scrollY,
            maxX: root.scrollWidth - innerWidth, maxY: root.scrollHeight - innerHeight,
            outerX: outer.scrollLeft, outerY: outer.scrollTop,
            innerX: inner.scrollLeft, innerY: inner.scrollTop,
            marker: { left: marker.left, top: marker.top, right: marker.right, bottom: marker.bottom }
          };
        })()`, "--tab", tab, "--json")
			t.Fatalf("%s state check = %#v, want true; positions = %#v", label, resp.Data.Result, state.Data.Result)
		}
	}
	nestedUnchanged := `
      const outer = document.querySelector('#outer-scroll');
      const inner = document.querySelector('#inner-scroll');
      const nestedStable = outer.scrollLeft === 40 && outer.scrollTop === 50 && inner.scrollLeft === 60 && inner.scrollTop === 70;`
	check("initial", `(() => {`+nestedUnchanged+`
      const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
      const markerVisible = marker.left >= 0 && marker.top >= 0 && marker.right <= innerWidth && marker.bottom <= innerHeight;
      return scrollX === 0 && scrollY === 0 && nestedStable && !markerVisible;
    })()`)

	// The default distance is 300px; the remaining directional commands use
	// explicit distances so both parsing paths and every axis are exercised.
	runE2EJSON(t, env, "scroll", "down", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "right", "220", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "up", "125", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	runE2EJSON(t, env, "scroll", "left", "70", "--tab", tab, "--json")
	runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	check("directional pixels", `(() => {`+nestedUnchanged+`
      return scrollX === 150 && scrollY === 175 && nestedStable;
    })()`)

	for _, direction := range []string{"down", "right"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("maximum", `(() => {`+nestedUnchanged+`
      const root = document.scrollingElement;
      const marker = document.querySelector('#viewport-end-marker').getBoundingClientRect();
      const markerVisible = marker.left >= 0 && marker.top >= 0 && marker.right <= innerWidth && marker.bottom <= innerHeight;
      return scrollX === root.scrollWidth - innerWidth && scrollY === root.scrollHeight - innerHeight && nestedStable && markerVisible;
    })()`)
	for _, direction := range []string{"down", "right"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("stable maximum", `(() => {
      const root = document.scrollingElement;
      return scrollX === root.scrollWidth - innerWidth && scrollY === root.scrollHeight - innerHeight;
    })()`)

	for _, direction := range []string{"up", "left", "up", "left"} {
		runE2EJSON(t, env, "scroll", direction, "10000", "--tab", tab, "--json")
		runE2EJSON(t, env, "wait", "150", "--tab", tab, "--json")
	}
	check("stable origin", `(() => {`+nestedUnchanged+`
      return scrollX === 0 && scrollY === 0 && nestedStable;
    })()`)
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

func TestE2ECLICacheNavigation(t *testing.T) {
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
	cacheableURL := site.URL() + "/cache/cacheable"
	openResp := runE2EJSON(t, env, "open", cacheableURL, "--new", "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	tab := openResp.Data.Tab
	if tab == "" {
		t.Fatalf("cache fixture open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "1")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "refresh", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	refreshCount := runE2EJSON(t, env, "eval", "document.querySelector('#request-count').textContent", "--tab", tab, "--json").Data.Result
	if refreshCount != "1" && refreshCount != "2" {
		t.Fatalf("cacheable refresh count = %#v, want cached 1 or revalidated 2", refreshCount)
	}
	cacheableRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/cacheable", "--tab", tab, "--json").Data.NetworkRequests
	if len(cacheableRequests) == 0 {
		t.Fatal("cacheable refresh produced no network metadata")
	}
	cacheable := cacheableRequests[len(cacheableRequests)-1]
	if cacheable.Status == nil || *cacheable.Status != http.StatusOK || networkHeader(cacheable.ResponseHeaders, "Cache-Control") != "public, max-age=3600" {
		t.Fatalf("cacheable refresh metadata = %+v", cacheable)
	}
	if cacheable.FromDiskCache && refreshCount != "1" {
		t.Fatalf("disk-cached response advanced fixture counter: count=%#v request=%+v", refreshCount, cacheable)
	}

	// Navigating away and back may use the back-forward cache, the HTTP cache,
	// or revalidate. All are valid as long as at most one server request occurs.
	runE2EJSON(t, env, "open", site.URL()+"/", "--tab", tab, "--wait-for", "#ready", "--timeout", "10000", "--json")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "back", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	backCount := runE2EJSON(t, env, "eval", "document.querySelector('#request-count').textContent", "--tab", tab, "--json").Data.Result
	if backCount != refreshCount && !(refreshCount == "1" && backCount == "2") && !(refreshCount == "2" && backCount == "3") {
		t.Fatalf("cacheable back-navigation count advanced unexpectedly: refresh=%#v back=%#v", refreshCount, backCount)
	}
	backRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/cacheable", "--tab", tab, "--json").Data.NetworkRequests
	if len(backRequests) > 1 {
		t.Fatalf("cacheable back navigation made multiple requests: %+v", backRequests)
	}

	runE2EJSON(t, env, "open", site.URL()+"/cache/no-cache", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "1")
	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "refresh", "--tab", tab, "--wait-for", "#cache-ready", "--timeout", "10000", "--json")
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.querySelector('#request-count').textContent", "2")
	noCacheRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/cache/no-cache", "--tab", tab, "--json").Data.NetworkRequests
	if len(noCacheRequests) == 0 {
		t.Fatal("no-cache refresh produced no network metadata")
	}
	noCache := noCacheRequests[len(noCacheRequests)-1]
	if noCache.FromDiskCache || noCache.Status == nil || *noCache.Status != http.StatusOK || networkHeader(noCache.ResponseHeaders, "Cache-Control") != "no-store" {
		t.Fatalf("no-cache refresh metadata = %+v", noCache)
	}
}

func networkHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func TestE2ECLIStreamingNetworkFailures(t *testing.T) {
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
		t.Fatalf("streaming network open response did not include short tab id: %+v", openResp.Data)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	stream := runE2EJSON(t, env, "eval", `await fetch('/api/network/stream').then(response => response.text())`, "--tab", tab, "--json")
	if stream.Data.Result != "stream-chunk-one\nstream-chunk-two\n" {
		t.Fatalf("stream response = %#v", stream.Data.Result)
	}
	streamRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/stream", "--with-body", "--tab", tab, "--json").Data.NetworkRequests
	if len(streamRequests) != 1 || streamRequests[0].Status == nil || *streamRequests[0].Status != http.StatusOK || streamRequests[0].Failed || streamRequests[0].ResponseBody != "stream-chunk-one\nstream-chunk-two\n" {
		t.Fatalf("stream network lifecycle = %+v", streamRequests)
	}

	aborted := runE2EJSON(t, env, "eval", `await fetch('/api/network/abort').then(response => response.text()).then(
		value => ({value}), error => ({name: error.name, message: error.message})
	)`, "--tab", tab, "--json")
	abortResult, ok := aborted.Data.Result.(map[string]interface{})
	if !ok || abortResult["name"] != "TypeError" || abortResult["message"] == "" {
		t.Fatalf("aborted fetch result = %#v", aborted.Data.Result)
	}
	abortRequests := runE2EJSON(t, env, "network", "requests", "--filter", "/api/network/abort", "--tab", tab, "--json").Data.NetworkRequests
	if len(abortRequests) != 1 || abortRequests[0].Status == nil || *abortRequests[0].Status != http.StatusOK || !abortRequests[0].Failed || abortRequests[0].FailureReason == "" {
		t.Fatalf("aborted network lifecycle = %+v", abortRequests)
	}

	timedOut := runE2EJSON(t, env, "eval", `await (async () => {
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), 75);
		try {
			await fetch('/api/network/stream?case=timeout', {signal: controller.signal}).then(response => response.text());
			return {completed: true};
		} catch (error) {
			return {name: error.name, message: error.message};
		} finally {
			clearTimeout(timer);
		}
	})()`, "--tab", tab, "--json")
	timeoutResult, ok := timedOut.Data.Result.(map[string]interface{})
	if !ok || timeoutResult["name"] != "AbortError" || timeoutResult["message"] == "" {
		t.Fatalf("timed out stream result = %#v", timedOut.Data.Result)
	}
	timeoutRequests := runE2EJSON(t, env, "network", "requests", "--filter", "case=timeout", "--tab", tab, "--json").Data.NetworkRequests
	if len(timeoutRequests) != 1 || !timeoutRequests[0].Failed || !strings.Contains(timeoutRequests[0].FailureReason, "ERR_ABORTED") {
		t.Fatalf("timed out network lifecycle = %+v", timeoutRequests)
	}

	// A failed transfer must not poison the daemon or the tab's browser context.
	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "document.title", "E2E Verify Home")
	ping := runE2EJSON(t, env, "eval", `await fetch('/api/ping?after=failed-transfer').then(response => response.json()).then(value => value.ok)`, "--tab", tab, "--json")
	if ping.Data.Result != "true" {
		t.Fatalf("post-failure ping result = %#v", ping.Data.Result)
	}
}

func TestE2ECLIRedirectChain(t *testing.T) {
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
	tab := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if tab == "" {
		t.Fatal("redirect fixture open response did not include a short tab id")
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", tab, "--json")
	})

	runE2EJSON(t, env, "network", "clear", "--tab", tab, "--json")
	runE2EJSON(t, env, "open", baseURL+"/redirect/start", "--tab", tab, "--wait-for", "#redirect-ready", "--timeout", "10000", "--json")
	if finalURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; finalURL != baseURL+"/redirect/final" {
		t.Fatalf("redirect final URL = %q", finalURL)
	}
	if title := runE2EJSON(t, env, "get", "title", "--tab", tab, "--json").Data.Value; title != "E2E Redirect Final" {
		t.Fatalf("redirect final title = %q", title)
	}

	requests := runE2EJSON(t, env, "network", "requests", "--filter", "/redirect/", "--tab", tab, "--json").Data.NetworkRequests
	wantRedirects := map[string]struct {
		status   int
		location string
	}{
		baseURL + "/redirect/start":  {status: http.StatusFound, location: "/redirect/middle"},
		baseURL + "/redirect/middle": {status: http.StatusTemporaryRedirect, location: "/redirect/final"},
	}
	for url, want := range wantRedirects {
		found := false
		for _, request := range requests {
			if request.URL != url {
				continue
			}
			found = true
			location := ""
			for name, value := range request.ResponseHeaders {
				if strings.EqualFold(name, "Location") {
					location = value
				}
			}
			if request.Status == nil || *request.Status != want.status || location != want.location {
				t.Fatalf("redirect network record for %s = %+v, want status %d location %q", url, request, want.status, want.location)
			}
		}
		if !found {
			t.Fatalf("redirect network records missing %s: %+v", url, requests)
		}
	}

	runE2EJSON(t, env, "back", "--tab", tab, "--wait-for", "#ready", "--timeout", "5000", "--json")
	if backURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; backURL != baseURL+"/" {
		t.Fatalf("back from redirect URL = %q, want home", backURL)
	}
	runE2EJSON(t, env, "forward", "--tab", tab, "--wait-for", "#redirect-ready", "--timeout", "10000", "--json")
	if forwardURL := runE2EJSON(t, env, "get", "url", "--tab", tab, "--json").Data.Value; forwardURL != baseURL+"/redirect/final" {
		t.Fatalf("forward through redirect URL = %q, want final URL", forwardURL)
	}
}

func TestE2ECLIURLFidelity(t *testing.T) {
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

	token := "e2e-url-fidelity-token"
	env, serverURL := startE2EServer(t, home, token)
	baseURL := site.URL()
	exactURL := baseURL + "/url-fidelity?case=exact"
	queryURL := baseURL + "/url-fidelity/%E8%B7%AF%E5%BE%91?case=query&name=%E6%B8%AC%E8%A9%A6+%F0%9F%9A%80&reserved=%26%3D%2F%3F"
	fragmentURL := queryURL + "#%E7%89%87%E6%AE%B5-%F0%9F%8C%9F"

	exactTab := runE2EJSON(t, env, "open", exactURL, "--new", "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	if exactTab == "" {
		t.Fatal("exact URL open response did not include a short tab id")
	}
	runE2EJSON(t, env, "eval", `window.__borzURLReuseSentinel = "preserved"`, "--tab", exactTab, "--json")
	reused := runE2EJSON(t, env, "open", exactURL, "--wait-for", "#url-fidelity-ready", "--timeout", "5000", "--json")
	if reused.Data.Tab != exactTab {
		t.Fatalf("exact URL open selected tab %q, want %q", reused.Data.Tab, exactTab)
	}
	if sentinel := runE2EJSON(t, env, "eval", `window.__borzURLReuseSentinel`, "--tab", exactTab, "--json").Data.Result; sentinel != "preserved" {
		t.Fatalf("exact URL reuse reloaded page state: result=%#v", sentinel)
	}

	queryTab := runE2EJSON(t, env, "open", queryURL, "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	fragmentTab := runE2EJSON(t, env, "open", fragmentURL, "--wait-for", "#url-fidelity-ready", "--timeout", "10000", "--json").Data.Tab
	if queryTab == "" || fragmentTab == "" || queryTab == exactTab || fragmentTab == exactTab || fragmentTab == queryTab {
		t.Fatalf("query and fragment URLs did not create distinct tabs: exact=%q query=%q fragment=%q", exactTab, queryTab, fragmentTab)
	}
	for _, tab := range []string{exactTab, queryTab, fragmentTab} {
		tab := tab
		t.Cleanup(func() {
			runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
		})
	}

	for _, tc := range []struct {
		tab string
		url string
	}{
		{tab: exactTab, url: exactURL},
		{tab: queryTab, url: queryURL},
		{tab: fragmentTab, url: fragmentURL},
	} {
		if got := runE2EJSON(t, env, "get", "url", "--tab", tc.tab, "--json").Data.Value; got != tc.url {
			t.Fatalf("CLI get url for tab %s = %q, want %q", tc.tab, got, tc.url)
		}
	}

	if got := runE2EJSON(t, env, "eval", `document.querySelector("#url-unicode-param").textContent`, "--tab", queryTab, "--json").Data.Result; got != "測試 🚀" {
		t.Fatalf("decoded Unicode query parameter = %#v", got)
	}
	if got := runE2EJSON(t, env, "eval", `document.querySelector("#url-reserved-param").textContent`, "--tab", queryTab, "--json").Data.Result; got != "&=/?" {
		t.Fatalf("decoded reserved query parameter = %#v", got)
	}

	restURL := runE2ERESTJSON(t, serverURL, token, "/v1/get", map[string]interface{}{
		"attribute": "url", "tab": fragmentTab,
	})
	if restURL.Data.Tab != fragmentTab || restURL.Data.Value != fragmentURL {
		t.Fatalf("REST get url = tab %q value %q, want tab %q value %q", restURL.Data.Tab, restURL.Data.Value, fragmentTab, fragmentURL)
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

func TestE2ECLIPerActionTabTargeting(t *testing.T) {
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
	first := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	second := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if first == "" || second == "" || first == second {
		t.Fatalf("per-action fixtures are not distinct: first=%q second=%q", first, second)
	}
	t.Cleanup(func() {
		runE2ECLI(t, env, "close", "--tab", first, "--json")
		runE2ECLI(t, env, "close", "--tab", second, "--json")
	})

	requireTab := func(operation string, resp protocol.Response, want string) {
		t.Helper()
		if resp.Data.Tab != want {
			t.Fatalf("%s response tab = %q, want %q", operation, resp.Data.Tab, want)
		}
	}
	requireSecondActive := func(stage string) {
		t.Helper()
		tabs := runE2EJSON(t, env, "tab", "list", "--json").Data.Tabs
		active := ""
		for _, tab := range tabs {
			if tab.Active {
				active = tab.Tab
				break
			}
		}
		if active != second {
			t.Fatalf("active tab after %s = %q, want second fixture %q; tabs=%+v", stage, active, second, tabs)
		}
	}

	firstSetup := runE2EJSON(t, env, "eval", `document.querySelector("#click-button").setAttribute("aria-label", "First tab action"); document.querySelector("#clicked-result").textContent = "first idle"; true`, "--tab", first, "--json")
	requireTab("first eval", firstSetup, first)
	secondSetup := runE2EJSON(t, env, "eval", `document.querySelector("#click-button").setAttribute("aria-label", "Second tab action"); document.querySelector("#clicked-result").textContent = "second idle"; true`, "--tab", second, "--json")
	requireTab("second eval", secondSetup, second)
	requireSecondActive("targeted evals")

	firstSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", first, "--json")
	requireTab("first snapshot", firstSnapshot, first)
	firstRef := refByName(t, firstSnapshot.Data.SnapshotData, "First tab action")
	secondSnapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", second, "--json")
	requireTab("second snapshot", secondSnapshot, second)
	secondRef := refByName(t, secondSnapshot.Data.SnapshotData, "Second tab action")
	requireSecondActive("targeted snapshots")

	firstGet := runE2EJSON(t, env, "get", "text", firstRef, "--tab", first, "--json")
	requireTab("first get", firstGet, first)
	if firstGet.Data.Value != "Click me" {
		t.Fatalf("first get text = %q, want Click me", firstGet.Data.Value)
	}
	secondGet := runE2EJSON(t, env, "get", "text", secondRef, "--tab", second, "--json")
	requireTab("second get", secondGet, second)
	if secondGet.Data.Value != "Click me" {
		t.Fatalf("second get text = %q, want Click me", secondGet.Data.Value)
	}
	requireSecondActive("targeted gets")

	firstClick := runE2EJSON(t, env, "click", firstRef, "--tab", first, "--json")
	requireTab("first click", firstClick, first)
	firstResult := runE2EJSON(t, env, "eval", `document.querySelector("#clicked-result").textContent`, "--tab", first, "--json")
	requireTab("first result eval", firstResult, first)
	if firstResult.Data.Result != "clicked 1" {
		t.Fatalf("first action result = %#v, want clicked 1", firstResult.Data.Result)
	}
	secondBeforeClick := runE2EJSON(t, env, "eval", `document.querySelector("#clicked-result").textContent`, "--tab", second, "--json")
	requireTab("second isolation eval", secondBeforeClick, second)
	if secondBeforeClick.Data.Result != "second idle" {
		t.Fatalf("first action affected second tab: result=%#v", secondBeforeClick.Data.Result)
	}

	secondClick := runE2EJSON(t, env, "click", secondRef, "--tab", second, "--json")
	requireTab("second click", secondClick, second)
	secondResult := runE2EJSON(t, env, "eval", `document.querySelector("#clicked-result").textContent`, "--tab", second, "--json")
	requireTab("second result eval", secondResult, second)
	if secondResult.Data.Result != "clicked 1" {
		t.Fatalf("second action result = %#v, want clicked 1", secondResult.Data.Result)
	}
	requireSecondActive("all explicitly targeted actions")
}

func TestE2EDaemonReconnectRecovery(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	profile := "e2e-recovery"
	bin := filepath.Join(t.TempDir(), "borz")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build borz e2e binary: %v\n%s", err, out)
	}

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	cdpURL := fmt.Sprintf("http://%s:%d", ep.Host, ep.Port)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	runJSON := func(args ...string) protocol.Response {
		t.Helper()
		out := run(args...)
		var resp protocol.Response
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !resp.Success || resp.Data == nil {
			t.Fatalf("borz %s returned unsuccessful response: %+v", strings.Join(args, " "), resp)
		}
		return resp
	}
	daemonPath := filepath.Join(home, "profiles", profile, "daemon.json")
	readDaemon := func() protocol.DaemonInfo {
		t.Helper()
		raw, err := os.ReadFile(daemonPath)
		if err != nil {
			t.Fatalf("read isolated daemon state: %v", err)
		}
		var info protocol.DaemonInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("decode isolated daemon state: %v", err)
		}
		return info
	}

	var tab string
	t.Cleanup(func() {
		if tab != "" {
			cmd := exec.Command(bin, "close", "--profile", profile, "--tab", tab, "--json")
			cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
			_ = cmd.Run()
		}
		if raw, err := os.ReadFile(daemonPath); err == nil {
			var info protocol.DaemonInfo
			_ = json.Unmarshal(raw, &info)
			cmd := exec.Command(bin, "daemon", "shutdown", "--profile", profile)
			cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
			_ = cmd.Run()
			if info.PID > 0 && !client.WaitForProcessExit(info.PID, 3*time.Second) {
				if process, findErr := os.FindProcess(info.PID); findErr == nil {
					_ = process.Kill()
				}
			}
		}
	})

	opened := runJSON("open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--profile", profile, "--json")
	tab = opened.Data.Tab
	if tab == "" {
		t.Fatalf("initial browser work returned no tab: %+v", opened.Data)
	}
	requireEvalStringFromResponse(t, runJSON("eval", `document.title`, "--tab", tab, "--profile", profile, "--json"), "E2E Verify Home")
	first := readDaemon()
	if first.PID <= 0 || !client.IsProcessAlive(first.PID) {
		t.Fatalf("initial isolated daemon is not alive: %+v", first)
	}

	requireContains(t, run("daemon", "shutdown", "--profile", profile), "Daemon stopped", "daemon shutdown")
	if !client.WaitForProcessExit(first.PID, 5*time.Second) {
		t.Fatalf("initial daemon pid %d did not exit after clean shutdown", first.PID)
	}
	if _, err := os.Stat(daemonPath); !os.IsNotExist(err) {
		t.Fatalf("daemon state remained after shutdown: %v", err)
	}

	snapshot := runJSON("snapshot", "-i", "--tab", tab, "--profile", profile, "--json")
	second := readDaemon()
	if second.PID <= 0 || second.PID == first.PID || !client.IsProcessAlive(second.PID) {
		t.Fatalf("CLI action did not start a distinct healthy daemon: first=%+v second=%+v", first, second)
	}
	status := run("daemon", "status", "--profile", profile)
	requireContains(t, status, `"running": true`, "restarted daemon status")
	requireContains(t, status, `"cdpConnected": true`, "restarted daemon status")

	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	runJSON("click", clickRef, "--tab", tab, "--profile", profile, "--json")
	requireEvalStringFromResponse(t, runJSON("eval", `document.querySelector("#clicked-result").textContent`, "--tab", tab, "--profile", profile, "--json"), "clicked 1")

	runJSON("close", "--tab", tab, "--profile", profile, "--json")
	tab = ""
	requireContains(t, run("daemon", "shutdown", "--profile", profile), "Daemon stopped", "restarted daemon shutdown")
	if !client.WaitForProcessExit(second.PID, 5*time.Second) {
		t.Fatalf("restarted daemon pid %d leaked after shutdown", second.PID)
	}
}

func TestE2ENamedProfileIsolation(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "borz")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build borz e2e binary: %v\n%s", err, out)
	}

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	cdpURL := fmt.Sprintf("http://%s:%d", ep.Host, ep.Port)
	profiles := []string{"e2e-isolation-a", "e2e-isolation-b"}
	run := func(profile string, args ...string) string {
		t.Helper()
		args = append(args, "--profile", profile)
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	runJSON := func(profile string, args ...string) protocol.Response {
		t.Helper()
		out := run(profile, args...)
		var resp protocol.Response
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !resp.Success || resp.Data == nil {
			t.Fatalf("borz %s returned unsuccessful response: %+v", strings.Join(args, " "), resp)
		}
		return resp
	}
	daemonPath := func(profile string) string {
		return filepath.Join(home, "profiles", profile, "daemon.json")
	}
	readDaemon := func(profile string) protocol.DaemonInfo {
		t.Helper()
		raw, err := os.ReadFile(daemonPath(profile))
		if err != nil {
			t.Fatalf("read daemon state for profile %q: %v", profile, err)
		}
		var info protocol.DaemonInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("decode daemon state for profile %q: %v", profile, err)
		}
		return info
	}

	tabs := make(map[string]string)
	t.Cleanup(func() {
		for _, profile := range profiles {
			if tab := tabs[profile]; tab != "" {
				cmd := exec.Command(bin, "close", "--tab", tab, "--json", "--profile", profile)
				cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
				_ = cmd.Run()
			}
			if raw, readErr := os.ReadFile(daemonPath(profile)); readErr == nil {
				var info protocol.DaemonInfo
				_ = json.Unmarshal(raw, &info)
				cmd := exec.Command(bin, "daemon", "shutdown", "--profile", profile)
				cmd.Env = append(os.Environ(), "BORZ_HOME="+home, "BORZ_CDP_URL="+cdpURL)
				_ = cmd.Run()
				if info.PID > 0 && !client.WaitForProcessExit(info.PID, 3*time.Second) {
					if process, findErr := os.FindProcess(info.PID); findErr == nil {
						_ = process.Kill()
					}
				}
			}
		}
	})

	tabs[profiles[0]] = runJSON(profiles[0], "open", site.URL()+"/spa", "--new", "--wait-for", `#spa-ready[data-route="home"]`, "--timeout", "10000", "--json").Data.Tab
	tabs[profiles[1]] = runJSON(profiles[1], "open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json").Data.Tab
	if tabs[profiles[0]] == "" || tabs[profiles[1]] == "" || tabs[profiles[0]] == tabs[profiles[1]] {
		t.Fatalf("profile tabs are not distinct: %+v", tabs)
	}

	firstDaemon := readDaemon(profiles[0])
	secondDaemon := readDaemon(profiles[1])
	if firstDaemon.PID <= 0 || secondDaemon.PID <= 0 || firstDaemon.PID == secondDaemon.PID || firstDaemon.Port == secondDaemon.Port || firstDaemon.Token == secondDaemon.Token {
		t.Fatalf("profile daemon metadata is not isolated: first=%+v second=%+v", firstDaemon, secondDaemon)
	}

	runJSON(profiles[0], "eval", `window.__borzProfileMarker = "profile-a"`, "--json")
	runJSON(profiles[1], "eval", `window.__borzProfileMarker = "profile-b"`, "--json")
	requireEvalStringFromResponse(t, runJSON(profiles[0], "eval", `window.__borzProfileMarker + ":" + document.title`, "--json"), "profile-a:E2E SPA Home")
	requireEvalStringFromResponse(t, runJSON(profiles[1], "eval", `window.__borzProfileMarker + ":" + document.title`, "--json"), "profile-b:E2E Verify Page Two")

	firstBaseline := runJSON(profiles[0], "snapshot", "--diff", "--json").Data.SnapshotDiffData
	secondBaseline := runJSON(profiles[1], "snapshot", "--diff", "--json").Data.SnapshotDiffData
	if firstBaseline == nil || !firstBaseline.BaselineReset || secondBaseline == nil || !secondBaseline.BaselineReset {
		t.Fatalf("profiles did not establish independent diff baselines: first=%+v second=%+v", firstBaseline, secondBaseline)
	}
	runJSON(profiles[0], "eval", `document.querySelector('[data-spa-route="details"]').setAttribute("aria-disabled", "true")`, "--json")
	firstDiff := runJSON(profiles[0], "snapshot", "--diff", "--json").Data.SnapshotDiffData
	if firstDiff == nil || firstDiff.BaselineReset || len(firstDiff.Added) != 0 || len(firstDiff.Removed) != 0 || len(firstDiff.Changed) != 1 {
		t.Fatalf("first profile mutation diff = %+v, want exactly one changed node", firstDiff)
	}
	secondUnchanged := runJSON(profiles[1], "snapshot", "--diff", "--json").Data.SnapshotDiffData
	if secondUnchanged == nil || secondUnchanged.BaselineReset || secondUnchanged.Stats.Added != 0 || secondUnchanged.Stats.Removed != 0 || secondUnchanged.Stats.Changed != 0 {
		t.Fatalf("first profile disturbed second profile baseline: %+v", secondUnchanged)
	}

	runJSON(profiles[0], "close", "--tab", tabs[profiles[0]], "--json")
	tabs[profiles[0]] = ""
	requireContains(t, run(profiles[0], "daemon", "shutdown"), "Daemon stopped", "first profile shutdown")
	if !client.WaitForProcessExit(firstDaemon.PID, 5*time.Second) {
		t.Fatalf("first profile daemon pid %d leaked after shutdown", firstDaemon.PID)
	}
	if _, err := os.Stat(daemonPath(profiles[0])); !os.IsNotExist(err) {
		t.Fatalf("first profile daemon state remained after shutdown: %v", err)
	}
	if !client.IsProcessAlive(secondDaemon.PID) {
		t.Fatalf("second profile daemon pid %d exited with first profile", secondDaemon.PID)
	}
	requireEvalStringFromResponse(t, runJSON(profiles[1], "eval", `window.__borzProfileMarker + ":" + document.title`, "--json"), "profile-b:E2E Verify Page Two")

	runJSON(profiles[1], "close", "--tab", tabs[profiles[1]], "--json")
	tabs[profiles[1]] = ""
	requireContains(t, run(profiles[1], "daemon", "shutdown"), "Daemon stopped", "second profile shutdown")
	if !client.WaitForProcessExit(secondDaemon.PID, 5*time.Second) {
		t.Fatalf("second profile daemon pid %d leaked after shutdown", secondDaemon.PID)
	}
	if _, err := os.Stat(daemonPath(profiles[1])); !os.IsNotExist(err) {
		t.Fatalf("second profile daemon state remained after shutdown: %v", err)
	}
}

func TestE2EIdleTabReaper(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	profile := "e2e-idle-reaper"
	bin := filepath.Join(t.TempDir(), "borz")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build borz e2e binary: %v\n%s", err, out)
	}

	site, err := e2everify.Start("")
	if err != nil {
		t.Fatalf("start e2e verify site: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = site.Close(ctx)
	})

	baseEnv := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "BORZ_CDP_URL=") || strings.HasPrefix(entry, "BB_BROWSER_CDP_URL=") {
			continue
		}
		baseEnv = append(baseEnv, entry)
	}
	baseEnv = append(baseEnv,
		"BORZ_HOME="+home,
		"BORZ_E2E=1",
		"BORZ_TAB_IDLE_TIMEOUT=1",
		"BORZ_E2E_IDLE_TAB_THRESHOLD=3s",
		"BORZ_E2E_IDLE_TAB_TICK=100ms",
	)
	run := func(args ...string) string {
		t.Helper()
		args = append(args, "--profile", profile)
		cmd := exec.Command(bin, args...)
		cmd.Env = baseEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("borz %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	runJSON := func(args ...string) protocol.Response {
		t.Helper()
		out := run(args...)
		var resp protocol.Response
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("borz %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
		}
		if !resp.Success || resp.Data == nil {
			t.Fatalf("borz %s returned unsuccessful response: %+v", strings.Join(args, " "), resp)
		}
		return resp
	}

	daemonPath := filepath.Join(home, "profiles", profile, "daemon.json")
	portPath := filepath.Join(home, "profiles", profile, "browser", "cdp-port")
	browserPort := 0
	t.Cleanup(func() {
		if raw, readErr := os.ReadFile(daemonPath); readErr == nil {
			var info protocol.DaemonInfo
			_ = json.Unmarshal(raw, &info)
			cmd := exec.Command(bin, "daemon", "shutdown", "--profile", profile)
			cmd.Env = baseEnv
			_ = cmd.Run()
			if info.PID > 0 && !client.WaitForProcessExit(info.PID, 3*time.Second) {
				if process, findErr := os.FindProcess(info.PID); findErr == nil {
					_ = process.Kill()
				}
			}
		}
		if browserPort == 0 {
			if raw, readErr := os.ReadFile(portPath); readErr == nil {
				browserPort, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			}
		}
		if browserPort > 0 {
			closeE2EBrowser(t, browserPort)
		}
	})

	idle := runJSON("open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	active := runJSON("open", site.URL()+"/page2", "--new", "--wait-for", "#page-two-ready", "--timeout", "10000", "--json").Data.Tab
	blank := runJSON("tab", "new", "--json").Data.Tab
	if idle == "" || active == "" || blank == "" {
		t.Fatalf("reaper tabs missing ids: idle=%q active=%q blank=%q", idle, active, blank)
	}
	portRaw, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read isolated browser port: %v", err)
	}
	browserPort, err = strconv.Atoi(strings.TrimSpace(string(portRaw)))
	if err != nil || browserPort <= 0 {
		t.Fatalf("invalid isolated browser port %q: %v", portRaw, err)
	}

	// Keep the background blank tab fresh without changing the active fixture tab.
	time.Sleep(1500 * time.Millisecond)
	blankEval := runJSON("eval", "location.href", "--tab", blank, "--json")
	if blankEval.Data.Result != "about:blank" {
		t.Fatalf("blank tab URL = %#v, want about:blank", blankEval.Data.Result)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		tabs := runJSON("tab", "list", "--json").Data.Tabs
		present := make(map[string]protocol.TabInfo, len(tabs))
		for _, tab := range tabs {
			present[tab.Tab] = tab
		}
		_, idlePresent := present[idle]
		activeTab, activePresent := present[active]
		blankTab, blankPresent := present[blank]
		if !idlePresent {
			if !activePresent || !activeTab.Active {
				t.Fatalf("active fixture tab was not protected: active=%q tabs=%+v", active, tabs)
			}
			if !blankPresent || blankTab.URL != "about:blank" {
				t.Fatalf("fresh blank tab was not preserved: blank=%q tabs=%+v", blank, tabs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle fixture tab %q was not reaped; tabs=%+v", idle, tabs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func closeE2EBrowser(t *testing.T, port int) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		t.Errorf("close isolated browser: read version endpoint: %v", err)
		return
	}
	defer resp.Body.Close()
	var versionInfo struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil || versionInfo.WebSocketDebuggerURL == "" {
		t.Errorf("close isolated browser: decode version endpoint: %v", err)
		return
	}
	conn, _, err := websocket.DefaultDialer.Dial(versionInfo.WebSocketDebuggerURL, nil)
	if err != nil {
		t.Errorf("close isolated browser: connect CDP: %v", err)
		return
	}
	if err := conn.WriteJSON(map[string]interface{}{"id": 1, "method": "Browser.close"}); err != nil {
		_ = conn.Close()
		t.Errorf("close isolated browser: send Browser.close: %v", err)
		return
	}
	_ = conn.Close()

	deadline := time.Now().Add(3 * time.Second)
	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	for time.Now().Before(deadline) {
		check, checkErr := http.Get(versionURL)
		if checkErr != nil {
			return
		}
		_ = check.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("close isolated browser: CDP endpoint on port %d remained reachable", port)
}

func requireEvalStringFromResponse(t *testing.T, resp protocol.Response, want string) {
	t.Helper()
	if resp.Data == nil {
		t.Fatalf("eval result had no data, want %q", want)
	}
	got, ok := resp.Data.Result.(string)
	if !ok || got != want {
		t.Fatalf("eval result = %#v, want %q", resp.Data.Result, want)
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

func TestE2ERESTWaitAndDelaySemantics(t *testing.T) {
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

	token := "e2e-rest-delay-token"
	_, serverURL := startE2EServer(t, home, token)
	opened := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/async-action", "new": true,
		"waitFor": "#async-action-ready", "timeoutMs": 10000,
	})
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("REST delay open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})

	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": tab,
	})
	actionRef := refByName(t, snapshot.Data.SnapshotData, "Start async action")

	const delayMs = 700
	started := time.Now()
	runE2ERESTJSON(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": actionRef, "tab": tab,
		"waitFor": "#async-action-result", "timeoutMs": 5000,
		"preDelayMs": delayMs, "postDelayMs": delayMs,
	})
	if elapsed := time.Since(started); elapsed < 1800*time.Millisecond {
		t.Fatalf("REST click with pre/wait/post delays returned after %s, want at least 1.8s", elapsed)
	}
	result := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#async-action-result").textContent`, "tab": tab,
	})
	if result.Data.Result != "Async action 1 complete" {
		t.Fatalf("REST delayed action result = %#v", result.Data.Result)
	}

	const failedPostDelayMs = 5000
	started = time.Now()
	status, failed := runE2ERESTJSONResponse(t, serverURL, token, "/v1/click", map[string]interface{}{
		"ref": actionRef, "tab": tab,
		"waitFor": "#never-rendered-after-rest-action", "timeoutMs": 600,
		"postDelayMs": failedPostDelayMs,
	})
	failedElapsed := time.Since(started)
	if status != http.StatusBadRequest || failed.Success || failed.ID == "" {
		t.Fatalf("REST timed-out action = status %d, response %+v", status, failed)
	}
	requireContains(t, failed.Error, `wait-for selector "#never-rendered-after-rest-action"`, "REST action timeout error")
	requireContains(t, failed.Error, "timeout after 600ms", "REST action timeout error")
	if failedElapsed >= 3500*time.Millisecond {
		t.Fatalf("failed REST action took %s with %dms post-delay; post-delay should be skipped", failedElapsed, failedPostDelayMs)
	}
	cleared := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `document.querySelector("#async-action-result") === null`, "tab": tab,
	})
	if cleared.Data.Result != true {
		t.Fatalf("REST timed-out action did not run before its wait failed: result %#v", cleared.Data.Result)
	}

	for field, value := range map[string]int{"timeoutMs": -1, "preDelayMs": -1, "postDelayMs": -1} {
		status, invalid := runE2ERESTJSONResponse(t, serverURL, token, "/v1/click", map[string]interface{}{
			"ref": actionRef, "tab": tab, field: value,
		})
		if status != http.StatusBadRequest || invalid.Error != field+" must be a non-negative integer" {
			t.Errorf("REST %s lower bound = status %d, response %+v", field, status, invalid)
		}
	}
}

func TestE2ERESTMalformedRequests(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)

	token := "e2e-rest-malformed-token"
	_, serverURL := startE2EServer(t, home, token)

	request := func(method, path, bearer, contentType, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, serverURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, raw
	}

	assertError := func(name, method, path, bearer, contentType, body string, wantStatus int, wantError string) {
		t.Helper()
		resp, raw := request(method, path, bearer, contentType, body)
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s status = %d, want %d; body=%s", name, resp.StatusCode, wantStatus, raw)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("%s Content-Type = %q, want application/json", name, got)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("%s CORS origin = %q, want *", name, got)
		}
		var envelope protocol.Response
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode %s envelope: %v\n%s", name, err, raw)
		}
		if envelope.ID == "" || envelope.Success || envelope.Data != nil || !strings.Contains(envelope.Error, wantError) {
			t.Fatalf("%s envelope = %+v, want id, success=false, error containing %q", name, envelope, wantError)
		}
	}

	assertError("invalid JSON", http.MethodPost, "/v1/snapshot", token, "text/plain", `{not-json`, http.StatusBadRequest, "invalid JSON")
	assertError("missing required field with ignored unknown field", http.MethodPost, "/v1/upload", token, "application/json", `{"unknownField":true}`, http.StatusBadRequest, "files (or file) is required")
	assertError("wrong method", http.MethodGet, "/v1/snapshot", token, "", "", http.StatusMethodNotAllowed, "Method not allowed")
	assertError("bad bearer token", http.MethodPost, "/v1/snapshot", "wrong-token", "application/json", `{}`, http.StatusUnauthorized, "Unauthorized")
	assertError("unknown route", http.MethodPost, "/v1/does-not-exist", token, "application/json", `{}`, http.StatusNotFound, "Not found")

	wrongMethod, _ := request(http.MethodGet, "/v1/snapshot", token, "", "")
	if got := wrongMethod.Header.Get("Allow"); got != http.MethodPost {
		t.Fatalf("wrong-method Allow = %q, want POST", got)
	}
	badToken, _ := request(http.MethodPost, "/v1/snapshot", "wrong-token", "application/json", `{}`)
	if got := badToken.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("bad-token WWW-Authenticate = %q, want Bearer", got)
	}

	preflight, raw := request(http.MethodOptions, "/v1/snapshot", "", "", "")
	if preflight.StatusCode != http.StatusNoContent || len(raw) != 0 {
		t.Fatalf("CORS preflight = status %d body %q, want 204 with empty body", preflight.StatusCode, raw)
	}
	if preflight.Header.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(preflight.Header.Get("Access-Control-Allow-Methods"), http.MethodPost) ||
		!strings.Contains(preflight.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("CORS preflight headers = %v", preflight.Header)
	}
}

func TestE2ERESTOpenAPIConformance(t *testing.T) {
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

	token := "e2e-openapi-conformance-token"
	_, serverURL := startE2EServer(t, home, token)

	request := func(method, path, bearer string, body io.Reader) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, serverURL+path, body)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, raw
	}

	specResp, specBody := request(http.MethodGet, "/openapi.yaml", "", nil)
	if specResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", specResp.StatusCode)
	}
	operations := parseE2EOpenAPIOperations(t, specBody)

	paths := make([]string, 0, len(operations))
	for path := range operations {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		methods := operations[path]
		t.Run("route_"+strings.Trim(strings.ReplaceAll(path, "/", "_"), "_"), func(t *testing.T) {
			wrongMethod, _ := request(http.MethodDelete, path, token, nil)
			if wrongMethod.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("DELETE %s status = %d, want 405", path, wrongMethod.StatusCode)
			}
			gotMethods := strings.Split(wrongMethod.Header.Get("Allow"), ", ")
			wantMethods := make([]string, 0, len(methods))
			for method := range methods {
				wantMethods = append(wantMethods, method)
			}
			slices.Sort(wantMethods)
			if gotMethods[0] != "" {
				slices.Sort(gotMethods)
			}
			if gotMethods[0] != "" && !slices.Equal(gotMethods, wantMethods) {
				t.Fatalf("%s Allow = %v, OpenAPI documents %v", path, gotMethods, wantMethods)
			}

			method := wantMethods[0]
			var body io.Reader
			if method != http.MethodGet {
				body = strings.NewReader(`{}`)
			}
			unauthorized, _ := request(method, path, "", body)
			if methods[method] {
				if unauthorized.StatusCode != http.StatusUnauthorized {
					t.Fatalf("unauthenticated %s %s status = %d, want 401", method, path, unauthorized.StatusCode)
				}
			} else if unauthorized.StatusCode == http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s %s returned 401, but OpenAPI declares security: []", method, path)
			}
		})
	}

	statusResp, statusBody := request(http.MethodGet, "/status", token, nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /status = %d: %s", statusResp.StatusCode, statusBody)
	}
	tabsResp, tabsBody := request(http.MethodGet, "/v1/tabs", token, nil)
	if tabsResp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /v1/tabs = %d: %s", tabsResp.StatusCode, tabsBody)
	}

	opened := runE2ERESTJSON(t, serverURL, token, "/v1/open", map[string]interface{}{
		"url": site.URL() + "/", "new": true, "waitFor": "#ready", "timeoutMs": 10000,
	})
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("OpenAPI smoke open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})
	snapshot := runE2ERESTJSON(t, serverURL, token, "/v1/snapshot", map[string]interface{}{
		"interactive": true, "tab": tab,
	})
	if snapshot.Data.Tab != tab || snapshot.Data.SnapshotData == nil {
		t.Fatalf("OpenAPI smoke snapshot = %+v, want tab %q with snapshot data", snapshot.Data, tab)
	}
	title := runE2ERESTJSON(t, serverURL, token, "/v1/get", map[string]interface{}{
		"attribute": "title", "tab": tab,
	})
	if title.Data.Tab != tab || title.Data.Value != "E2E Verify Home" {
		t.Fatalf("OpenAPI smoke get = tab %q value %q, want tab %q title %q", title.Data.Tab, title.Data.Value, tab, "E2E Verify Home")
	}
}

// parseE2EOpenAPIOperations extracts the path, method, and effective security
// from borz's deliberately conventional OpenAPI YAML formatting. Keeping this
// parser test-local avoids adding a production YAML dependency for one E2E.
func parseE2EOpenAPIOperations(t *testing.T, spec []byte) map[string]map[string]bool {
	t.Helper()
	operations := make(map[string]map[string]bool)
	globalBearer := false
	inGlobalSecurity := false
	inPaths := false
	currentPath := ""
	currentMethod := ""

	for _, line := range strings.Split(string(spec), "\n") {
		switch {
		case line == "security:":
			inGlobalSecurity = true
		case inGlobalSecurity && strings.HasPrefix(line, "  - bearerAuth:"):
			globalBearer = true
		case inGlobalSecurity && strings.TrimSpace(line) == "- {}":
			t.Fatal("OpenAPI global security permits anonymous access, but token servers require bearer auth")
		case inGlobalSecurity && line != "" && !strings.HasPrefix(line, " "):
			inGlobalSecurity = false
		}

		if line == "paths:" {
			inPaths = true
			continue
		}
		if !inPaths {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			currentPath = strings.TrimSuffix(strings.TrimSpace(line), ":")
			operations[currentPath] = make(map[string]bool)
			currentMethod = ""
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") && strings.HasSuffix(trimmed, ":") {
			method := strings.ToUpper(strings.TrimSuffix(trimmed, ":"))
			if slices.Contains([]string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, method) {
				currentMethod = method
				operations[currentPath][currentMethod] = globalBearer
			}
			continue
		}
		if currentPath != "" && currentMethod != "" && line == "      security: []" {
			operations[currentPath][currentMethod] = false
		}
	}

	if !globalBearer {
		t.Fatal("OpenAPI document is missing global bearerAuth security")
	}
	if len(operations) == 0 {
		t.Fatal("OpenAPI document did not contain any operations")
	}
	for path, methods := range operations {
		if len(methods) == 0 {
			t.Fatalf("OpenAPI path %s has no supported HTTP operations", path)
		}
	}
	return operations
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

func TestE2ECLIEvalOptions(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	scriptPath := filepath.Join(t.TempDir(), "eval-options.js")
	script := `window.__borzEvalState = {
  unicode: user.name,
  nested: payload.levels[0].values[1],
  emptyString,
  emptyListLength: emptyList.length,
  emptyObjectKeys: Object.keys(emptyObject).length,
  nullValue
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write eval script: %v", err)
	}

	fileResp := runE2EJSON(t, env,
		"eval", "--file", scriptPath,
		"--json-arg", `user={"name":"Unicode 雪人 ☃️"}`,
		"--json-arg", `payload={"levels":[{"values":["",42]}]}`,
		"--json-arg", `emptyString=""`,
		"--json-arg", `emptyList=[]`,
		"--json-arg", `emptyObject={}`,
		"--json-arg", `nullValue=null`,
		"--tab", tab, "--json",
	)
	result, ok := fileResp.Data.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("eval --file result = %#v", fileResp.Data.Result)
	}
	if result["unicode"] != "Unicode 雪人 ☃️" || result["nested"] != float64(42) ||
		result["emptyString"] != "" || result["emptyListLength"] != float64(0) ||
		result["emptyObjectKeys"] != float64(0) || result["nullValue"] != nil {
		t.Fatalf("eval --file JSON args result = %#v", result)
	}

	unwrapped := runE2ECLI(t, env, "eval", "window.__borzEvalState.unicode", "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(unwrapped); got != "Unicode 雪人 ☃️" {
		t.Fatalf("eval --unwrap = %q", got)
	}

	awaited := runE2ECLI(t, env, "eval", `await Promise.resolve(window.__borzEvalState.nested)`, "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(awaited); got != "42" {
		t.Fatalf("top-level await result = %q", got)
	}

	noAutoAwait := runE2EJSONResponse(t, env, "eval", `await Promise.resolve("should not run")`, "--no-auto-await", "--tab", tab, "--json")
	if noAutoAwait.Success {
		t.Fatalf("eval --no-auto-await unexpectedly succeeded: %+v", noAutoAwait)
	}
	requireContains(t, noAutoAwait.Error, "SyntaxError", "eval --no-auto-await error")
	requireContains(t, noAutoAwait.Error, "await", "eval --no-auto-await error")

	scriptError := runE2EJSONResponse(t, env, "eval", `(() => { throw new Error("e2e-eval-intentional") })()`, "--tab", tab, "--json")
	if scriptError.Success {
		t.Fatalf("throwing eval unexpectedly succeeded: %+v", scriptError)
	}
	requireContains(t, scriptError.Error, "Error: e2e-eval-intentional", "eval script error")

	requireEvalStringWithPrefix(t, env, []string{"--tab", tab}, "window.__borzEvalState.unicode", "Unicode 雪人 ☃️")
}

func TestE2ECLIOutputShaping(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	script := `({object: {label: "live", count: 2}, list: ["first", 7], string: "plain output"})`

	jsonOut := runE2ECLI(t, env, "eval", script, "--tab", tab, "--unwrap", "--json")
	var jsonResp protocol.Response
	if err := json.Unmarshal([]byte(jsonOut), &jsonResp); err != nil {
		t.Fatalf("eval --unwrap --json returned non-JSON output: %v\n%s", err, jsonOut)
	}
	result, ok := jsonResp.Data.Result.(map[string]interface{})
	if !ok || result["string"] != "plain output" {
		t.Fatalf("eval --unwrap --json did not preserve the response envelope: %#v", jsonResp.Data.Result)
	}

	objectOut := runE2ECLI(t, env, "eval", script+`.object`, "--tab", tab, "--unwrap")
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(objectOut), &object); err != nil {
		t.Fatalf("unwrapped object is not JSON: %v\n%s", err, objectOut)
	}
	if object["label"] != "live" || object["count"] != float64(2) {
		t.Fatalf("unwrapped object = %#v", object)
	}

	listOut := runE2ECLI(t, env, "eval", script+`.list`, "--tab", tab, "--unwrap")
	var list []interface{}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("unwrapped list is not JSON: %v\n%s", err, listOut)
	}
	if len(list) != 2 || list[0] != "first" || list[1] != float64(7) {
		t.Fatalf("unwrapped list = %#v", list)
	}

	stringOut := runE2ECLI(t, env, "eval", script+`.string`, "--tab", tab, "--unwrap")
	if got := strings.TrimSpace(stringOut); got != "plain output" {
		t.Fatalf("unwrapped string = %q", got)
	}

	jqOut := runE2ECLI(t, env, "eval", script, "--tab", tab, "--unwrap", "--json", "--jq", ".result.object")
	var jqObject map[string]interface{}
	if err := json.Unmarshal([]byte(jqOut), &jqObject); err != nil {
		t.Fatalf("jq object output is not JSON: %v\n%s", err, jqOut)
	}
	if jqObject["label"] != "live" || jqObject["count"] != float64(2) {
		t.Fatalf("jq object output = %#v", jqObject)
	}

	jqList := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.list")
	if err := json.Unmarshal([]byte(jqList), &list); err != nil || len(list) != 2 || list[0] != "first" || list[1] != float64(7) {
		t.Fatalf("jq list output = %q, parsed %#v, error %v", jqList, list, err)
	}

	jqString := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.string")
	if got := strings.TrimSpace(jqString); got != "plain output" {
		t.Fatalf("jq string output = %q", got)
	}

	missing := runE2ECLI(t, env, "eval", script, "--tab", tab, "--jq", ".result.missing")
	if strings.TrimSpace(missing) != "" {
		t.Fatalf("jq missing path output = %q, want empty", missing)
	}

	jqFailure := runE2ECLI(t, env, "eval", `(() => { throw new Error("e2e-output-shaping") })()`, "--tab", tab, "--jq", ".error")
	requireContains(t, jqFailure, "Error: e2e-output-shaping", "jq failure output")
}

func TestE2ECLISelectorAndRefErrors(t *testing.T) {
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
	tab := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json").Data.Tab
	if tab == "" {
		t.Fatal("selector/ref fixture open returned no tab")
	}
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	requireError := func(label string, response protocol.Response, fragments ...string) {
		t.Helper()
		if response.Success || response.ID == "" || response.Error == "" {
			t.Fatalf("%s response = %+v, want structured error", label, response)
		}
		for _, fragment := range fragments {
			if !strings.Contains(response.Error, fragment) {
				t.Fatalf("%s error %q missing %q", label, response.Error, fragment)
			}
		}
	}

	missingSelector := runE2EJSONResponse(t, env, "frame", "#missing-frame", "--tab", tab, "--json")
	requireError("missing selector", missingSelector, "iframe not found", "#missing-frame")

	invalidCSS := runE2EJSONResponse(t, env, "frame", "[", "--tab", tab, "--json")
	requireError("invalid CSS", invalidCSS, "invalid selector", "[")

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("selector/ref snapshot returned no data")
	}
	clickRef := refByName(t, snapshot, "Click counter")
	inputRef := refByName(t, snapshot, "E2E text input")
	selectRef := refByName(t, snapshot, "E2E color select")

	nonexistentRef := runE2EJSONResponse(t, env, "click", "e999999", "--tab", tab, "--json")
	requireError("nonexistent ref", nonexistentRef, "unknown ref: e999999", "Run snapshot first")

	wrongType := runE2EJSONResponse(t, env, "select", inputRef, "green", "--tab", tab, "--json")
	requireError("wrong element type", wrongType, "element is not a select")

	invalidValue := runE2EJSONResponse(t, env, "select", selectRef, "purple", "--tab", tab, "--json")
	requireError("invalid select value", invalidValue, "select value not found: purple")

	runE2EJSON(t, env, "open", baseURL+"/page2", "--tab", tab, "--wait-for", "#page-two-ready", "--timeout", "5000", "--json")
	staleRef := runE2EJSONResponse(t, env, "click", clickRef, "--tab", tab, "--json")
	requireError("stale ref", staleRef, "unknown ref: "+clickRef, "Run snapshot first")

	runE2EJSON(t, env, "open", baseURL+"/", "--tab", tab, "--wait-for", "#ready", "--timeout", "5000", "--json")
	refreshed := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	validClickRef := refByName(t, refreshed, "Click counter")
	runE2EJSON(t, env, "click", validClickRef, "--tab", tab, "--json")
	clicked := runE2EJSON(t, env, "eval", `document.querySelector("#clicked-result").textContent`, "--tab", tab, "--json")
	if clicked.Data.Result != "clicked 1" {
		t.Fatalf("valid action after selector/ref errors = %#v, want clicked 1", clicked.Data.Result)
	}
}

func TestE2ECLIActionIdempotencyAndStateTransitions(t *testing.T) {
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
		t.Fatal("action state fixture open returned no tab")
	}
	t.Cleanup(func() { runE2ECLI(t, env, "close", "--tab", tab, "--json") })

	snapshot := runE2EJSON(t, env, "snapshot", "-i", "--tab", tab, "--json").Data.SnapshotData
	if snapshot == nil {
		t.Fatal("action state snapshot returned no data")
	}
	clickRef := refByName(t, snapshot, "Click counter")
	hoverRef := refByName(t, snapshot, "Hover target")
	inputRef := refByName(t, snapshot, "E2E text input")
	submitRef := refByName(t, snapshot, "Submit form")
	checkRef := refByName(t, snapshot, "E2E checkbox")
	selectRef := refByName(t, snapshot, "E2E color select")

	assertState := func(label, script string) {
		t.Helper()
		resp := runE2EJSON(t, env, "eval", script, "--tab", tab, "--json")
		if ok, _ := resp.Data.Result.(bool); !ok {
			t.Fatalf("%s state check = %#v, want true", label, resp.Data.Result)
		}
	}

	for range 2 {
		runE2EJSON(t, env, "check", checkRef, "--tab", tab, "--json")
	}
	assertState("repeated check", `document.querySelector("#check-box").checked && document.querySelector("#checkbox-state").textContent === "checked"`)
	for range 2 {
		runE2EJSON(t, env, "uncheck", checkRef, "--tab", tab, "--json")
	}
	assertState("repeated uncheck", `!document.querySelector("#check-box").checked && document.querySelector("#checkbox-state").textContent === "unchecked"`)

	runE2EJSON(t, env, "fill", inputRef, "Unicode 測試 ☃️", "--tab", tab, "--json")
	assertState("Unicode fill", `document.querySelector("#text-input").value === "Unicode 測試 ☃️" && document.querySelector("#input-state").textContent === "Unicode 測試 ☃️"`)
	runE2EJSON(t, env, "fill", inputRef, "", "--tab", tab, "--json")
	assertState("empty fill", `document.querySelector("#text-input").value === "" && document.querySelector("#input-state").textContent === "empty"`)

	for range 2 {
		runE2EJSON(t, env, "select", selectRef, "green", "--tab", tab, "--json")
	}
	assertState("repeated select", `document.querySelector("#color-select").value === "green" && document.querySelector("#select-state").textContent === "green"`)

	for range 2 {
		runE2EJSON(t, env, "hover", hoverRef, "--tab", tab, "--json")
	}
	assertState("repeated hover", `document.querySelector("#hover-result").textContent === "hovered"`)
	for range 2 {
		runE2EJSON(t, env, "click", clickRef, "--tab", tab, "--json")
	}
	assertState("repeated click", `document.querySelector("#clicked-result").textContent === "clicked 2"`)

	runE2EJSON(t, env, "eval", `window.__borzSubmitCount = 0; document.querySelector("#text-form").addEventListener("submit", () => { window.__borzSubmitCount += 1; }); true`, "--tab", tab, "--json")
	runE2EJSON(t, env, "fill", inputRef, "submit 測試", "--tab", tab, "--json")
	for range 2 {
		runE2EJSON(t, env, "click", submitRef, "--tab", tab, "--json")
	}
	assertState("submission event count", `window.__borzSubmitCount === 2 && document.querySelector("#submit-result").textContent === "submitted submit 測試"`)
}

func TestE2ECLIFetchRequests(t *testing.T) {
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
	opened := runE2EJSON(t, env, "open", baseURL+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() { runE2EJSON(t, env, "close", "--tab", tab, "--json") })

	runE2EJSON(t, env, "eval", `document.cookie = "e2e_fetch_cookie=same-origin; Path=/; SameSite=Lax"`, "--tab", tab, "--json")
	fetchResult := func(path string, args ...string) map[string]interface{} {
		t.Helper()
		command := []string{"fetch", baseURL + path}
		command = append(command, args...)
		command = append(command, "--tab", tab, "--json")
		resp := runE2EJSON(t, env, command...)
		result, ok := resp.Data.Result.(map[string]interface{})
		if !ok {
			t.Fatalf("borz fetch %s result = %#v", path, resp.Data.Result)
		}
		return result
	}
	bodyOf := func(result map[string]interface{}) map[string]interface{} {
		t.Helper()
		body, ok := result["body"].(map[string]interface{})
		if !ok {
			t.Fatalf("fetch body = %#v", result)
		}
		return body
	}

	get := fetchResult("/api/fetch/get", "--header", "X-E2E-Header: get-value", "--header", "X-E2E-Second: second-value")
	getBody := bodyOf(get)
	if get["status"] != float64(http.StatusOK) || getBody["method"] != http.MethodGet || getBody["header"] != "get-value" || getBody["secondHeader"] != "second-value" {
		t.Fatalf("GET fetch result = %#v", get)
	}
	if cookie, _ := getBody["cookie"].(string); !strings.Contains(cookie, "e2e_fetch_cookie=same-origin") {
		t.Fatalf("GET fetch did not inherit same-origin cookie: %#v", getBody)
	}

	jsonPayload := `{"message":"Unicode 測試","nested":{"count":2}}`
	post := fetchResult("/api/fetch/post", "--method", "POST", "--header", "Content-Type: application/json", "--body", jsonPayload)
	postBody := bodyOf(post)
	decodedJSON, ok := postBody["jsonBody"].(map[string]interface{})
	if !ok || postBody["method"] != http.MethodPost || postBody["rawBody"] != jsonPayload || decodedJSON["message"] != "Unicode 測試" || decodedJSON["nested"].(map[string]interface{})["count"] != float64(2) {
		t.Fatalf("POST JSON fetch result = %#v", post)
	}

	formPayload := "name=borz+e2e&tag=one&tag=two"
	put := fetchResult("/api/fetch/put", "--method", "PUT", "--header", "Content-Type: application/x-www-form-urlencoded", "--body", formPayload)
	putBody := bodyOf(put)
	form, ok := putBody["formBody"].(map[string]interface{})
	if !ok || putBody["method"] != http.MethodPut || form["name"].([]interface{})[0] != "borz e2e" || len(form["tag"].([]interface{})) != 2 {
		t.Fatalf("PUT form fetch result = %#v", put)
	}

	non2xx := fetchResult("/api/fetch/status")
	if non2xx["status"] != float64(http.StatusUnprocessableEntity) || bodyOf(non2xx)["method"] != http.MethodGet {
		t.Fatalf("non-2xx fetch result = %#v", non2xx)
	}

	failure := fetchResult("/api/fetch/get", "--method", "TRACE")
	if message, ok := failure["error"].(string); !ok || strings.TrimSpace(message) == "" {
		t.Fatalf("failed fetch did not return a structured error: %#v", failure)
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

func TestE2EMCPMultiTabAgainstVerifySite(t *testing.T) {
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
	clientClosed := false
	t.Cleanup(func() {
		if !clientClosed {
			_ = mcpClient.Close()
		}
	})

	initRequest := mcpprotocol.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcpprotocol.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-multi-tab", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	firstURL := site.URL() + "/?mcp-tab=first"
	secondURL := site.URL() + "/page2?mcp-tab=second"
	firstNew := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_new", map[string]interface{}{"url": firstURL})
	requireContains(t, firstNew, "Opened new tab at "+firstURL, "first MCP tab-new result")
	secondNew := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_new", map[string]interface{}{"url": secondURL})
	requireContains(t, secondNew, "Opened new tab at "+secondURL, "second MCP tab-new result")

	tabIDForURL := func(tabList, url string) string {
		t.Helper()
		for _, line := range strings.Split(tabList, "\n") {
			if !strings.Contains(line, url) {
				continue
			}
			_, after, ok := strings.Cut(line, "(tab: ")
			if !ok {
				break
			}
			if id := strings.TrimSuffix(after, ")"); id != "" {
				return id
			}
		}
		t.Fatalf("MCP tab list did not contain a tab id for %s:\n%s", url, tabList)
		return ""
	}

	tabs := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	firstTab := tabIDForURL(tabs, firstURL)
	secondTab := tabIDForURL(tabs, secondURL)
	if firstTab == secondTab {
		t.Fatalf("MCP tab-new returned non-distinct tabs: first=%q second=%q", firstTab, secondTab)
	}
	firstOpen, secondOpen := true, true
	t.Cleanup(func() {
		if clientClosed {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if firstOpen {
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": firstTab})
		}
		if secondOpen {
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": secondTab})
		}
	})

	selected := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_select", map[string]interface{}{"tab": firstTab})
	requireContains(t, selected, "Switched tab", "MCP tab-select result")
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	requireContains(t, tabs, "* ", "selected MCP tab-list result")
	for _, line := range strings.Split(tabs, "\n") {
		if strings.HasPrefix(line, "* ") && !strings.Contains(line, "(tab: "+firstTab+")") {
			t.Fatalf("MCP selected unexpected tab, want %q:\n%s", firstTab, tabs)
		}
	}

	secondTitle := callE2EMCPTool(t, ctx, mcpClient, "browser_get", map[string]interface{}{
		"attribute": "title", "tab": secondTab,
	})
	if secondTitle != "E2E Verify Page Two" {
		t.Fatalf("explicit MCP tab targeting returned title %q, want %q", secondTitle, "E2E Verify Page Two")
	}
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	for _, line := range strings.Split(tabs, "\n") {
		if strings.HasPrefix(line, "* ") && !strings.Contains(line, "(tab: "+firstTab+")") {
			t.Fatalf("explicit MCP targeting changed active tab, want %q:\n%s", firstTab, tabs)
		}
	}

	closedFirst := callE2EMCPTool(t, ctx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": firstTab})
	if closedFirst != "Tab closed" {
		t.Fatalf("MCP tab-close result = %q, want %q", closedFirst, "Tab closed")
	}
	firstOpen = false
	tabs = callE2EMCPTool(t, ctx, mcpClient, "browser_tab_list", nil)
	if strings.Contains(tabs, "(tab: "+firstTab+")") || !strings.Contains(tabs, "(tab: "+secondTab+")") {
		t.Fatalf("MCP tab list after close did not preserve only the remaining test tab:\n%s", tabs)
	}
	remainingTitle := callE2EMCPTool(t, ctx, mcpClient, "browser_get", map[string]interface{}{
		"attribute": "title", "tab": secondTab,
	})
	if remainingTitle != "E2E Verify Page Two" {
		t.Fatalf("remaining MCP tab unusable after closing peer: title=%q", remainingTitle)
	}

	callE2EMCPTool(t, ctx, mcpClient, "browser_tab_close", map[string]interface{}{"tab": secondTab})
	secondOpen = false
	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	clientClosed = true
}

func TestE2EMCPErrorPathsAgainstVerifySite(t *testing.T) {
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
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-errors", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	baseURL := site.URL() + "/"
	callE2EMCPTool(t, ctx, mcpClient, "browser_navigate", map[string]interface{}{
		"url": baseURL, "new": true, "waitFor": "#ready", "timeout": 10000,
	})
	t.Cleanup(func() {
		if !closed {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_, _ = callMCPTool(cleanupCtx, mcpClient, "browser_close", nil)
		}
	})

	invalidParams := callE2EMCPRawTool(t, ctx, stdio, "invalid-params", "browser_navigate", map[string]interface{}{"url": 42})
	requireE2EMCPErrorText(t, invalidParams, "url is required", "invalid MCP parameters")

	unknownRef := callE2EMCPRawTool(t, ctx, stdio, "unknown-ref", "browser_click", map[string]interface{}{"ref": "999999"})
	requireE2EMCPErrorText(t, unknownRef, "ref", "unknown MCP ref")

	unknownTab := callE2EMCPRawTool(t, ctx, stdio, "unknown-tab", "browser_get", map[string]interface{}{
		"attribute": "url", "tab": "missing-e2e-tab",
	})
	requireE2EMCPErrorText(t, unknownTab, "tab not found", "unknown MCP tab")

	jsException := callE2EMCPRawTool(t, ctx, stdio, "js-exception", "browser_eval", map[string]interface{}{
		"script": `throw new Error("mcp-e2e-boom")`,
	})
	requireE2EMCPErrorText(t, jsException, "mcp-e2e-boom", "MCP JavaScript exception")

	snapshot := callE2EMCPTool(t, ctx, mcpClient, "browser_snapshot", map[string]interface{}{"interactive": true})
	clickRef := refFromMCPSnapshot(t, snapshot, "Click counter")
	waitTimeout := callE2EMCPRawTool(t, ctx, stdio, "wait-timeout", "browser_click", map[string]interface{}{
		"ref": clickRef, "waitFor": "#never-rendered-for-mcp", "timeout": 600,
	})
	requireE2EMCPErrorText(t, waitTimeout, `wait-for selector "#never-rendered-for-mcp"`, "MCP wait timeout")

	unsupported := sendE2EMCPRequest(t, ctx, stdio, "unsupported-call", "unsupported/e2e", map[string]interface{}{})
	if unsupported.Error == nil {
		t.Fatalf("unsupported MCP call returned no JSON-RPC error: %+v", unsupported)
	}
	if unsupported.Error.Code != mcpprotocol.METHOD_NOT_FOUND {
		t.Fatalf("unsupported MCP call error code = %d, want %d: %+v", unsupported.Error.Code, mcpprotocol.METHOD_NOT_FOUND, unsupported.Error)
	}
	requireContains(t, strings.ToLower(unsupported.Error.Message), "not found", "unsupported MCP call error")

	valid := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": `document.querySelector("#clicked-result").textContent`,
	})
	if valid != `"clicked 1"` {
		t.Fatalf("valid MCP call after errors = %q, want %q", valid, `"clicked 1"`)
	}

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func TestE2EEvalSurfaceParity(t *testing.T) {
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

	const token = "e2e-eval-parity-token"
	env, serverURL := startE2EServer(t, home, token)
	opened := runE2EJSON(t, env, "open", site.URL()+"/", "--new", "--wait-for", "#ready", "--timeout", "10000", "--json")
	tab := opened.Data.Tab
	if tab == "" {
		t.Fatalf("eval parity open response did not include tab: %+v", opened.Data)
	}
	t.Cleanup(func() {
		runE2ERESTJSON(t, serverURL, token, "/v1/close", map[string]interface{}{"tab": tab})
	})

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
	initRequest.Params.ClientInfo = mcpprotocol.Implementation{Name: "borz-e2e-eval-parity", Version: "test"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("initialize MCP stdio server: %v", err)
	}

	const topLevelAwait = `await Promise.resolve({surface: "parity", unicode: "等待 🚀", nested: {value: 43}})`
	cliResult := runE2EJSON(t, env, "eval", topLevelAwait, "--tab", tab, "--json").Data.Result

	mcpText := callE2EMCPTool(t, ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab,
	})
	var mcpResult interface{}
	if err := json.Unmarshal([]byte(mcpText), &mcpResult); err != nil {
		t.Fatalf("decode MCP eval result %q: %v", mcpText, err)
	}

	status, rawREST := runE2ERESTJSONResponse(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab,
	})
	if status != http.StatusBadRequest || rawREST.Success {
		t.Fatalf("REST top-level await without wrapper = status %d, response %+v", status, rawREST)
	}
	requireContains(t, rawREST.Error, "SyntaxError", "unwrapped REST eval error")
	requireContains(t, rawREST.Error, "await", "unwrapped REST eval error")

	restResult := runE2ERESTJSON(t, serverURL, token, "/v1/eval", map[string]interface{}{
		"script": `(async () => ({surface: "parity", unicode: "等待 🚀", nested: {value: await Promise.resolve(43)}}))()`,
		"tab":    tab,
	}).Data.Result
	if !reflect.DeepEqual(cliResult, mcpResult) || !reflect.DeepEqual(cliResult, restResult) {
		t.Fatalf("eval surface results differ: CLI=%#v MCP=%#v REST=%#v", cliResult, mcpResult, restResult)
	}

	cliOptOut := runE2EJSONResponse(t, env, "eval", topLevelAwait, "--no-auto-await", "--tab", tab, "--json")
	if cliOptOut.Success {
		t.Fatalf("CLI eval opt-out unexpectedly succeeded: %+v", cliOptOut)
	}
	requireContains(t, cliOptOut.Error, "SyntaxError", "CLI eval opt-out error")
	requireContains(t, cliOptOut.Error, "await", "CLI eval opt-out error")

	mcpOptOut, err := callMCPTool(ctx, mcpClient, "browser_eval", map[string]interface{}{
		"script": topLevelAwait, "tab": tab, "noAutoAwait": true,
	})
	if err != nil {
		t.Fatalf("call MCP eval opt-out: %v", err)
	}
	requireE2EMCPErrorText(t, mcpOptOut, "SyntaxError", "MCP eval opt-out error")
	requireE2EMCPErrorText(t, mcpOptOut, "await", "MCP eval opt-out error")

	if err := mcpClient.Close(); err != nil {
		t.Fatalf("close MCP stdio client and wait for server exit: %v", err)
	}
	closed = true
}

func sendE2EMCPRequest(t *testing.T, ctx context.Context, stdio *transport.Stdio, id, method string, params interface{}) *transport.JSONRPCResponse {
	t.Helper()
	response, err := stdio.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: mcpprotocol.JSONRPC_VERSION,
		ID:      mcpprotocol.NewRequestId(id),
		Method:  method,
		Params:  params,
	})
	if err != nil {
		t.Fatalf("send MCP request %s (%s): %v", id, method, err)
	}
	if got := response.ID.Value(); got != id {
		t.Fatalf("MCP response id = %#v, want %q for method %s", got, id, method)
	}
	return response
}

func callE2EMCPRawTool(t *testing.T, ctx context.Context, stdio *transport.Stdio, id, name string, args map[string]interface{}) *mcpprotocol.CallToolResult {
	t.Helper()
	response := sendE2EMCPRequest(t, ctx, stdio, id, "tools/call", mcpprotocol.CallToolParams{
		Name: name, Arguments: args,
	})
	if response.Error != nil {
		t.Fatalf("MCP tool %s returned JSON-RPC error for id %s: %+v", name, id, response.Error)
	}
	result, err := mcpprotocol.ParseCallToolResult(&response.Result)
	if err != nil {
		t.Fatalf("parse MCP tool %s result for id %s: %v", name, id, err)
	}
	return result
}

func requireE2EMCPErrorText(t *testing.T, result *mcpprotocol.CallToolResult, want, label string) {
	t.Helper()
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s result = %+v, want one error content item", label, result)
	}
	text, ok := result.Content[0].(mcpprotocol.TextContent)
	if !ok {
		t.Fatalf("%s returned non-text error content: %#v", label, result.Content[0])
	}
	requireContains(t, text.Text, want, label)
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
	status, resp := runE2ERESTJSONResponse(t, serverURL, token, path, body)
	if status != http.StatusOK || !resp.Success || resp.ID == "" || resp.Data == nil || resp.Error != "" {
		t.Fatalf("POST %s envelope = status %d, response %+v", path, status, resp)
	}
	return resp
}

func runE2ERESTJSONResponse(t *testing.T, serverURL, token, path string, body interface{}) (int, protocol.Response) {
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
	return httpResp.StatusCode, resp
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
