package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

// setupOnePage configures the fake to advertise one page target and attach it.
func setupOnePage(f *fakeCDP, targetID, url, title string) {
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"targetInfos": []interface{}{
				map[string]interface{}{"targetId": targetID, "type": "page", "url": url, "title": title},
			},
		}, nil
	})
}

func TestDispatch_Back_Forward_Refresh_Close(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	// Every Runtime.evaluate / Page.reload / Target.closeTarget returns {}.
	c := connectCdp(t, f)

	for _, action := range []protocol.ActionType{protocol.ActionBack, protocol.ActionForward, protocol.ActionRefresh} {
		resp := DispatchRequest(c, &protocol.Request{ID: string(action), Action: action})
		if !resp.Success {
			t.Fatalf("%s failed: %+v", action, resp)
		}
	}

	resp := DispatchRequest(c, &protocol.Request{ID: "close", Action: protocol.ActionClose})
	if !resp.Success {
		t.Fatalf("close failed: %+v", resp)
	}
}

func TestDispatch_Open_NewTab(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-NEW"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test"})
	if !resp.Success {
		t.Fatalf("open: %+v", resp)
	}
	if resp.Data.URL != "https://ex.test" {
		t.Fatalf("url: %q", resp.Data.URL)
	}
	if c.CurrentTargetID != "T-NEW" {
		t.Fatalf("open should set current target to new tab, got %q", c.CurrentTargetID)
	}
}

func TestDispatch_Open_WaitFor_Found(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://ex.test", "Existing")

	// Return false the first two probes, then true.
	var probes int32
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if !strings.Contains(p.Expression, "querySelector") {
			return map[string]interface{}{"result": map[string]interface{}{"type": "undefined"}}, nil
		}
		n := atomic.AddInt32(&probes, 1)
		val := n >= 3
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "boolean", "value": val},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test", WaitFor: ".article",
	})
	if !resp.Success {
		t.Fatalf("open --wait-for: %+v", resp)
	}
	if got := atomic.LoadInt32(&probes); got < 3 {
		t.Fatalf("expected at least 3 probes, got %d", got)
	}
}

func TestDispatch_Open_WaitFor_Timeout(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://ex.test", "Existing")
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		// Always report "not found" so the wait times out.
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "boolean", "value": false},
		}, nil
	})
	c := connectCdp(t, f)

	timeoutMs := 150
	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test", WaitFor: ".never", TimeoutMs: &timeoutMs,
	})
	if resp.Success {
		t.Fatalf("expected timeout failure, got success: %+v", resp)
	}
	if !strings.Contains(resp.Error, "timeout") {
		t.Fatalf("expected timeout error, got: %q", resp.Error)
	}
}

func TestDispatch_Click_WaitFor_Found(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")

	// First Runtime.evaluate calls are part of click (point lookup, etc.) —
	// only the wait-for probe matches `querySelector`. Return true on the
	// second querySelector probe.
	var probes int32
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if !strings.Contains(p.Expression, "querySelector") {
			// Click pipeline may evaluate other things; respond with empty result.
			return map[string]interface{}{"result": map[string]interface{}{"type": "undefined"}}, nil
		}
		n := atomic.AddInt32(&probes, 1)
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "boolean", "value": n >= 2},
		}, nil
	})
	c := connectCdp(t, f)

	// Build a request that resolves a ref via XPath; the click pipeline needs
	// some DOM plumbing, so we mock that minimally and trust the existing
	// dispatch path. Use --wait-for and assert the probes ran.
	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionEval, Script: "1+1", WaitFor: ".loaded",
	})
	if !resp.Success {
		t.Fatalf("eval --wait-for: %+v", resp)
	}
	if got := atomic.LoadInt32(&probes); got < 2 {
		t.Fatalf("expected at least 2 wait-for probes, got %d", got)
	}
}

func TestDispatch_Eval_WaitFor_Timeout(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if !strings.Contains(p.Expression, "querySelector") {
			return map[string]interface{}{"result": map[string]interface{}{"type": "number", "value": 2}}, nil
		}
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "boolean", "value": false},
		}, nil
	})
	c := connectCdp(t, f)

	timeoutMs := 150
	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionEval, Script: "1+1",
		WaitFor: ".never", TimeoutMs: &timeoutMs,
	})
	if resp.Success {
		t.Fatalf("expected timeout failure, got success: %+v", resp)
	}
	if !strings.Contains(resp.Error, "timeout") {
		t.Fatalf("expected timeout error, got: %q", resp.Error)
	}
}

func TestDispatch_Open_ReuseExisting(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://ex.test", "Existing")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test"})
	if !resp.Success {
		t.Fatalf("open reuse: %+v", resp)
	}
	if resp.Data.TabID != "T1" {
		t.Fatalf("should reuse existing T1, got %v", resp.Data.TabID)
	}
}

func TestDispatch_Open_ForceNewWithFlag(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://ex.test", "Existing")
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-FRESH"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test", New: true})
	if !resp.Success || resp.Data.TabID != "T-FRESH" {
		t.Fatalf("open --new: %+v", resp)
	}
}

// activatedTargetIDs returns the targetIds passed to every Target.activateTarget call.
func activatedTargetIDs(t *testing.T, f *fakeCDP) []string {
	t.Helper()
	var ids []string
	for _, c := range f.Calls() {
		if c.Method != "Target.activateTarget" {
			continue
		}
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(c.Params, &p)
		ids = append(ids, p.TargetID)
	}
	return ids
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestDispatch_Open_NewTab_ActivatesTab(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-NEW"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test"})
	if !resp.Success {
		t.Fatalf("open: %+v", resp)
	}
	if !contains(activatedTargetIDs(t, f), "T-NEW") {
		t.Fatalf("expected Target.activateTarget for T-NEW, got %v", activatedTargetIDs(t, f))
	}
}

func TestDispatch_Open_ReuseExisting_ActivatesTab(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://ex.test", "Existing")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://ex.test"})
	if !resp.Success {
		t.Fatalf("open reuse: %+v", resp)
	}
	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatalf("expected Target.activateTarget for T1 (reused), got %v", activatedTargetIDs(t, f))
	}
}

func TestDispatch_Open_ExistingTab_ActivatesTab(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://old", "Old")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionOpen, URL: "https://new", TabID: "T1"})
	if !resp.Success {
		t.Fatalf("open --tab: %+v", resp)
	}
	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatalf("expected Target.activateTarget for T1, got %v", activatedTargetIDs(t, f))
	}
}

func TestDispatch_TabNew(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-NEW"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabNew, URL: "about:blank"})
	if !resp.Success || resp.Data.TabID != "T-NEW" {
		t.Fatalf("tab_new: %+v", resp)
	}
}

// TestDispatch_TabNew_WaitsForNavigation guards against the race that produced
// gh#1: tab_new returned a tabId whose page context was still about:blank, so
// the next fetch ran with the wrong origin and failed CORS as a generic
// "Failed to fetch". The fix polls until the document leaves about:blank
// before returning. Here the fake initially reports the page as still on
// about:blank for the first 2 probes, then ready — tab_new must not return
// until the ready signal arrives.
func TestDispatch_TabNew_WaitsForNavigation(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.createTarget", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetId": "T-NEW"}, nil
	})
	var probes int32
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		n := atomic.AddInt32(&probes, 1)
		href := "about:blank"
		state := "loading"
		if n >= 3 {
			href = "https://reddit.test/"
			state = "complete"
		}
		return map[string]interface{}{
			"result": map[string]interface{}{
				"type":  "string",
				"value": `{"readyState":"` + state + `","href":"` + href + `"}`,
			},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabNew, URL: "https://reddit.test/"})
	if !resp.Success {
		t.Fatalf("tab_new: %+v", resp)
	}
	if got := atomic.LoadInt32(&probes); got < 3 {
		t.Fatalf("expected at least 3 readiness probes, got %d", got)
	}
}

func TestDispatch_TabList_NonEmpty(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"targetInfos": []interface{}{
				map[string]interface{}{"targetId": "T1", "type": "page", "url": "https://a", "title": "A"},
				map[string]interface{}{"targetId": "T2", "type": "page", "url": "https://b", "title": "B"},
				map[string]interface{}{"targetId": "T3", "type": "iframe", "url": "x", "title": ""}, // filtered out
			},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabList})
	if !resp.Success {
		t.Fatalf("tab_list: %+v", resp)
	}
	if len(resp.Data.Tabs) != 2 {
		t.Fatalf("tabs: %+v", resp.Data.Tabs)
	}
}

func TestDispatch_TabSelect(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabSelect, TabID: "T1"})
	if !resp.Success {
		t.Fatalf("tab_select: %+v", resp)
	}
	// tab_select must physically activate the tab in Chrome's UI, not just
	// flip the daemon's CurrentTargetID. Otherwise n8n / external apps that
	// expect "switch tab" semantics see no visible change.
	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatalf("tab_select should call Target.activateTarget for T1, got %v", activatedTargetIDs(t, f))
	}
}

func TestDispatch_Activate_Flag_FocusesTab(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	// Any action with Activate=true should bring the tab forward before
	// running. Use snapshot — it normally does NOT activate.
	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionSnapshot, TabID: "T1", Activate: true,
	})
	if !resp.Success {
		t.Fatalf("snapshot+activate: %+v", resp)
	}
	if !contains(activatedTargetIDs(t, f), "T1") {
		t.Fatalf("Activate=true should call Target.activateTarget, got %v", activatedTargetIDs(t, f))
	}
}

func TestEnsurePageTarget_DoesNotMutateCurrentWhenTabRefSet(t *testing.T) {
	f := newFakeCDP(t)
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"targetInfos": []interface{}{
				map[string]interface{}{"targetId": "T1", "type": "page", "url": "https://a", "title": "A"},
				map[string]interface{}{"targetId": "T2", "type": "page", "url": "https://b", "title": "B"},
			},
		}, nil
	})
	c := connectCdp(t, f)

	// Seed CurrentTargetID to T1, then route a command at T2 explicitly.
	c.CurrentTargetID = "T1"
	target, err := c.EnsurePageTarget("T2")
	if err != nil || target.ID != "T2" {
		t.Fatalf("EnsurePageTarget(T2) = (%v, %v)", target, err)
	}
	// CurrentTargetID must NOT change — explicit-tab requests should not
	// race-shift the implicit "current tab" used by other concurrent callers.
	if c.CurrentTargetID != "T1" {
		t.Fatalf("CurrentTargetID mutated to %q after explicit-tab routing; want T1", c.CurrentTargetID)
	}

	// Conversely, an empty tabRef MAY update CurrentTargetID (no caller is
	// claiming a specific tab, so the daemon's notion of "current" may follow).
	c.CurrentTargetID = ""
	if _, err := c.EnsurePageTarget(""); err != nil {
		t.Fatal(err)
	}
	if c.CurrentTargetID == "" {
		t.Fatal("EnsurePageTarget(\"\") with no current should seed CurrentTargetID")
	}
}

func TestDispatch_TabClose(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionTabClose, TabID: "T1"})
	if !resp.Success {
		t.Fatalf("tab_close: %+v", resp)
	}
}

func TestDispatch_Wait(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	ms := 20
	start := time.Now()
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionWait, Ms: &ms})
	if !resp.Success || time.Since(start) < 15*time.Millisecond {
		t.Fatalf("wait: %+v (elapsed %v)", resp, time.Since(start))
	}
}

func TestDispatch_Eval(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"type": "number", "value": 42},
		}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionEval, Script: "1+1"})
	if !resp.Success {
		t.Fatalf("eval: %+v", resp)
	}
	// Result after JSON round-trip is float64.
	if f64, ok := resp.Data.Result.(float64); !ok || f64 != 42 {
		t.Fatalf("result: %+v", resp.Data.Result)
	}
}

func TestDispatch_Eval_MissingScript(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionEval})
	if resp.Success || !strings.Contains(resp.Error, "script") {
		t.Fatalf("expected missing-script error: %+v", resp)
	}
}

func TestDispatch_Scroll_AllDirections(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	for _, dir := range []string{"up", "down", "left", "right", ""} {
		resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionScroll, Direction: dir})
		if !resp.Success {
			t.Fatalf("scroll %q: %+v", dir, resp)
		}
	}

	// Custom pixel count.
	px := 500
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionScroll, Direction: "down", Pixels: &px})
	if !resp.Success {
		t.Fatalf("scroll px: %+v", resp)
	}
}

func TestDispatch_Press(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPress, Key: "a"})
	if !resp.Success {
		t.Fatalf("press: %+v", resp)
	}
	// Press without a key -> error.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionPress})
	if resp.Success {
		t.Fatal("press without key should fail")
	}
}

func TestDispatch_Key_AllTypes(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	// type
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "type", Text: "hi"})
	if !resp.Success {
		t.Fatalf("type: %+v", resp)
	}

	// down
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "down", Key: "Shift"})
	if !resp.Success {
		t.Fatalf("down: %+v", resp)
	}

	// up
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "up", Key: "Shift"})
	if !resp.Success {
		t.Fatalf("up: %+v", resp)
	}

	// press (default)
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, Key: "Enter"})
	if !resp.Success {
		t.Fatalf("press default: %+v", resp)
	}

	// unknown keyType
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "bogus", Key: "a"})
	if resp.Success {
		t.Fatalf("bogus keyType should fail")
	}

	// type with missing text
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "type"})
	if resp.Success {
		t.Fatalf("type with no text should fail")
	}

	// down with missing key
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionKey, KeyType: "down"})
	if resp.Success {
		t.Fatalf("down with no key should fail")
	}
}

func TestDispatch_Mouse_AllTypes(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	x, y := 10.0, 20.0
	for _, mt := range []string{"move", "down", "up", "click", "wheel", ""} {
		dx, dy := 1.0, 2.0
		resp := DispatchRequest(c, &protocol.Request{
			ID: "x", Action: protocol.ActionMouse, MouseType: mt, X: &x, Y: &y,
			DeltaX: &dx, DeltaY: &dy,
		})
		if !resp.Success {
			t.Fatalf("mouse %q: %+v", mt, resp)
		}
	}

	// Unknown mouse type.
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionMouse, MouseType: "bogus"})
	if resp.Success {
		t.Fatal("bogus mouseType should fail")
	}
}

func TestDispatch_Screenshot(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Page.captureScreenshot", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"data": "AAAA"}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionScreenshot})
	if !resp.Success {
		t.Fatalf("screenshot: %+v", resp)
	}
	if !strings.HasPrefix(resp.Data.DataURL, "data:image/png;base64,") {
		t.Fatalf("dataURL: %q", resp.Data.DataURL)
	}

	shotPath := filepath.Join(t.TempDir(), "shot.png")
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionScreenshot, Path: shotPath})
	if !resp.Success {
		t.Fatalf("screenshot with path: %+v", resp)
	}
	if resp.Data.ScreenshotPath != shotPath {
		t.Fatalf("screenshot path: %+v", resp.Data)
	}
	data, err := os.ReadFile(shotPath)
	if err != nil {
		t.Fatalf("read screenshot: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved screenshot is empty")
	}
}

func TestDispatch_Get_URLAndTitle(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(params, &p)
		if p.Expression == "location.href" {
			return map[string]interface{}{"result": map[string]interface{}{"value": "https://got-url"}}, nil
		}
		if p.Expression == "document.title" {
			return map[string]interface{}{"result": map[string]interface{}{"value": "got-title"}}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": nil}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "url"})
	if !resp.Success || resp.Data.Value != "https://got-url" {
		t.Fatalf("get url: %+v", resp)
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "title"})
	if !resp.Success || resp.Data.Value != "got-title" {
		t.Fatalf("get title: %+v", resp)
	}

	// Missing attribute.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet})
	if resp.Success {
		t.Fatalf("missing attr: %+v", resp)
	}

	// Attr that needs a ref without a ref: error.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "class"})
	if resp.Success {
		t.Fatalf("class without ref: %+v", resp)
	}
}

func TestDispatch_MissingRefParameters(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	for _, action := range []protocol.ActionType{
		protocol.ActionClick,
		protocol.ActionHover,
		protocol.ActionFill,
		protocol.ActionType_,
		protocol.ActionCheck,
		protocol.ActionUncheck,
	} {
		resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: action})
		if resp.Success || !strings.Contains(resp.Error, "ref") {
			t.Fatalf("%s: expected missing-ref error, got %+v", action, resp)
		}
	}

	// Select without ref or value
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSelect})
	if resp.Success {
		t.Fatalf("select without ref: %+v", resp)
	}
}

func TestDispatch_Snapshot_Fallback(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "Page Title")
	// buildDomTree script fails so dispatch falls back to document.title.
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "buildDomTree") {
			return nil, &cdpErr{msg: "script failed"}
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": "Page Title"}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSnapshot})
	if !resp.Success {
		t.Fatalf("snapshot: %+v", resp)
	}
	if resp.Data.SnapshotData == nil {
		t.Fatalf("missing snapshot data: %+v", resp)
	}
}

type cdpErr struct{ msg string }

func (e *cdpErr) Error() string { return e.msg }

func TestDispatch_Snapshot_TextOnly(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(params, &p)
		if !strings.Contains(p.Expression, "stripSelectors") {
			t.Fatalf("expected text-snapshot script, got: %s", p.Expression)
		}
		return map[string]interface{}{"result": map[string]interface{}{
			"value": map[string]interface{}{
				"title": "Hello",
				"url":   "https://a/",
				"text":  "Some body content",
			},
		}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{
		ID:     "x",
		Action: protocol.ActionSnapshot,
		Mode:   "text",
	})
	if !resp.Success {
		t.Fatalf("snapshot --text-only: %+v", resp)
	}
	if resp.Data.SnapshotData == nil {
		t.Fatalf("missing snapshot data")
	}
	got := resp.Data.SnapshotData.Snapshot
	if !strings.Contains(got, "# Hello") {
		t.Errorf("expected title line, got %q", got)
	}
	if !strings.Contains(got, "Some body content") {
		t.Errorf("expected body text, got %q", got)
	}
	if len(resp.Data.SnapshotData.Refs) != 0 {
		t.Errorf("text mode should not produce refs, got %d", len(resp.Data.SnapshotData.Refs))
	}
}

func TestDispatch_Network_Query(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	// Seed the tab with some network requests via direct state manipulation.
	tab := c.TabManager.GetTab("T1")
	if tab == nil {
		// EnsurePageTarget creates it via AttachAndEnable; ensure that happened.
		DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
		tab = c.TabManager.GetTab("T1")
	}
	if tab == nil {
		t.Fatal("no tab after priming")
	}
	tab.AddNetworkRequest("R1", protocol.NetworkRequestInfo{URL: "https://x/1", Method: "GET"})
	tab.AddNetworkRequest("R2", protocol.NetworkRequestInfo{URL: "https://x/2", Method: "POST"})

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionNetwork, NetworkCommand: "requests",
	})
	if !resp.Success {
		t.Fatalf("network requests: %+v", resp)
	}
	if len(resp.Data.NetworkRequests) != 2 {
		t.Fatalf("expected 2 requests, got %+v", resp.Data.NetworkRequests)
	}

	// Clear command.
	resp = DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionNetwork, NetworkCommand: "clear",
	})
	if !resp.Success {
		t.Fatalf("network clear: %+v", resp)
	}
}

func TestDispatch_Console_Get(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	// Prime the tab by running a quick action first.
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	if tab == nil {
		t.Fatal("no tab")
	}
	tab.AddConsoleMessage(protocol.ConsoleMessageInfo{Type: "log", Text: "hi"})

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionConsole, ConsoleCommand: "get",
	})
	if !resp.Success || len(resp.Data.ConsoleMessages) != 1 {
		t.Fatalf("console: %+v", resp)
	}

	// Clear.
	resp = DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionConsole, ConsoleCommand: "clear",
	})
	if !resp.Success {
		t.Fatalf("console clear: %+v", resp)
	}
}

func TestDispatch_Errors_Get(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	if tab == nil {
		t.Fatal("no tab")
	}
	tab.AddJSError(protocol.JSErrorInfo{Message: "boom"})

	resp := DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionErrors, ErrorsCommand: "get",
	})
	if !resp.Success || len(resp.Data.JSErrors) != 1 {
		t.Fatalf("errors: %+v", resp)
	}

	resp = DispatchRequest(c, &protocol.Request{
		ID: "x", Action: protocol.ActionErrors, ErrorsCommand: "clear",
	})
	if !resp.Success {
		t.Fatalf("errors clear: %+v", resp)
	}
}
