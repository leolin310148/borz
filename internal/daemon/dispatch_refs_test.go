package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
			"result": map[string]interface{}{"value": map[string]interface{}{"x": 12.5, "y": 20.0, "ok": true}},
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

func TestDispatch_Click_PropagatesMouseError(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Input.dispatchMouseEvent", func(params json.RawMessage) (interface{}, error) {
		var event struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(params, &event)
		if event.Type == "mousePressed" {
			return nil, errors.New("synthetic input rejected")
		}
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "1"})
	if resp.Success || !strings.Contains(resp.Error, "press mouse button: synthetic input rejected") {
		t.Fatalf("click mouse error = %+v", resp)
	}
}

func TestDispatch_Click_ReportsCoveredElement(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	var pointScript string
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		var call struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &call)
		pointScript = call.FunctionDeclaration
		return map[string]interface{}{
			"result": map[string]interface{}{},
			"exceptionDetails": map[string]interface{}{
				"text": "Uncaught",
				"exception": map[string]interface{}{
					"description": "Error: Element is not clickable at its center; hit div.overlay instead of button#save",
				},
			},
		}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "1"})
	if resp.Success || !strings.Contains(resp.Error, "div.overlay instead of button#save") {
		t.Fatalf("covered click response = %+v", resp)
	}
	if !strings.Contains(pointScript, "borz press Escape") || !strings.Contains(pointScript, "fresh snapshot") {
		t.Fatalf("covered click script lacks an actionable overlay hint: %s", pointScript)
	}
}

func TestDispatch_Click_FocusesHiddenXtermInput(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		if strings.Contains(string(params), "isTerminalInput") {
			return map[string]interface{}{
				"result": map[string]interface{}{"value": map[string]interface{}{"focusOnly": true}},
			}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": true}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "0", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "textbox"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionClick, Ref: "0"})
	if !resp.Success {
		t.Fatalf("hidden terminal click = %+v", resp)
	}
	var focused bool
	var mouseEvents int
	for _, call := range f.Calls() {
		if call.Method == "DOM.focus" {
			focused = true
		}
		if call.Method == "Input.dispatchMouseEvent" {
			mouseEvents++
		}
	}
	if !focused || mouseEvents != 0 {
		t.Fatalf("hidden terminal focus=%v mouseEvents=%d calls=%+v", focused, mouseEvents, f.Calls())
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
	if resp.Data.Value != "" {
		t.Fatalf("value: %q", resp.Data.Value)
	}
	frameworkEvents := false
	for _, call := range f.Calls() {
		if call.Method == "Runtime.callFunctionOn" &&
			strings.Contains(string(call.Params), "Object.getOwnPropertyDescriptor") &&
			strings.Contains(string(call.Params), "InputEvent") &&
			strings.Contains(string(call.Params), "change") {
			frameworkEvents = true
		}
	}
	if !frameworkEvents {
		t.Fatalf("fill did not use native value setter with framework events, calls=%+v", f.Calls())
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

func TestDispatch_CheckRejectsUnchangedCustomControl(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		if strings.Contains(string(params), "async function(desired)") {
			return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{
				"ok": false, "checked": false, "error": "checkbox state remained false after interaction",
			}}}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{"x": 12.5, "y": 20.0, "ok": true}}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionCheck, Ref: "1"})
	if resp.Success || !strings.Contains(resp.Error, "state remained false") {
		t.Fatalf("unchanged checkbox response = %+v", resp)
	}
}

func TestDispatch_SelectRejectsWrongElementAndUnknownValue(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		message := "element is not a select"
		if strings.Contains(string(params), `"value":"missing"`) {
			message = "select value not found: missing"
		}
		return map[string]interface{}{
			"result": map[string]interface{}{"value": map[string]interface{}{"ok": false, "error": message}},
		}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	wrongType := DispatchRequest(c, &protocol.Request{ID: "wrong-type", Action: protocol.ActionSelect, Ref: "1", Value: "green"})
	if wrongType.Success || !strings.Contains(wrongType.Error, "not a select") {
		t.Fatalf("select wrong element type = %+v", wrongType)
	}

	invalidValue := DispatchRequest(c, &protocol.Request{ID: "invalid-value", Action: protocol.ActionSelect, Ref: "1", Value: "missing"})
	if invalidValue.Success || !strings.Contains(invalidValue.Error, "select value not found: missing") {
		t.Fatalf("select invalid value = %+v", invalidValue)
	}
}

func TestDispatch_SelectSupportsAndVerifiesCustomCombobox(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	var selectScript string
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		var call struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &call)
		selectScript = call.FunctionDeclaration
		return map[string]interface{}{
			"result": map[string]interface{}{"value": map[string]interface{}{"ok": true}},
		}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "76", &protocol.RefInfo{BackendDOMNodeID: 10, Role: "combobox"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSelect, Ref: "76", Value: "qwen/qwen3.6-35b-a3b"})
	if !resp.Success {
		t.Fatalf("custom combobox select = %+v", resp)
	}
	for _, want := range []string{"aria-controls", "el-select-dropdown__item", "aria-selected", "selection did not change"} {
		if !strings.Contains(selectScript, want) {
			t.Fatalf("custom select script missing %q: %s", want, selectScript)
		}
	}
}

func TestDispatch_Upload(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "a.txt")
	file2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(file1, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"objectId": "FILE1"}}, nil
	})
	f.On("DOM.describeNode", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"node": map[string]interface{}{"backendNodeId": 99}}, nil
	})
	var sawFiles []interface{}
	var sawBackendID int
	f.On("DOM.setFileInputFiles", func(raw json.RawMessage) (interface{}, error) {
		var params struct {
			BackendNodeID int           `json:"backendNodeId"`
			Files         []interface{} `json:"files"`
		}
		json.Unmarshal(raw, &params)
		sawFiles = params.Files
		sawBackendID = params.BackendNodeID
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)

	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "1", Files: []string{file1, file2}})
	if !resp.Success {
		t.Fatalf("upload: %+v", resp)
	}
	if len(sawFiles) != 2 {
		t.Fatalf("CDP received %d files, want 2", len(sawFiles))
	}
	if sawBackendID != 99 {
		t.Fatalf("CDP received backend node %d, want resolved file input 99", sawBackendID)
	}

	// Missing ref -> fail.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Files: []string{file1}})
	if resp.Success || !strings.Contains(resp.Error, "missing ref") {
		t.Fatalf("missing ref: %+v", resp)
	}

	// Missing files -> fail.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "1"})
	if resp.Success || !strings.Contains(resp.Error, "missing files") {
		t.Fatalf("missing files: %+v", resp)
	}

	// File that doesn't exist -> stat error.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "1", Files: []string{filepath.Join(dir, "does-not-exist")}})
	if resp.Success || !strings.Contains(resp.Error, "stat") {
		t.Fatalf("missing file should fail: %+v", resp)
	}

	// Directory rejected.
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "1", Files: []string{dir}})
	if resp.Success || !strings.Contains(resp.Error, "directory") {
		t.Fatalf("directory should fail: %+v", resp)
	}

	// Unknown ref -> fail (after files validate).
	resp = DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "ghost", Files: []string{file1}})
	if resp.Success || !strings.Contains(resp.Error, "unknown ref") {
		t.Fatalf("unknown ref: %+v", resp)
	}
}

func TestDispatch_UploadRejectsRefWithoutAssociatedFileInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"type": "object", "subtype": "null"}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 10, Role: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionUpload, Ref: "1", Files: []string{file}})
	if resp.Success || !strings.Contains(resp.Error, "associated <label>") {
		t.Fatalf("unassociated upload ref = %+v", resp)
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

func TestDispatch_GetValueReadsLiveProperty(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	var functionDeclaration string
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		var call struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &call)
		functionDeclaration = call.FunctionDeclaration
		return map[string]interface{}{"result": map[string]interface{}{"value": "live textarea value"}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "13", &protocol.RefInfo{BackendDOMNodeID: 10})

	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionGet, Attribute: "value", Ref: "13"})
	if !resp.Success || resp.Data.Value != "live textarea value" {
		t.Fatalf("get live value = %+v", resp)
	}
	if !strings.Contains(functionDeclaration, "'value' in this") || !strings.Contains(functionDeclaration, "this.getValue") {
		t.Fatalf("get value does not read DOM/framework property: %s", functionDeclaration)
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
	if !strings.Contains(resp.Error, "stale because the page or DOM changed") || !strings.Contains(resp.Error, "run snapshot again") {
		t.Fatalf("stale ref error is not actionable: %q", resp.Error)
	}
}

func TestDispatch_ResolveByXPath_RebindsUniqueDynamicPortalRef(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(&buildDomTreeResult{
		RootID: "root",
		Map: map[string]json.RawMessage{
			"root": mustRaw(t, rawDomElementNode{TagName: "body", XPath: "/html/body", Children: []string{"upload"}}),
			"upload": mustRaw(t, rawDomElementNode{
				TagName: "button", XPath: "/html/body/div[4]/ul/li[2]/button", HighlightIndex: intPtr(19),
				Attributes: map[string]string{"role": "menuitem", "borz-rendered-name": "檔案上傳"},
			}),
		},
	}))
	var searched []string
	f.On("DOM.performSearch", func(params json.RawMessage) (interface{}, error) {
		var request struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(params, &request)
		searched = append(searched, request.Query)
		if strings.Contains(request.Query, "li[3]") {
			return map[string]interface{}{"searchId": "OLD", "resultCount": 0}, nil
		}
		return map[string]interface{}{"searchId": "NEW", "resultCount": 1}, nil
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
	seedRef(c, "T1", "19", &protocol.RefInfo{
		XPath: "/html/body/div[4]/ul/li[3]/button", Role: "menuitem", Name: "檔案上傳", TagName: "button",
	})

	resp := DispatchRequest(c, &protocol.Request{ID: "click", Action: protocol.ActionClick, Ref: "19"})
	if !resp.Success {
		t.Fatalf("dynamic portal click should rebind its fresh semantic ref: %+v", resp)
	}
	if got := c.TabManager.GetTab("T1").Refs["19"].BackendDOMNodeID; got != 321 {
		t.Fatalf("rebound backend node id = %d, want 321", got)
	}
	if len(searched) != 2 || !strings.Contains(searched[0], "li[3]") || !strings.Contains(searched[1], "li[2]") {
		t.Fatalf("XPath searches = %#v, want stale absolute path then relocated semantic path", searched)
	}
}

func TestDispatch_ResolveByXPath_RejectsAmbiguousSemanticFallback(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(&buildDomTreeResult{
		RootID: "root",
		Map: map[string]json.RawMessage{
			"root": mustRaw(t, rawDomElementNode{TagName: "body", XPath: "/html/body", Children: []string{"a", "b"}}),
			"a":    mustRaw(t, rawDomElementNode{TagName: "button", XPath: "/html/body/div[1]/button", HighlightIndex: intPtr(1), Attributes: map[string]string{"role": "menuitem", "aria-label": "Upload"}}),
			"b":    mustRaw(t, rawDomElementNode{TagName: "button", XPath: "/html/body/div[2]/button", HighlightIndex: intPtr(2), Attributes: map[string]string{"role": "menuitem", "aria-label": "Upload"}}),
		},
	}))
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"searchId": "OLD", "resultCount": 0}, nil
	})
	f.On("DOM.discardSearchResults", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	seedRef(c, "T1", "7", &protocol.RefInfo{XPath: "/gone", Role: "menuitem", Name: "Upload", TagName: "button"})

	_, err := parseRef(c, "T1", tab, "7")
	if err == nil || !strings.Contains(err.Error(), "semantic fallback found 2 exact matches") {
		t.Fatalf("ambiguous semantic fallback error = %v", err)
	}
}

func TestDispatch_Click_NewChildTabReconcilesHitTargetRace(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})

	var targetReads atomic.Int32
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		targets := []interface{}{map[string]interface{}{"targetId": "T1", "type": "page", "url": "https://a", "title": "A"}}
		// dispatchAction resolves the source target first, then captures the
		// click baseline; expose the child only on the post-error read.
		if targetReads.Add(1) >= 3 {
			targets = append(targets, map[string]interface{}{"targetId": "WORD", "type": "page", "url": "https://word.example/doc", "title": "Word", "openerId": "T1"})
		}
		return map[string]interface{}{"targetInfos": targets}, nil
	})
	f.On("Runtime.callFunctionOn", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{
			"result": map[string]interface{}{},
			"exceptionDetails": map[string]interface{}{
				"text":      "Uncaught",
				"exception": map[string]interface{}{"description": "Error: Element is not clickable at its center; hit span instead of button"},
			},
		}, nil
	})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "menuitem", Name: "Open in browser", TagName: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "click", Action: protocol.ActionClick, Ref: "1"})
	if !resp.Success {
		t.Fatalf("new child page should reconcile click hit-target race: %+v", resp)
	}
	for _, call := range f.Calls() {
		if call.Method == "Input.dispatchMouseEvent" {
			t.Fatalf("click dispatched duplicate mouse input after new-tab outcome: %+v", f.Calls())
		}
	}
}

func TestDispatch_Click_RebindsOnceWhenResolvedPortalNodeMovedUnderOverlay(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(&buildDomTreeResult{
		RootID: "root",
		Map: map[string]json.RawMessage{
			"root": mustRaw(t, rawDomElementNode{TagName: "body", XPath: "/html/body", Children: []string{"current"}}),
			"current": mustRaw(t, rawDomElementNode{
				TagName: "button", XPath: "/html/body/div[5]/button", HighlightIndex: intPtr(9),
				Attributes: map[string]string{"role": "menuitem", "aria-label": "Open in browser"},
			}),
		},
	}))
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"searchId": "CURRENT", "resultCount": 1}, nil
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
	var pointCalls atomic.Int32
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		var call struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &call)
		if strings.Contains(call.FunctionDeclaration, "semanticIdentity") {
			if pointCalls.Add(1) == 1 {
				return map[string]interface{}{
					"result": map[string]interface{}{},
					"exceptionDetails": map[string]interface{}{
						"text":      "Uncaught",
						"exception": map[string]interface{}{"description": "Error: Element is not clickable at its center; hit a#header instead of button"},
					},
				}, nil
			}
			return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{"x": 40.0, "y": 60.0}}}, nil
		}
		// Do not start the optional element-level fallback probe in this test.
		return map[string]interface{}{"result": map[string]interface{}{"value": false}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "9", &protocol.RefInfo{
		BackendDOMNodeID: 111, XPath: "/html/body/div[3]/ul/li[3]/button",
		Role: "menuitem", Name: "Open in browser", TagName: "button",
	})

	resp := DispatchRequest(c, &protocol.Request{ID: "click", Action: protocol.ActionClick, Ref: "9"})
	if !resp.Success {
		t.Fatalf("moved portal generation should be rebound and hit-tested once more: %+v", resp)
	}
	if pointCalls.Load() != 2 {
		t.Fatalf("interactable point calls = %d, want initial failure plus one retry", pointCalls.Load())
	}
	if got := c.TabManager.GetTab("T1").Refs["9"].BackendDOMNodeID; got != 321 {
		t.Fatalf("recovered backend node id = %d, want 321", got)
	}
}

func TestClickTargetBaseline_IgnoresUnrelatedNewPage(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	c := connectCdp(t, f)
	baseline := captureClickTargetBaseline(c, "T1")
	f.On("Target.getTargets", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"targetInfos": []interface{}{
			map[string]interface{}{"targetId": "T1", "type": "page"},
			map[string]interface{}{"targetId": "OTHER", "type": "page", "openerId": "T2"},
		}}, nil
	})
	if baseline.openedChildPage(c, 0) {
		t.Fatal("unrelated new page must not mask a click failure")
	}
}

func TestDispatch_Click_RetargetedControlSkipsDetachedNodeProbe(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	setupRefHandlers(f)
	f.On("Runtime.callFunctionOn", func(params json.RawMessage) (interface{}, error) {
		var call struct {
			FunctionDeclaration string `json:"functionDeclaration"`
		}
		_ = json.Unmarshal(params, &call)
		if !strings.Contains(call.FunctionDeclaration, "semanticIdentity") || !strings.Contains(call.FunctionDeclaration, "sameGeometry") {
			t.Fatalf("interactable-point check lacks exact-semantic, same-geometry retargeting: %s", call.FunctionDeclaration)
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{"x": 12.5, "y": 20.0, "retargeted": true}}}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	seedRef(c, "T1", "1", &protocol.RefInfo{BackendDOMNodeID: 42, Role: "menuitem", Name: "Upload", TagName: "button"})

	resp := DispatchRequest(c, &protocol.Request{ID: "click", Action: protocol.ActionClick, Ref: "1"})
	if !resp.Success {
		t.Fatalf("retargeted exact-semantic control click: %+v", resp)
	}
	var runtimeCalls int
	for _, call := range f.Calls() {
		if call.Method == "Runtime.callFunctionOn" {
			runtimeCalls++
		}
	}
	if runtimeCalls != 1 {
		t.Fatalf("detached-node probe/fallback should be skipped; Runtime.callFunctionOn calls = %d", runtimeCalls)
	}
}

func TestDispatch_SnapshotScopesToCSSRoot(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"result": map[string]interface{}{"value": map[string]interface{}{
			"rootId": "panel", "rootSelectorMatched": true,
			"map": map[string]interface{}{
				"panel": map[string]interface{}{"tagName": "section", "children": []string{"save"}, "attributes": map[string]string{"data-testid": "node-settings-panel"}},
				"save":  map[string]interface{}{"tagName": "button", "xpath": "/html/body/section/button", "children": []string{}, "highlightIndex": 7, "attributes": map[string]string{"aria-label": "Save node"}},
			},
		}}}, nil
	})
	c := connectCdp(t, f)
	resp := DispatchRequest(c, &protocol.Request{ID: "x", Action: protocol.ActionSnapshot, Interactive: true, Selector: "[data-testid=node-settings-panel]"})
	if !resp.Success || resp.Data.SnapshotData.Refs["7"] == nil {
		t.Fatalf("CSS-scoped snapshot should retain descendants that do not contain selector text: %+v", resp)
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
