package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestDispatchTabSelectionCloseAndTraceBranches(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetInfos": []interface{}{
			map[string]interface{}{"targetId": "Target-0001", "type": "page", "url": "https://one.test", "title": "One"},
			map[string]interface{}{"targetId": "Target-0002", "type": "page", "url": "https://two.test", "title": "Two"},
			map[string]interface{}{"targetId": "Worker-0003", "type": "worker", "url": "https://w.test", "title": "Worker"},
		}}, nil
	})
	c := connectCdp(t, f)
	c.TabManager.AddTab("Target-0001")
	c.TabManager.AddTab("Target-0002")

	resp := DispatchRequest(c, &protocol.Request{ID: "list", Action: protocol.ActionTabList})
	if !resp.Success || len(resp.Data.Tabs) != 2 || resp.Data.ActiveIndex == nil {
		t.Fatalf("tab list = %+v", resp)
	}
	short := c.TabManager.GetShortID("Target-0002")
	resp = DispatchRequest(c, &protocol.Request{ID: "short", Action: protocol.ActionTabSelect, TabID: short})
	if !resp.Success || resp.Data.TabID != "Target-0002" || resp.Data.Tab != short {
		t.Fatalf("select by short id = %+v short=%q", resp, short)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "idxstr", Action: protocol.ActionTabSelect, TabID: "0"})
	if !resp.Success || resp.Data.TabID != "Target-0001" {
		t.Fatalf("select by numeric string = %+v", resp)
	}
	idx := 1
	resp = DispatchRequest(c, &protocol.Request{ID: "idx", Action: protocol.ActionTabSelect, Index: &idx})
	if !resp.Success || resp.Data.TabID != "Target-0002" {
		t.Fatalf("select by index = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "missing", Action: protocol.ActionTabSelect, TabID: "nope"})
	if resp.Success || !strings.Contains(resp.Error, "tab not found") {
		t.Fatalf("select missing = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "closeidx", Action: protocol.ActionTabClose, Index: &idx})
	if !resp.Success || resp.Data.TabID != "Target-0002" {
		t.Fatalf("close by index = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "closemissing", Action: protocol.ActionTabClose, TabID: "nope"})
	if resp.Success || !strings.Contains(resp.Error, "tab not found") {
		t.Fatalf("close missing = %+v", resp)
	}

	for _, tc := range []struct {
		cmd     string
		success bool
	}{
		{"start", true},
		{"status", true},
		{"stop", true},
		{"bogus", false},
	} {
		resp := DispatchRequest(c, &protocol.Request{ID: "trace-" + tc.cmd, Action: protocol.ActionTrace, TraceCommand: tc.cmd})
		if resp.Success != tc.success {
			t.Fatalf("trace %s = %+v", tc.cmd, resp)
		}
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "history", Action: protocol.ActionHistory})
	if resp.Success || !strings.Contains(resp.Error, "not supported") {
		t.Fatalf("history = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "unknown", Action: "made-up"})
	if resp.Success || !strings.Contains(resp.Error, "unknown action") {
		t.Fatalf("unknown = %+v", resp)
	}
}

func TestDispatchFrameDialogAndEventQueryBranches(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a.test", "A")
	f.On("DOM.getDocument", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"root": map[string]interface{}{"nodeId": 10}}, nil
	})
	queryNode := 7
	f.On("DOM.querySelector", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"nodeId": queryNode}, nil
	})
	frameID := "F1"
	nodeName := "IFRAME"
	f.On("DOM.describeNode", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"node": map[string]interface{}{
			"frameId": frameID, "nodeName": nodeName,
			"attributes": []interface{}{"name", "app", "src", "https://frame.test"},
		}}, nil
	})
	f.On("Network.getResponseBody", func(params json.RawMessage) (interface{}, error) {
		if strings.Contains(string(params), "bad-body") {
			return nil, fmt.Errorf("body unavailable")
		}
		return map[string]interface{}{"body": base64.StdEncoding.EncodeToString([]byte("ok")), "base64Encoded": true}, nil
	})
	c := connectCdp(t, f)
	tab := c.TabManager.AddTab("T1")
	status := 200
	tab.AddNetworkRequest("ok-body", protocol.NetworkRequestInfo{URL: "https://api.test/ok", Method: "GET", Type: "fetch", Status: &status})
	tab.AddNetworkRequest("bad-body", protocol.NetworkRequestInfo{URL: "https://api.test/bad", Method: "POST", Type: "fetch", Status: &status})
	tab.AddNetworkRequest("failed", protocol.NetworkRequestInfo{URL: "https://api.test/failed", Method: "GET", Type: "fetch", Failed: true})
	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Type: "log", Text: "hello"})
	tab.AddJSError(protocol.JSErrorInfo{Message: "boom"})

	resp := DispatchRequest(c, &protocol.Request{ID: "frame-missing-selector", Action: protocol.ActionFrame})
	if resp.Success || !strings.Contains(resp.Error, "missing selector") {
		t.Fatalf("frame missing selector = %+v", resp)
	}
	queryNode = 0
	resp = DispatchRequest(c, &protocol.Request{ID: "frame-not-found", Action: protocol.ActionFrame, Selector: "iframe"})
	if resp.Success || !strings.Contains(resp.Error, "iframe not found") {
		t.Fatalf("frame not found = %+v", resp)
	}
	queryNode = 7
	frameID = ""
	resp = DispatchRequest(c, &protocol.Request{ID: "frame-no-id", Action: protocol.ActionFrame, Selector: "iframe"})
	if resp.Success || !strings.Contains(resp.Error, "cannot get iframe") {
		t.Fatalf("frame no id = %+v", resp)
	}
	frameID = "F1"
	nodeName = "DIV"
	resp = DispatchRequest(c, &protocol.Request{ID: "frame-not-iframe", Action: protocol.ActionFrame, Selector: "div"})
	if resp.Success || !strings.Contains(resp.Error, "not an iframe") {
		t.Fatalf("frame not iframe = %+v", resp)
	}
	nodeName = "IFRAME"
	resp = DispatchRequest(c, &protocol.Request{ID: "frame-ok", Action: protocol.ActionFrame, Selector: "iframe"})
	frameInfo, _ := resp.Data.FrameInfo.(map[string]interface{})
	if !resp.Success || frameInfo["frameId"] != "F1" {
		t.Fatalf("frame ok = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "frame-main", Action: protocol.ActionFrameMain})
	if !resp.Success || tab.ActiveFrameID != "" {
		t.Fatalf("frame main = %+v active=%q", resp, tab.ActiveFrameID)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "dialog", Action: protocol.ActionDialog, DialogResponse: "dismiss"})
	if !resp.Success || tab.DialogHandler == nil || tab.DialogHandler.Accept {
		t.Fatalf("dialog dismiss = %+v handler=%+v", resp, tab.DialogHandler)
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "net", Action: protocol.ActionNetwork, WithBody: true})
	if !resp.Success || len(resp.Data.NetworkRequests) != 3 {
		t.Fatalf("network with bodies = %+v", resp)
	}
	if resp.Data.NetworkRequests[0].ResponseBody == "" && resp.Data.NetworkRequests[1].ResponseBody == "" {
		t.Fatalf("expected one response body in %+v", resp.Data.NetworkRequests)
	}
	for _, sub := range []string{"clear", "route", "unroute"} {
		resp = DispatchRequest(c, &protocol.Request{ID: "net-" + sub, Action: protocol.ActionNetwork, NetworkCommand: sub})
		if !resp.Success {
			t.Fatalf("network %s = %+v", sub, resp)
		}
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "net-bogus", Action: protocol.ActionNetwork, NetworkCommand: "bogus"})
	if resp.Success || !strings.Contains(resp.Error, "unknown network") {
		t.Fatalf("network bogus = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "console-get", Action: protocol.ActionConsole})
	if !resp.Success || len(resp.Data.ConsoleMessages) != 1 {
		t.Fatalf("console get = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "console-bogus", Action: protocol.ActionConsole, ConsoleCommand: "bogus"})
	if resp.Success || !strings.Contains(resp.Error, "unknown console") {
		t.Fatalf("console bogus = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "errors-get", Action: protocol.ActionErrors})
	if !resp.Success || len(resp.Data.JSErrors) != 1 {
		t.Fatalf("errors get = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "errors-clear", Action: protocol.ActionErrors, ErrorsCommand: "clear"})
	if !resp.Success {
		t.Fatalf("errors clear = %+v", resp)
	}
	resp = DispatchRequest(c, &protocol.Request{ID: "errors-bogus", Action: protocol.ActionErrors, ErrorsCommand: "bogus"})
	if resp.Success || !strings.Contains(resp.Error, "unknown errors") {
		t.Fatalf("errors bogus = %+v", resp)
	}
}
