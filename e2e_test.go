package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/client"
	e2everify "github.com/leolin310148/bb-browser-go/internal/e2e_verify_site"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

const e2eEnabledEnv = "BB_BROWSER_E2E"

func TestE2ECLIHelper(t *testing.T) {
	if os.Getenv("BB_BROWSER_E2E_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"bb-browser"}, os.Args[i+1:]...)
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
	t.Setenv("BB_BROWSER_HOME", home)
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

	runE2EJSON(t, env, "wait", "10", "--json")
	runE2EJSON(t, env, "scroll", "down", "900", "--json")
	runE2EJSON(t, env, "wait", "200", "--json")
	requireEvalBool(t, env, "document.scrollingElement.scrollTop > 0 || window.scrollY > 0", true)

	screenshot := runE2EJSON(t, env, "screenshot", "--json")
	if !strings.HasPrefix(screenshot.Data.DataURL, "data:image/png;base64,") {
		t.Fatalf("screenshot data URL prefix mismatch: %.40q", screenshot.Data.DataURL)
	}

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
}

func TestE2EClientModeAgainstServer(t *testing.T) {
	skipUnlessE2E(t)

	home := t.TempDir()
	t.Setenv("BB_BROWSER_HOME", home)
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
	requireEvalStringWithPrefix(t, env, []string{"--remote"}, "document.title", "E2E Verify Home")

	snapshot := runE2EJSON(t, env, "--remote", "snapshot", "-i", "--json")
	clickRef := refByName(t, snapshot.Data.SnapshotData, "Click counter")
	runE2EJSON(t, env, "--remote", "click", clickRef, "--json")
	requireEvalStringWithPrefix(t, env, []string{"--remote"}, `document.querySelector("#clicked-result").textContent`, "clicked 1")

	tabs := runE2EJSON(t, env, "--remote", "tab", "list", "--json")
	if len(tabs.Data.Tabs) == 0 {
		t.Fatalf("remote tab list returned no tabs: %+v", tabs.Data)
	}
}

type e2eDaemonEnv struct {
	home string
}

func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("local Chrome e2e tests are disabled in GitHub Actions")
	}
	if os.Getenv(e2eEnabledEnv) != "1" {
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
		"BB_BROWSER_E2E_HELPER=1",
		"BB_BROWSER_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bb-browser daemon helper: %v", err)
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
		"BB_BROWSER_E2E_HELPER=1",
		"BB_BROWSER_HOME="+home,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bb-browser server helper: %v", err)
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
		"BB_BROWSER_E2E_HELPER=1",
		"BB_BROWSER_HOME="+env.home,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bb-browser %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

func runE2EJSON(t *testing.T, env e2eDaemonEnv, args ...string) protocol.Response {
	t.Helper()
	out := runE2ECLI(t, env, args...)
	var resp protocol.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("bb-browser %s returned non-JSON response: %v\n%s", strings.Join(args, " "), err, out)
	}
	if !resp.Success {
		t.Fatalf("bb-browser %s returned unsuccessful response: %s\n%s", strings.Join(args, " "), resp.Error, out)
	}
	if resp.Data == nil {
		t.Fatalf("bb-browser %s returned empty data: %s", strings.Join(args, " "), out)
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
