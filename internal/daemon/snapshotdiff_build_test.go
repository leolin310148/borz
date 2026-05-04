package daemon

import (
	"encoding/json"
	"testing"
)

// helpers -----------------------------------------------------------------

func ptrInt(i int) *int { return &i }

func makeTree(t *testing.T, rootID string, m map[string]interface{}) *buildDomTreeResult {
	t.Helper()
	out := map[string]json.RawMessage{}
	for k, v := range m {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %q: %v", k, err)
		}
		out[k] = b
	}
	return &buildDomTreeResult{RootID: rootID, Map: out}
}

func findNode(s *DiffSnapshot, role, name string) *DiffNode {
	for _, n := range s.Nodes {
		if n.Role == role && n.Name == name {
			return n
		}
	}
	return nil
}

// BuildDiffSnapshot --------------------------------------------------------

func TestBuildDiffSnapshot_NilOrEmpty(t *testing.T) {
	if s := BuildDiffSnapshot(nil, "u"); s == nil || s.URL != "u" || len(s.Nodes) != 0 {
		t.Fatalf("nil result: got %+v", s)
	}
	if s := BuildDiffSnapshot(&buildDomTreeResult{Map: map[string]json.RawMessage{}}, "u"); len(s.Nodes) != 0 {
		t.Fatalf("empty map: got %+v", s)
	}
	if s := BuildDiffSnapshot(&buildDomTreeResult{RootID: "x"}, "u"); len(s.Nodes) != 0 {
		t.Fatalf("nil map: got %+v", s)
	}
}

func TestBuildDiffSnapshot_PopulatesAncestors(t *testing.T) {
	// section[Login] > form > button[Submit]
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "section", XPath: "/s", Children: []string{"form"},
			Attributes: map[string]string{"aria-label": "Login"}},
		"form": rawDomElementNode{TagName: "form", XPath: "/s/form", Children: []string{"btn"}},
		"btn": rawDomElementNode{TagName: "button", XPath: "/s/form/button",
			HighlightIndex: ptrInt(1),
			Attributes:     map[string]string{"aria-label": "Submit"}},
	})
	s := BuildDiffSnapshot(tree, "https://x")
	btn := findNode(s, "button", "Submit")
	if btn == nil {
		t.Fatalf("button not found in nodes: %+v", s.Nodes)
	}
	if len(btn.Ancestors) != 2 {
		t.Fatalf("expected 2 ancestors, got %d: %+v", len(btn.Ancestors), btn.Ancestors)
	}
	if btn.Ancestors[0].Role != "region" || btn.Ancestors[0].Name != "Login" {
		t.Fatalf("ancestor[0] should be region[Login], got %+v", btn.Ancestors[0])
	}
	if btn.Ancestors[1].Role != "form" {
		t.Fatalf("ancestor[1] should be form, got %+v", btn.Ancestors[1])
	}
	if btn.Ref != "1" {
		t.Fatalf("button ref should be '1', got %q", btn.Ref)
	}
	if btn.TagName != "button" {
		t.Fatalf("tag should be lowercased button, got %q", btn.TagName)
	}
	if btn.XPath != "/s/form/button" {
		t.Fatalf("xpath: got %q", btn.XPath)
	}
}

func TestBuildDiffSnapshot_KeepsOnlyTrackedAttrs(t *testing.T) {
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "button", XPath: "/b",
			HighlightIndex: ptrInt(1),
			Attributes: map[string]string{
				"id":            "btn-1",
				"class":         "primary lg", // untracked
				"data-foo":      "bar",        // untracked
				"aria-disabled": "true",       // tracked
				"aria-pressed":  "false",      // tracked
				"style":         "color:red;", // untracked
			}},
	})
	s := BuildDiffSnapshot(tree, "u")
	if len(s.Nodes) != 1 {
		t.Fatalf("expected 1 node: %+v", s.Nodes)
	}
	n := s.Nodes[0]
	if n.ID != "btn-1" {
		t.Fatalf("ID should still be populated from raw 'id' attr, got %q", n.ID)
	}
	if _, ok := n.Attrs["class"]; ok {
		t.Fatalf("class must NOT be retained (untracked): %+v", n.Attrs)
	}
	if _, ok := n.Attrs["data-foo"]; ok {
		t.Fatalf("data-* must NOT be retained: %+v", n.Attrs)
	}
	if v := n.Attrs["aria-disabled"]; v != "true" {
		t.Fatalf("aria-disabled should be retained as 'true': %+v", n.Attrs)
	}
	if v := n.Attrs["aria-pressed"]; v != "false" {
		t.Fatalf("aria-pressed should be retained: %+v", n.Attrs)
	}
}

func TestBuildDiffSnapshot_SkipsTextNodes(t *testing.T) {
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "div", XPath: "/d", Children: []string{"t", "btn"}},
		"t":    rawDomTextNode{Type: "TEXT_NODE", Text: "hello"},
		"btn":  rawDomElementNode{TagName: "button", XPath: "/d/b", HighlightIndex: ptrInt(1)},
	})
	s := BuildDiffSnapshot(tree, "u")
	if len(s.Nodes) != 2 {
		t.Fatalf("expected 2 element nodes (root + button), got %d: %+v", len(s.Nodes), s.Nodes)
	}
	for _, n := range s.Nodes {
		if n.TagName == "" {
			t.Fatalf("text node leaked into nodes: %+v", n)
		}
	}
}

func TestBuildDiffSnapshot_SiblingSubtreesDoNotShareAncestors(t *testing.T) {
	// Reproduces a slice-aliasing bug: if the walker append-mutates a shared
	// ancestor slice, the second sibling's ancestors get a stale tail from the
	// first.
	tree := makeTree(t, "root", map[string]interface{}{
		"root": rawDomElementNode{TagName: "main", XPath: "/m", Children: []string{"a", "b"}},
		"a": rawDomElementNode{TagName: "section", XPath: "/m/a", Children: []string{"a-btn"},
			Attributes: map[string]string{"aria-label": "A"}},
		"b": rawDomElementNode{TagName: "section", XPath: "/m/b", Children: []string{"b-btn"},
			Attributes: map[string]string{"aria-label": "B"}},
		"a-btn": rawDomElementNode{TagName: "button", XPath: "/m/a/btn",
			HighlightIndex: ptrInt(1), Attributes: map[string]string{"aria-label": "ButtonA"}},
		"b-btn": rawDomElementNode{TagName: "button", XPath: "/m/b/btn",
			HighlightIndex: ptrInt(2), Attributes: map[string]string{"aria-label": "ButtonB"}},
	})
	s := BuildDiffSnapshot(tree, "u")
	a := findNode(s, "button", "ButtonA")
	b := findNode(s, "button", "ButtonB")
	if a == nil || b == nil {
		t.Fatalf("missing buttons: %+v", s.Nodes)
	}
	if len(a.Ancestors) != 2 || len(b.Ancestors) != 2 {
		t.Fatalf("each button should have 2 ancestors, got A=%d B=%d", len(a.Ancestors), len(b.Ancestors))
	}
	if a.Ancestors[1].Name != "A" {
		t.Fatalf("ButtonA's parent ancestor should be section[A]: %+v", a.Ancestors)
	}
	if b.Ancestors[1].Name != "B" {
		t.Fatalf("ButtonB's parent ancestor should be section[B] (not A): %+v", b.Ancestors)
	}
}

// End-to-end: BuildDiffSnapshot output is consumable by DiffSnapshots ------

func TestBuildAndDiff_EndToEnd_AddRemoveChange(t *testing.T) {
	mk := func(disabled string) *buildDomTreeResult {
		return makeTree(t, "root", map[string]interface{}{
			"root": rawDomElementNode{TagName: "main", XPath: "/m",
				Attributes: map[string]string{"aria-label": "Page"},
				Children:   []string{"btn", "old-or-new"}},
			"btn": rawDomElementNode{TagName: "button", XPath: "/m/btn",
				HighlightIndex: ptrInt(1),
				Attributes: map[string]string{
					"id":            "submit",
					"aria-label":    "Submit",
					"aria-disabled": disabled,
				}},
			"old-or-new": rawDomElementNode{}, // overridden below
		})
	}
	prevTree := mk("true")
	// Force "old" listitem in prev.
	oldNode, _ := json.Marshal(rawDomElementNode{TagName: "li", XPath: "/m/li",
		HighlightIndex: ptrInt(2), Attributes: map[string]string{"aria-label": "Old"}})
	prevTree.Map["old-or-new"] = oldNode
	prev := BuildDiffSnapshot(prevTree, "https://x")

	currTree := mk("false")
	// Force a new alert in curr instead of the listitem.
	newNode, _ := json.Marshal(rawDomElementNode{TagName: "div", XPath: "/m/alert",
		HighlightIndex: ptrInt(2),
		Attributes:     map[string]string{"role": "alert", "aria-label": "Saved"}})
	currTree.Map["old-or-new"] = newNode
	curr := BuildDiffSnapshot(currTree, "https://x")

	d := DiffSnapshots(prev, curr)
	if d.BaselineReset {
		t.Fatal("same URL must not trigger baseline reset")
	}
	if d.Stats.Added != 1 || d.Stats.Removed != 1 || d.Stats.Changed != 1 {
		t.Fatalf("expected 1/1/1 added/removed/changed, got %+v", d.Stats)
	}
	// The button matched by id and reflects the aria-disabled flip.
	var found bool
	for _, c := range d.Changed {
		if c.Role == "button" && c.AttrChanges["aria-disabled"].Old == "true" && c.AttrChanges["aria-disabled"].New == "false" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected aria-disabled true→false on button: %+v", d.Changed)
	}
}
