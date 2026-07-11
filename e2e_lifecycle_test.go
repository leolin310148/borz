package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/client"
	e2everify "github.com/leolin310148/borz/internal/e2e_verify_site"
	"github.com/leolin310148/borz/internal/protocol"
)

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
