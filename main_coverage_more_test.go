package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/daemon"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/recorder"
	"github.com/leolin310148/borz/internal/site"
)

func TestMainAdditionalDispatchBranches(t *testing.T) {
	scriptFile := filepath.Join(t.TempDir(), "script.js")
	if err := os.WriteFile(scriptFile, []byte("return foo + 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		args   []string
		action protocol.ActionType
		check  func(*testing.T, protocol.Request, string)
	}{
		{"snapshot long flags", []string{"--tab", "tab-x", "snapshot", "--depth", "4", "--selector", "main"}, protocol.ActionSnapshot, func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "tab-x" || req.MaxDepth == nil || *req.MaxDepth != 4 || req.Selector != "main" {
				t.Fatalf("snapshot req = %+v", req)
			}
		}},
		{"eval file json arg auto await", []string{"eval", "--file", scriptFile, "--json-arg", "foo=41"}, protocol.ActionEval, func(t *testing.T, req protocol.Request, out string) {
			if !strings.Contains(req.Script, "const foo = 41;") || !strings.Contains(req.Script, "return foo + 1") {
				t.Fatalf("eval script = %s", req.Script)
			}
		}},
		{"scroll default", []string{"scroll"}, protocol.ActionScroll, func(t *testing.T, req protocol.Request, out string) {
			if req.Direction != "down" || req.Pixels == nil || *req.Pixels != 300 {
				t.Fatalf("scroll default req = %+v", req)
			}
		}},
		{"scroll invalid pixels", []string{"scroll", "right", "nope"}, protocol.ActionScroll, func(t *testing.T, req protocol.Request, out string) {
			if req.Direction != "right" || req.Pixels == nil || *req.Pixels != 300 {
				t.Fatalf("scroll invalid req = %+v", req)
			}
		}},
		{"wait default", []string{"wait"}, protocol.ActionWait, func(t *testing.T, req protocol.Request, out string) {
			if req.Ms == nil || *req.Ms != 1000 {
				t.Fatalf("wait default req = %+v", req)
			}
		}},
		{"dialog default", []string{"dialog"}, protocol.ActionDialog, func(t *testing.T, req protocol.Request, out string) {
			if req.DialogResponse != "accept" || req.PromptText != "" {
				t.Fatalf("dialog default req = %+v", req)
			}
		}},
		{"console get", []string{"console", "--filter", "hello"}, protocol.ActionConsole, func(t *testing.T, req protocol.Request, out string) {
			if req.ConsoleCommand != "get" || req.Filter != "hello" {
				t.Fatalf("console get req = %+v", req)
			}
		}},
		{"errors clear", []string{"errors", "--clear"}, protocol.ActionErrors, func(t *testing.T, req protocol.Request, out string) {
			if req.ErrorsCommand != "clear" {
				t.Fatalf("errors clear req = %+v", req)
			}
		}},
		{"trace start", []string{"trace", "start"}, protocol.ActionTrace, func(t *testing.T, req protocol.Request, out string) {
			if req.TraceCommand != "start" {
				t.Fatalf("trace start req = %+v", req)
			}
		}},
		{"tab default list", []string{"tab"}, protocol.ActionTabList, func(t *testing.T, req protocol.Request, out string) {}},
		{"tab new default", []string{"tab", "new"}, protocol.ActionTabNew, func(t *testing.T, req protocol.Request, out string) {
			if req.URL != "about:blank" {
				t.Fatalf("tab new default req = %+v", req)
			}
		}},
		{"tab select id flag", []string{"tab", "select", "--id", "tab-flag"}, protocol.ActionTabSelect, func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "tab-flag" {
				t.Fatalf("tab select --id req = %+v", req)
			}
		}},
		{"tab select global tab", []string{"--tab", "global-tab", "tab", "select"}, protocol.ActionTabSelect, func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "global-tab" {
				t.Fatalf("tab select global req = %+v", req)
			}
		}},
		{"tab close id flag", []string{"tab", "close", "--id", "tab-close"}, protocol.ActionTabClose, func(t *testing.T, req protocol.Request, out string) {
			if req.TabID != "tab-close" {
				t.Fatalf("tab close --id req = %+v", req)
			}
		}},
		{"tab numeric shortcut", []string{"tab", "0"}, protocol.ActionTabSelect, func(t *testing.T, req protocol.Request, out string) {
			if req.Index == nil || *req.Index != 0 {
				t.Fatalf("tab numeric req = %+v", req)
			}
		}},
		{"network route", []string{"network", "route"}, protocol.ActionNetwork, func(t *testing.T, req protocol.Request, out string) {
			if req.NetworkCommand != "route" {
				t.Fatalf("network route req = %+v", req)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, reqs := runMainWithFakeDaemon(t, tc.args...)
			if len(reqs) != 1 {
				t.Fatalf("requests=%+v out=%q", reqs, out)
			}
			if reqs[0].Action != tc.action {
				t.Fatalf("action=%s want=%s req=%+v", reqs[0].Action, tc.action, reqs[0])
			}
			tc.check(t, reqs[0], out)
		})
	}
}

func TestMainHelpAndSiteBranches(t *testing.T) {
	if newDaemonServer(daemon.ServerOptions{}) == nil {
		t.Fatal("default daemon server factory returned nil")
	}
	out := captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "help", "--all"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "borz") || !strings.Contains(out, "site") {
		t.Fatalf("help --all output = %q", out[:min(len(out), 200)])
	}
	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "help"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("help output = %q", out)
	}
	out = captureStdout(t, func() {
		oldArgs := os.Args
		os.Args = []string{"borz", "open", "--help"}
		defer func() { os.Args = oldArgs }()
		main()
	})
	if !strings.Contains(out, "open") {
		t.Fatalf("open --help output = %q", out)
	}

	newFakeDaemon(t)
	home := os.Getenv("BORZ_HOME")
	adapterDir := filepath.Join(home, "sites", "demo")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := `/* @meta
{
  "name": "demo/extra",
  "description": "Extra adapter",
  "domain": "example.test",
  "timeoutMs": 99,
  "args": {"q": {"required": true, "description": "query", "default": "cats"}},
  "output": {"type":"object"},
  "example": "borz demo/extra cats"
}
*/
return {ok:true, q: args.q};`
	adapterPath := filepath.Join(adapterDir, "extra.js")
	if err := os.WriteFile(adapterPath, []byte(adapter), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"help", "demo/extra"},
		{"demo/extra", "--help"},
		{"site", "demo/extra", "--help"},
	} {
		out = captureStdout(t, func() {
			oldArgs := os.Args
			os.Args = append([]string{"borz"}, args...)
			defer func() { os.Args = oldArgs }()
			main()
		})
		if !strings.Contains(out, "Extra adapter") || !strings.Contains(out, "Timeout:") || !strings.Contains(out, "Output:") {
			t.Fatalf("%v help output = %q", args, out)
		}
	}

	out = captureStdout(t, func() { handleSite([]string{"info", "demo/extra"}, true, "") })
	if !strings.Contains(out, `"name": "demo/extra"`) {
		t.Fatalf("site info JSON = %q", out)
	}
	out = captureStdout(t, func() { handleSite([]string{"search", "extra"}, false, "") })
	if !strings.Contains(out, "demo/extra") || !strings.Contains(out, "results") {
		t.Fatalf("site search text = %q", out)
	}
	out = captureStdout(t, func() { handleSite([]string{"lint", adapterPath}, false, "") })
	if !strings.Contains(out, "warning:") {
		t.Fatalf("site lint path = %q", out)
	}
	out = captureStdout(t, func() { handleSite([]string{"trust", "demo/extra"}, false, "") })
	if !strings.Contains(out, "Trusted demo/extra") {
		t.Fatalf("site trust local = %q", out)
	}
	out = captureStdout(t, func() { handleSite([]string{"demo/extra", "dogs"}, false, "tab-1") })
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("site default run = %q", out)
	}
	meta := siteMetaForConfirmTest(t, home)
	if err := confirmCommunityAdapter(&meta); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("confirm community error = %v", err)
	}
}

func TestRecordAdditionalBranches(t *testing.T) {
	out, reqs := runMainWithFakeDaemon(t, "record", "list", "--json")
	if len(reqs) != 0 || !strings.Contains(out, `"recordings"`) {
		t.Fatalf("record list json out=%q reqs=%+v", out, reqs)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "start", "https://positional.test", "--id", "rec-x", "--tab", "tab-1", "--lossless", "--audio", "mic, system", "--viewport", "800x600", "--dpr", "2", "--mask-selectors", ".secret,#token", "--mask-by-default", "--max-size", "2K", "--json")
	if !strings.Contains(out, `"rec-start"`) {
		t.Fatalf("record start json = %q", out)
	}
	for _, args := range [][]string{
		{"record", "stop", "--recover", "--json"},
		{"record", "pause", "--json"},
		{"record", "resume", "--json"},
		{"record", "info", "rec-1", "--json"},
	} {
		out, _ = runMainWithFakeDaemon(t, args...)
		if !strings.Contains(out, `"rec-1"`) {
			t.Fatalf("%v output = %q", args, out)
		}
	}

	dir := filepath.Join(t.TempDir(), "more.borzrec")
	w, err := recorder.Create(dir, recorder.CaptureOptions{ID: "more", Mode: "cdp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFrame(0, cliTestPNG(t), "png", recorder.Viewport{Width: 10, Height: 10, DPR: 1}, "", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { handleRecord([]string{"verify", dir}, []string{"record", "verify", dir}, true) })
	if !strings.Contains(out, `"id": "more"`) {
		t.Fatalf("verify json = %q", out)
	}
	fakeFFmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		handleRecord([]string{"render", dir}, []string{"record", "render", dir, "--ffmpeg", fakeFFmpeg, "--format", "gif", "--annotations", "cursor,keys", "--trim", "0-1", "--speed", "2x", "--watermark", "wm", "--smooth", "--chapters", "chapters.json", "--json"}, true)
	})
	if !strings.Contains(out, `"ok": true`) || !strings.Contains(out, `"out": "out.gif"`) {
		t.Fatalf("render json = %q", out)
	}
	out = captureStdout(t, func() { handleRecord([]string{"edit", dir}, []string{"record", "edit", dir}, false) })
	if !strings.Contains(out, "Bundle:") {
		t.Fatalf("edit local = %q", out)
	}
	out, _ = runMainWithFakeDaemon(t, "record", "play", "rec-1")
	if !strings.Contains(out, "Preview:") {
		t.Fatalf("play remote = %q", out)
	}

	for raw, want := range map[string]int64{"7": 7, "2K": 2048, "3M": 3 << 20, "4G": 4 << 30, "bad": 0} {
		if got := parseBytes(raw); got != want {
			t.Fatalf("parseBytes(%q) = %d, want %d", raw, got, want)
		}
	}
	if _, err := parseMask("1,2,bad,4"); err == nil {
		t.Fatal("parseMask should reject invalid numbers")
	}
	if b, err := json.Marshal(recordInfo{ID: "x"}); err != nil || !strings.Contains(string(b), "x") {
		t.Fatalf("recordInfo marshal = %s err=%v", b, err)
	}
	client.ResetForTests()
}

func siteMetaForConfirmTest(t *testing.T, home string) site.SiteMeta {
	t.Helper()
	path := filepath.Join(home, "bb-sites", "unsafe.js")
	body := strings.Replace(sampleSiteAdapterForMainTest, "demo/unsafe", "unsafe", 1)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := site.ParseSiteMeta(path, "community")
	if err != nil {
		t.Fatal(err)
	}
	return *meta
}

const sampleSiteAdapterForMainTest = `/* @meta
{"name":"demo/unsafe","description":"Unsafe","domain":"example.test","args":{}}
*/
return {};`
