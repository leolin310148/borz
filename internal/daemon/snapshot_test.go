package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustRaw(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestParseNode_TextAndElement(t *testing.T) {
	textRaw := mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "hi", IsVisible: true})
	isText, tn, _ := parseNode(textRaw)
	if !isText || tn.Text != "hi" {
		t.Fatalf("expected text node, got isText=%v tn=%+v", isText, tn)
	}

	elRaw := mustRaw(t, rawDomElementNode{TagName: "BUTTON", XPath: "/html/body/button"})
	isText, _, el := parseNode(elRaw)
	if isText || el.TagName != "BUTTON" {
		t.Fatalf("expected element, got isText=%v el=%+v", isText, el)
	}

	// Non-JSON should not panic; returns zero element.
	isText, _, el = parseNode(json.RawMessage(`not json`))
	if isText || el.TagName != "" {
		t.Fatalf("garbage input: expected zero element, got %+v", el)
	}
}

func TestGetRole(t *testing.T) {
	if r := getRole(rawDomElementNode{TagName: "a"}); r != "link" {
		t.Fatalf("a: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "H2"}); r != "heading" {
		t.Fatalf("H2: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "div", Attributes: map[string]string{"role": "dialog"}}); r != "dialog" {
		t.Fatalf("explicit role: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "input", Attributes: map[string]string{"type": "email"}}); r != "textbox" {
		t.Fatalf("input email: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "input"}); r != "textbox" {
		t.Fatalf("input no type: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "input", Attributes: map[string]string{"type": "checkbox"}}); r != "checkbox" {
		t.Fatalf("input checkbox: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "input", Attributes: map[string]string{"type": "weird"}}); r != "textbox" {
		t.Fatalf("input unknown type: got %q", r)
	}
	if r := getRole(rawDomElementNode{TagName: "mycustom"}); r != "mycustom" {
		t.Fatalf("unknown tag: got %q", r)
	}
}

func TestGetName_Priority(t *testing.T) {
	// aria-label beats everything.
	if n := getName(rawDomElementNode{Attributes: map[string]string{"aria-label": "A", "title": "T"}}, nil); n != "A" {
		t.Fatalf("aria: got %q", n)
	}
	// title beats placeholder.
	if n := getName(rawDomElementNode{Attributes: map[string]string{"title": "T", "placeholder": "P"}}, nil); n != "T" {
		t.Fatalf("title: got %q", n)
	}
	// placeholder, then alt, then value, then name.
	if n := getName(rawDomElementNode{Attributes: map[string]string{"placeholder": "P"}}, nil); n != "P" {
		t.Fatalf("placeholder: got %q", n)
	}
	if n := getName(rawDomElementNode{Attributes: map[string]string{"alt": "A"}}, nil); n != "A" {
		t.Fatalf("alt: got %q", n)
	}
	if n := getName(rawDomElementNode{Attributes: map[string]string{"value": "V"}}, nil); n != "V" {
		t.Fatalf("value: got %q", n)
	}
	if n := getName(rawDomElementNode{Attributes: map[string]string{"name": "N"}}, nil); n != "N" {
		t.Fatalf("name fallback: got %q", n)
	}
	if n := getName(rawDomElementNode{Attributes: map[string]string{}}, nil); n != "" {
		t.Fatalf("nothing: got %q", n)
	}
}

func TestGetName_FromTextContent(t *testing.T) {
	// Build a parent element with one text child.
	textRaw := mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "  Click me  "})
	nodeMap := map[string]json.RawMessage{"1": textRaw}
	el := rawDomElementNode{Attributes: map[string]string{}, Children: []string{"1", "missing"}}
	if n := getName(el, nodeMap); n != "Click me" {
		t.Fatalf("text content: got %q", n)
	}
}

func TestCollectTextContent_DepthAndNesting(t *testing.T) {
	// Root -> Element -> Element -> Text. Depth limit excludes depth>limit.
	nodeMap := map[string]json.RawMessage{
		"txt":   mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "deep"}),
		"inner": mustRaw(t, rawDomElementNode{TagName: "span", Children: []string{"txt"}}),
	}
	root := rawDomElementNode{Children: []string{"inner"}}

	// depthLimit is measured from each child of root starting at depth 0.
	// child "inner" is depth 0, its child "txt" is depth 1. Limit 5 sees it.
	if got := collectTextContent(root, nodeMap, 5); got != "deep" {
		t.Fatalf("depth 5: got %q", got)
	}
	// depth 0 only lets us visit "inner" itself (not a text node, so nothing collected).
	if got := collectTextContent(root, nodeMap, 0); got != "" {
		t.Fatalf("depth 0 should not reach text: got %q", got)
	}
}

func TestTruncateText(t *testing.T) {
	if got := truncateText("short", 10); got != "short" {
		t.Fatalf("under: got %q", got)
	}
	if got := truncateText("exactly10!", 10); got != "exactly10!" {
		t.Fatalf("exact: got %q", got)
	}
	if got := truncateText("this is a long string", 10); got != "this is..." {
		t.Fatalf("over: got %q", got)
	}
}

func TestMatchesSelector(t *testing.T) {
	el := rawDomElementNode{
		TagName:    "BUTTON",
		XPath:      "/html/body/button[1]",
		Attributes: map[string]string{"class": "primary", "data-x": "foo"},
	}
	// Empty selector matches everything.
	if !matchesSelector(el, "button", "Submit", "") {
		t.Fatal("empty selector should match")
	}
	// Match by tag (case-insensitive).
	if !matchesSelector(el, "button", "Submit", "Button") {
		t.Fatal("tag selector should match")
	}
	// Match by role.
	if !matchesSelector(el, "button", "Submit", "button") {
		t.Fatal("role selector should match")
	}
	// Match by name.
	if !matchesSelector(el, "button", "Submit", "submit") {
		t.Fatal("name selector should match")
	}
	// Match by attribute value.
	if !matchesSelector(el, "button", "Submit", "primary") {
		t.Fatal("attr selector should match")
	}
	// No match.
	if matchesSelector(el, "button", "Submit", "nonsense") {
		t.Fatal("should not match")
	}
}

func TestMatchesRole(t *testing.T) {
	// Empty filter matches anything.
	if !matchesRole("button", "") {
		t.Fatal("empty filter should match")
	}
	// Exact match (case-insensitive).
	if !matchesRole("button", "Button") {
		t.Fatal("case-insensitive exact should match")
	}
	// Substring must NOT match (this is the whole point vs. selector).
	if matchesRole("textbox", "text") {
		t.Fatal("substring should not match")
	}
	if matchesRole("button", "link") {
		t.Fatal("different role should not match")
	}
}

func TestConvertBuildDomTreeResult_RoleFilter(t *testing.T) {
	hi := func(i int) *int { return &i }
	nodeMap := map[string]json.RawMessage{
		"btn":  mustRaw(t, rawDomElementNode{TagName: "button", XPath: "/btn", HighlightIndex: hi(1), Attributes: map[string]string{"aria-label": "Submit"}}),
		"link": mustRaw(t, rawDomElementNode{TagName: "a", XPath: "/link", HighlightIndex: hi(2), Attributes: map[string]string{"href": "/submit", "aria-label": "Submit via link"}}),
	}
	res := &buildDomTreeResult{RootID: "btn", Map: nodeMap}

	// Selector alone matches both (both have "submit" in name/attrs).
	out := ConvertBuildDomTreeResult(res, true, false, nil, "submit", "")
	if len(out.Elements) != 2 {
		t.Fatalf("selector-only should match 2, got %d: %+v", len(out.Elements), out.Elements)
	}

	// Role filter narrows to button only.
	out = ConvertBuildDomTreeResult(res, true, false, nil, "submit", "button")
	if len(out.Elements) != 1 || out.Elements[0].Role != "button" {
		t.Fatalf("role=button should narrow to 1 button, got %+v", out.Elements)
	}

	// Role filter is exact, not substring — "butt" must NOT match "button".
	out = ConvertBuildDomTreeResult(res, true, false, nil, "", "butt")
	if len(out.Elements) != 0 {
		t.Fatalf("partial role should match nothing, got %+v", out.Elements)
	}

	// Role filter works case-insensitively.
	out = ConvertBuildDomTreeResult(res, true, false, nil, "", "LINK")
	if len(out.Elements) != 1 || out.Elements[0].Role != "link" {
		t.Fatalf("case-insensitive role should match link, got %+v", out.Elements)
	}
}

func TestEl2XPath(t *testing.T) {
	if got := el2xpath(rawDomElementNode{XPath: "/foo"}); got != "/foo" {
		t.Fatalf("xpath present: got %q", got)
	}
	if got := el2xpath(rawDomElementNode{}); got != "" {
		t.Fatalf("xpath empty: got %q", got)
	}
}

func TestConvertBuildDomTreeResult_Nil(t *testing.T) {
	for name, in := range map[string]*buildDomTreeResult{
		"nil":     nil,
		"nil map": {RootID: "x", Map: nil},
		"no root": {RootID: "", Map: map[string]json.RawMessage{}},
	} {
		out := ConvertBuildDomTreeResult(in, false, false, nil, "", "")
		if out == nil || out.Snapshot != "" || len(out.Refs) != 0 {
			t.Fatalf("%s: expected empty snapshot, got %+v", name, out)
		}
	}
}

func TestConvertBuildDomTreeResult_InteractiveOnly(t *testing.T) {
	hi := func(i int) *int { return &i }
	nodeMap := map[string]json.RawMessage{
		"a": mustRaw(t, rawDomElementNode{TagName: "button", XPath: "/a", HighlightIndex: hi(2), Attributes: map[string]string{"aria-label": "Save"}}),
		"b": mustRaw(t, rawDomElementNode{TagName: "a", XPath: "/b", HighlightIndex: hi(1), Attributes: map[string]string{"title": "Home"}}),
		"c": mustRaw(t, rawDomElementNode{TagName: "div", XPath: "/c" /* no highlight -> skipped */}),
		"t": mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "ignored"}),
	}
	res := &buildDomTreeResult{RootID: "a", Map: nodeMap}
	out := ConvertBuildDomTreeResult(res, true, false, nil, "", "")
	if out == nil {
		t.Fatal("nil result")
	}
	// Expect two lines, sorted by highlight index: 1 (link) then 2 (button).
	lines := strings.Split(out.Snapshot, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out.Snapshot)
	}
	if !strings.HasPrefix(lines[0], "link [ref=1]") {
		t.Fatalf("first line: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "button [ref=2]") {
		t.Fatalf("second line: %q", lines[1])
	}
	if out.Refs["1"] == nil || out.Refs["2"] == nil {
		t.Fatalf("refs missing: %+v", out.Refs)
	}
	if out.Refs["2"].Role != "button" || out.Refs["2"].Name != "Save" {
		t.Fatalf("ref 2: %+v", out.Refs["2"])
	}
	// Elements array mirrors Refs in snapshot order (ref 1 then ref 2).
	if len(out.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d: %+v", len(out.Elements), out.Elements)
	}
	if out.Elements[0].Ref != "1" || out.Elements[0].Role != "link" || out.Elements[0].Name != "Home" {
		t.Fatalf("element[0]: %+v", out.Elements[0])
	}
	if out.Elements[1].Ref != "2" || out.Elements[1].Role != "button" || out.Elements[1].Name != "Save" || out.Elements[1].TagName != "button" {
		t.Fatalf("element[1]: %+v", out.Elements[1])
	}

	// With a selector that matches neither, we get empty snapshot.
	out = ConvertBuildDomTreeResult(res, true, false, nil, "zzz", "")
	if out.Snapshot != "" {
		t.Fatalf("selector no match: got %q", out.Snapshot)
	}
}

func TestConvertBuildDomTreeResult_FullTree(t *testing.T) {
	hi := func(i int) *int { return &i }
	nodeMap := map[string]json.RawMessage{
		"root": mustRaw(t, rawDomElementNode{TagName: "section", XPath: "/root", Children: []string{"btn", "txt"}}),
		"btn":  mustRaw(t, rawDomElementNode{TagName: "button", XPath: "/root/btn", HighlightIndex: hi(1), Attributes: map[string]string{"aria-label": "Go"}}),
		"txt":  mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "hello"}),
	}
	res := &buildDomTreeResult{RootID: "root", Map: nodeMap}

	out := ConvertBuildDomTreeResult(res, false, false, nil, "", "")
	if !strings.Contains(out.Snapshot, "- region") { // <section> maps to role "region"
		t.Fatalf("missing root line: %q", out.Snapshot)
	}
	if !strings.Contains(out.Snapshot, "[ref=1]") {
		t.Fatalf("missing button ref line: %q", out.Snapshot)
	}
	if !strings.Contains(out.Snapshot, "text \"hello\"") {
		t.Fatalf("missing text line: %q", out.Snapshot)
	}
	if out.Refs["1"] == nil || out.Refs["1"].Name != "Go" {
		t.Fatalf("button ref: %+v", out.Refs["1"])
	}
	if len(out.Elements) != 1 || out.Elements[0].Ref != "1" || out.Elements[0].Name != "Go" {
		t.Fatalf("full-tree elements: %+v", out.Elements)
	}

	// Compact mode: no tag suffix "<...>".
	out = ConvertBuildDomTreeResult(res, false, true, nil, "", "")
	if strings.Contains(out.Snapshot, "<section>") || strings.Contains(out.Snapshot, "<button>") {
		t.Fatalf("compact mode should not include tag suffix: %q", out.Snapshot)
	}

	// MaxDepth 0: root only, no children.
	max := 0
	out = ConvertBuildDomTreeResult(res, false, false, &max, "", "")
	if strings.Contains(out.Snapshot, "text \"hello\"") {
		t.Fatalf("maxDepth 0 should skip text child: %q", out.Snapshot)
	}

	// Selector that doesn't match root: recurses to children.
	out = ConvertBuildDomTreeResult(res, false, false, nil, "button", "")
	if !strings.Contains(out.Snapshot, "[ref=1]") {
		t.Fatalf("selector recurse: expected button line: %q", out.Snapshot)
	}
	if strings.Contains(out.Snapshot, "region") {
		t.Fatalf("selector recurse: root should be filtered out: %q", out.Snapshot)
	}

	// Empty text is skipped.
	emptyMap := map[string]json.RawMessage{
		"root": mustRaw(t, rawDomElementNode{TagName: "div", Children: []string{"t"}}),
		"t":    mustRaw(t, rawDomTextNode{Type: "TEXT_NODE", Text: "   "}),
	}
	out = ConvertBuildDomTreeResult(&buildDomTreeResult{RootID: "root", Map: emptyMap}, false, false, nil, "", "")
	if strings.Contains(out.Snapshot, "text") {
		t.Fatalf("empty text should be skipped: %q", out.Snapshot)
	}
}
