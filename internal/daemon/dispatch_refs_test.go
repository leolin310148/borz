package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

// setupRefHandlers registers handlers that let ref-based actions succeed:
// DOM.resolveNode returns an objectId; Runtime.callFunctionOn returns a
// point for click/hover or "true" for other shapes. Specific tests override
// handlers as needed.
func setupRefHandlers(f *fakeCDP) {
	f.On("DOM.resolveNode", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"object": map[string]interface{}{"objectId": "OBJ1"}}, nil
	})
	f.On("Runtime.callFunctionOn", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"value": map[string]interface{}{"x": 12.5, "y": 20.0}},
		}, nil
	})
	f.On("Input.dispatchMouseEvent", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("Input.dispatchKeyEvent", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("Input.insertText", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("DOM.focus", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
}

// seedRef pre-populates a tab's Refs map as if a snapshot had run.
func seedRef(c *CdpConnection, targetID, ref string, info *protocol.RefInfo) {
	tab := c.TabManager.GetTab(targetID)
	if tab == nil {
		tab = c.TabManager.AddTab(targetID)
	}
	if tab.Refs == nil {
		tab.Refs = map[string]*protocol.RefInfo{}
	}
	tab.Refs[ref] = info
}

func TestDispatch_Click_WithRef(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)

	// Prime the tab (EnsurePageTarget populates TabManager).
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "1"})
	if !resp.Success {
		t.Fatalf("click: %+v", resp)
	}
}

func TestDispatch_Hover_WithRef(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 7})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionHover, Ref: "1"})
	if !resp.Success {
		t.Fatalf("hover: %+v", resp)
	}
}

func TestDispatch_Fill_And_Type(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionFill, Ref: "1", Text: "hello"})
	if !resp.Success {
		t.Fatalf("fill: %+v", resp)
	}
	if resp.Data.Value != "hello" {
		t.Fatalf("value: %q", resp.Data.Value)
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionType_, Ref: "1", Text: "more"})
	if !resp.Success {
		t.Fatalf("type: %+v", resp)
	}

	// Fill with empty text exercises the no-insertText branch of insertTextIntoNode.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionFill, Ref: "1", Text: ""})
	if !resp.Success {
		t.Fatalf("fill empty: %+v", resp)
	}
}

func TestDispatch_Check_Uncheck_Select(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionCheck, Ref: "1"})
	if !resp.Success {
		t.Fatalf("check: %+v", resp)
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUncheck, Ref: "1"})
	if !resp.Success {
		t.Fatalf("uncheck: %+v", resp)
	}

	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSelect, Ref: "1", Value: "opt2"})
	if !resp.Success {
		t.Fatalf("select: %+v", resp)
	}
}

func TestDispatch_Get_WithRef(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	// getAttributeValue uses Runtime.callFunctionOn with returnByValue:true
	// and expects result.value to be the attribute value string.
	f.On("Runtime.callFunctionOn", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{"value": "attr-value"},
		}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "class", Ref: "1"})
	if !resp.Success || resp.Data.Value != "attr-value" {
		t.Fatalf("get class: %+v", resp)
	}

	// Also cover the "text" attribute branch.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "text", Ref: "1"})
	if !resp.Success {
		t.Fatalf("get text: %+v", resp)
	}
}

func TestDispatch_ParseRef_Unknown(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})

	// Unknown ref (no Refs map entry) -> fail.
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "ghost"})
	if resp.Success || !strings.Contains(resp.Error, "unknown ref") {
		t.Fatalf("unknown ref: %+v", resp)
	}
}

func TestDispatch_ResolveByXPath(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	// parseRef with XPath but zero BackendDOMNodeID triggers resolveBackendNodeIDByXPath.
	f.On("DOM.getDocument", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"root": map[string]interface{}{"nodeId": 1}}, nil
	})
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"searchId": "S1", "resultCount": 1}, nil
	})
	f.On("DOM.getSearchResults", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"nodeIds": []int{99}}, nil
	})
	f.On("DOM.describeNode", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"node": map[string]interface{}{"backendNodeId": 321}}, nil
	})
	f.On("DOM.discardSearchResults", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{XPath: "/html/body/button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "1"})
	if !resp.Success {
		t.Fatalf("click via xpath: %+v", resp)
	}
	// After success, the ref should be memoized with the resolved backend id.
	tab := c.TabManager.GetTab("T1")
	if tab.Refs["1"].BackendDOMNodeID != 321 {
		t.Fatalf("backend id not memoized: %+v", tab.Refs["1"])
	}
}

func TestDispatch_ResolveByXPath_NoResults(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"searchId": "S1", "resultCount": 0}, nil
	})
	f.On("DOM.discardSearchResults", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{XPath: "/ghost"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "1"})
	if resp.Success {
		t.Fatalf("expected failure: %+v", resp)
	}
}

func TestDispatch_ClipboardRead(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Browser.grantPermissions", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("Page.bringToFront", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"value": "clipped"}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClipboardRead})
	if !resp.Success || resp.Data.Value != "clipped" {
		t.Fatalf("clipboard: %+v", resp)
	}
}

func TestDispatch_Snapshot_BuildDomTree(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	// Return a minimal valid buildDomTree result.
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		tree := map[string]interface{}{
			"rootId": "r",
			"map": map[string]interface{}{
				"r": map[string]interface{}{"tagName": "body", "xpath": "/html/body", "children": []string{"b"}},
				"b": map[string]interface{}{
					"tagName":        "button",
					"xpath":          "/html/body/button",
					"children":       []string{},
					"highlightIndex": 1,
					"attributes":     map[string]string{"aria-label": "Go"},
				},
			},
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": tree}}, nil
	})
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSnapshot})
	if !resp.Success {
		t.Fatalf("snapshot: %+v", resp)
	}
	if resp.Data.SnapshotData == nil || len(resp.Data.SnapshotData.Refs) == 0 {
		t.Fatalf("expected refs in snapshot: %+v", resp.Data.SnapshotData)
	}
}
