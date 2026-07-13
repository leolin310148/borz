package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestResolveSiteScope(t *testing.T) {
	tests := []struct {
		args    []string
		want    siteScope
		wantErr string
	}{
		{nil, siteScopeClient, ""},
		{[]string{"--scope", "client"}, siteScopeClient, ""},
		{[]string{"--scope=server"}, siteScopeServer, ""},
		{[]string{"--scope", "SERVER"}, siteScopeServer, ""},
		{[]string{"--scope", ""}, "", "requires client or server"},
		{[]string{"--scope", "remote"}, "", "expected client or server"},
	}
	for _, tc := range tests {
		got, err := resolveSiteScope(tc.args)
		if got != tc.want {
			t.Errorf("resolveSiteScope(%v) = %q, want %q", tc.args, got, tc.want)
		}
		if tc.wantErr == "" && err != nil {
			t.Errorf("resolveSiteScope(%v) unexpected error: %v", tc.args, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("resolveSiteScope(%v) error = %v, want %q", tc.args, err, tc.wantErr)
		}
	}
}

func TestServerSiteScopeHelpers(t *testing.T) {
	origGet, origPost := siteGetJSON, sitePostJSON
	t.Cleanup(func() { siteGetJSON, sitePostJSON = origGet, origPost })

	siteGetJSON = func(path string, timeout time.Duration) (json.RawMessage, error) {
		if path != "/v1/sites" || timeout != 30*time.Second {
			t.Fatalf("GET = %s timeout=%s", path, timeout)
		}
		return json.RawMessage(`{"success":true,"data":{"sites":[{"name":"server/demo","domain":"example.com"}]}}`), nil
	}

	var runBody map[string]interface{}
	sitePostJSON = func(path string, body interface{}, timeout time.Duration) (json.RawMessage, error) {
		switch path {
		case "/v1/sites/info":
			if !reflect.DeepEqual(body, map[string]interface{}{"name": "server/demo"}) {
				t.Fatalf("info body = %#v", body)
			}
			return json.RawMessage(`{"success":true,"data":{"site":{"name":"server/demo","argOrder":["q"],"args":{"q":{"required":true}}}}}`), nil
		case "/v1/sites/run":
			runBody = body.(map[string]interface{})
			return json.RawMessage(`{"success":true,"data":{"result":{"ok":true}}}`), nil
		default:
			t.Fatalf("unexpected POST %s", path)
			return nil, nil
		}
	}

	sites, err := loadServerSites()
	if err != nil || len(sites) != 1 || sites[0].Name != "server/demo" {
		t.Fatalf("loadServerSites = %+v, %v", sites, err)
	}
	meta, err := loadServerSite("server/demo")
	if err != nil || meta.Name != "server/demo" || len(meta.ArgOrder) != 1 {
		t.Fatalf("loadServerSite = %+v, %v", meta, err)
	}
	resp, err := runServerSite("server/demo", map[string]interface{}{"q": "AI"}, "T1", true, 2500)
	if err != nil || !resp.Success || resp.Data == nil {
		t.Fatalf("runServerSite = %+v, %v", resp, err)
	}
	if runBody["name"] != "server/demo" || runBody["tab"] != "T1" || runBody["force"] != true || runBody["timeoutMs"] != 2500 {
		t.Fatalf("run body = %#v", runBody)
	}
	if args, ok := runBody["args"].(map[string]interface{}); !ok || args["q"] != "AI" {
		t.Fatalf("run args = %#v", runBody["args"])
	}
}

func TestPrintEvalResponseServerSite(t *testing.T) {
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: "server result"}}
	out := captureStdout(t, func() {
		if !printEvalResponse(resp, false, true) {
			t.Fatal("response should succeed")
		}
	})
	if strings.TrimSpace(out) != "server result" {
		t.Fatalf("output = %q", out)
	}
}

func TestHandleSiteRunServerScopeUsesDaemonCatalog(t *testing.T) {
	origPost := sitePostJSON
	oldArgs := os.Args
	t.Cleanup(func() {
		sitePostJSON = origPost
		os.Args = oldArgs
	})
	os.Args = []string{"borz", "site", "run", "server/demo", "kittens", "--scope", "server"}

	var runBody map[string]interface{}
	sitePostJSON = func(path string, body interface{}, timeout time.Duration) (json.RawMessage, error) {
		switch path {
		case "/v1/sites/info":
			return json.RawMessage(`{"success":true,"data":{"site":{"name":"server/demo","source":"community","argOrder":["q"],"args":{"q":{"required":true}}}}}`), nil
		case "/v1/sites/run":
			runBody = body.(map[string]interface{})
			return json.RawMessage(`{"success":true,"data":{"result":{"scope":"server"}}}`), nil
		default:
			t.Fatalf("unexpected POST %s", path)
			return nil, nil
		}
	}

	out := captureStdout(t, func() {
		handleSite([]string{"run", "server/demo", "kittens"}, false, "T9")
	})
	if !strings.Contains(out, `"scope": "server"`) {
		t.Fatalf("output = %q", out)
	}
	if runBody["tab"] != "T9" {
		t.Fatalf("run body = %#v", runBody)
	}
	args, ok := runBody["args"].(map[string]interface{})
	if !ok || args["q"] != "kittens" {
		t.Fatalf("run args = %#v", runBody["args"])
	}
}
