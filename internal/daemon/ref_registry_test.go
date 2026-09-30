package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

// registryEvaluate answers the ref-registry lookup expression with a remote
// object (or a status string) and every other Runtime.evaluate with the
// default ready document.
func registryEvaluate(status string) fakeHandler {
	return func(params json.RawMessage) (interface{}, error) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "globalThis[") && strings.Contains(p.Expression, "registry.elements.get(") {
			if status == "" {
				return map[string]interface{}{"result": map[string]interface{}{"type": "object", "subtype": "node", "objectId": "REG1"}}, nil
			}
			return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": status}}, nil
		}
		return map[string]interface{}{"result": map[string]interface{}{"type": "string", "value": `{"readyState":"complete","href":"https://ready.test/"}`}}, nil
	}
}

func TestParseRef_ResolvesExactNodeFromRegistry(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", registryEvaluate(""))
	f.On("DOM.describeNode", func(json.RawMessage) (interface{}, error) {
		return map[string]interface{}{"node": map[string]interface{}{"backendNodeId": 555}}, nil
	})
	f.On("Runtime.releaseObject", func(json.RawMessage) (interface{}, error) { return map[string]interface{}{}, nil })
	searched := 0
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		searched++
		return map[string]interface{}{"searchId": "S", "resultCount": 0}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	tab.RefToken = "tok"
	seedRef(c, "T1", "3", &protocol.RefInfo{XPath: "li[2]/a", Role: "tab", Name: "Parts", TagName: "a"})

	got, err := parseRef(c, "T1", tab, "3", "")
	if err != nil || got != 555 {
		t.Fatalf("parseRef = %d, %v; want 555 from the registry", got, err)
	}
	if searched != 0 {
		t.Fatalf("registry hit must not fall back to XPath search (%d searches)", searched)
	}
	if tab.Refs["3"].BackendDOMNodeID != 0 {
		t.Fatal("registry resolutions must not be cached; the node can detach later")
	}
	if len(callsTo(f, "Runtime.releaseObject")) != 1 {
		t.Fatal("registry lookup must release its remote object")
	}
}

func TestParseRef_DetachedNodeIgnoresPositionalXPath(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	f.On("Runtime.evaluate", registryEvaluate("detached"))
	searched := 0
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		searched++
		return map[string]interface{}{"searchId": "S", "resultCount": 1}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	tab.RefToken = "tok"
	// Unnamed: semantic fallback cannot rebind it either.
	seedRef(c, "T1", "4", &protocol.RefInfo{XPath: "html/body/div[2]/button", Role: "button", TagName: "button"})

	_, err := parseRef(c, "T1", tab, "4", "")
	if err == nil || !strings.Contains(err.Error(), "the snapshotted element was removed from the page") {
		t.Fatalf("detached ref error = %v", err)
	}
	if searched != 0 {
		t.Fatal("a detached ref must not rebind to whatever now sits at its old XPath")
	}
}

func TestParseRef_ShadowRelativeXPathIsNotSearched(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	searched := 0
	f.On("DOM.performSearch", func(json.RawMessage) (interface{}, error) {
		searched++
		return map[string]interface{}{"searchId": "S", "resultCount": 1}, nil
	})
	c := connectCdp(t, f)
	DispatchRequest(c, &protocol.Request{ID: "prime", Action: protocol.ActionBack})
	tab := c.TabManager.GetTab("T1")
	seedRef(c, "T1", "5", &protocol.RefInfo{XPath: "li[2]/a", Role: "tab", TagName: "a"})

	_, err := parseRef(c, "T1", tab, "5", "")
	if err == nil || !strings.Contains(err.Error(), "relative to a shadow root or frame") {
		t.Fatalf("shadow XPath error = %v", err)
	}
	if searched != 0 {
		t.Fatal("a shadow-relative XPath must never be searched against the top document")
	}
}

func TestIsDocumentXPath(t *testing.T) {
	for xpath, want := range map[string]bool{
		"html":                  true,
		"html/body/div[2]":      true,
		"/html/body/button":     true,
		"li[2]/a":               false,
		"button":                false,
		"":                      false,
		"htmlish/body":          false,
		"div/html/body/section": false,
	} {
		if got := isDocumentXPath(xpath); got != want {
			t.Errorf("isDocumentXPath(%q) = %v, want %v", xpath, got, want)
		}
	}
}

func TestUnknownRefError(t *testing.T) {
	tab := &TabState{Refs: map[string]*protocol.RefInfo{"0": {}, "7": {}, "12": {}}}

	if err := unknownRefError(&TabState{RefInvalidationReason: "the page navigated"}, "3", "s1"); !strings.Contains(err.Error(), "invalidated because the page navigated") {
		t.Fatalf("invalidated: %v", err)
	}
	if err := unknownRefError(tab, "#submit", "s1"); !strings.Contains(err.Error(), "not CSS selectors") {
		t.Fatalf("selector: %v", err)
	}
	if err := unknownRefError(&TabState{}, "3", "s1"); !strings.Contains(err.Error(), "no snapshot for this tab") {
		t.Fatalf("no snapshot: %v", err)
	}

	tab.RefSnapshotAt = time.Now().Add(-5 * time.Second)
	tab.RefSnapshotSession = "tmux-2"
	err := unknownRefError(tab, "99", "tmux-1")
	for _, want := range []string{"another session (tmux-2)", "refs 0-12 (3 total)", "concurrent agents"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("other session error missing %q: %v", want, err)
		}
	}

	tab.RefSnapshotSession = "tmux-1"
	err = unknownRefError(tab, "99", "tmux-1")
	if !strings.Contains(err.Error(), "by this session") || !strings.Contains(err.Error(), "--selector, --role, or --limit") {
		t.Fatalf("same session error: %v", err)
	}
	tab.RefSnapshotSession = ""
	if err := unknownRefError(tab, "99", ""); !strings.Contains(err.Error(), "a caller without a session id") {
		t.Fatalf("no session error: %v", err)
	}
	tab.Refs = map[string]*protocol.RefInfo{}
	if err := unknownRefError(tab, "99", "x"); !strings.Contains(err.Error(), "no refs") {
		t.Fatalf("empty refs: %v", err)
	}
}

func TestGetName_LabelBeatsValueAndTitle(t *testing.T) {
	el := rawDomElementNode{TagName: "input", Attributes: map[string]string{"borz-label-name": "Account Name", "title": "T", "value": ""}}
	if got := getName(el, nil); got != "Account Name" {
		t.Fatalf("label name = %q", got)
	}
	el.Attributes["aria-label"] = "Explicit"
	if got := getName(el, nil); got != "Explicit" {
		t.Fatalf("aria-label must win, got %q", got)
	}
}

func TestSnapshot_RecordsRegistryTokenAndSession(t *testing.T) {
	f := newFakeCDP(t)
	setupOnePage(f, "T1", "https://a", "A")
	tree := &buildDomTreeResult{
		RootID:   "root",
		RefToken: "tok-1",
		Map: map[string]json.RawMessage{
			"root": mustRaw(t, rawDomElementNode{TagName: "body", XPath: "html/body", Children: []string{"b"}}),
			"b":    mustRaw(t, rawDomElementNode{TagName: "button", XPath: "html/body/button", HighlightIndex: intPtr(0), Attributes: map[string]string{"aria-label": "Go"}, IsVisible: true}),
		},
	}
	f.On("Runtime.evaluate", fakeBuildDomTreeSequence(tree))
	c := connectCdp(t, f)

	resp := DispatchRequest(c, &protocol.Request{ID: "s", Action: protocol.ActionSnapshot, Interactive: true, SessionID: "tmux-9"})
	if !resp.Success {
		t.Fatalf("snapshot: %+v", resp)
	}
	var params struct {
		Expression string `json:"expression"`
	}
	for _, call := range callsTo(f, "Runtime.evaluate") {
		_ = json.Unmarshal(call.Params, &params)
		if strings.Contains(params.Expression, "buildDomTree") {
			break
		}
	}
	if !strings.Contains(params.Expression, `"refRegistryKey":"__borzRefs"`) {
		t.Fatal("snapshot must ask buildDomTree to register ref elements")
	}
	tab := c.TabManager.GetTab("T1")
	if tab.RefToken != "tok-1" || tab.RefSnapshotSession != "tmux-9" || tab.RefSnapshotAt.IsZero() {
		t.Fatalf("tab ref metadata: token=%q session=%q at=%v", tab.RefToken, tab.RefSnapshotSession, tab.RefSnapshotAt)
	}
}
