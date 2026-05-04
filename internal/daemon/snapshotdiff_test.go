package daemon

import (
	"strings"
	"testing"
)

// ---------- builder helpers ----------

func anc(role, name string) AncestorStep { return AncestorStep{Role: role, Name: name} }

func mkNode(role, name string, opts ...func(*DiffNode)) *DiffNode {
	n := &DiffNode{Role: role, Name: name, TagName: role}
	for _, o := range opts {
		o(n)
	}
	return n
}

func withRef(r string) func(*DiffNode)             { return func(n *DiffNode) { n.Ref = r } }
func withID(s string) func(*DiffNode)              { return func(n *DiffNode) { n.ID = s } }
func withXPath(s string) func(*DiffNode)           { return func(n *DiffNode) { n.XPath = s } }
func withTag(s string) func(*DiffNode)             { return func(n *DiffNode) { n.TagName = s } }
func withAncestors(a ...AncestorStep) func(*DiffNode) {
	return func(n *DiffNode) { n.Ancestors = a }
}
func withAttrs(kv ...string) func(*DiffNode) {
	return func(n *DiffNode) {
		if n.Attrs == nil {
			n.Attrs = map[string]string{}
		}
		for i := 0; i+1 < len(kv); i += 2 {
			n.Attrs[kv[i]] = kv[i+1]
		}
	}
}

func snap(url string, nodes ...*DiffNode) *DiffSnapshot {
	return &DiffSnapshot{URL: url, Nodes: nodes}
}

func keyMatch(t *testing.T, entries []*DiffEntry, key string) *DiffEntry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	return nil
}

func changeFor(t *testing.T, changes []*DiffChange, key string) *DiffChange {
	t.Helper()
	for _, c := range changes {
		if c.Key == key {
			return c
		}
	}
	return nil
}

// ============================================================
// IsStableID — auto-generated id detection
// ============================================================

func TestIsStableID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
		why  string
	}{
		{"", false, "empty"},
		{"login-submit", true, "kebab-case human id"},
		{"loginSubmit", true, "camelCase"},
		{"form-1", true, "trailing number ok"},
		{"myButton42", true, "alnum mix not pure hex"},
		{"abc", true, "short hex-ish but under 8 chars"},

		{"react-aria-12345", false, "react-aria autogen"},
		{"react-12345", false, "react autogen"},
		{"radix-:r3:", false, "radix"},
		{":r0:", false, "radix bare"},
		{":r10:", false, "radix multi-digit"},
		{":rAa:", false, "radix mixed case"},
		{"mui-9f8e7d6c", false, "mui autogen"},
		{"abcdef1234", false, "10-char lowercase hex hash"},
		{"ABCDEF1234", false, "10-char uppercase hex hash"},
		{"deadbeef", false, "8-char hex hash"},
	}
	for _, c := range cases {
		t.Run(c.id+"/"+c.why, func(t *testing.T) {
			if got := IsStableID(c.id); got != c.want {
				t.Fatalf("IsStableID(%q) = %v, want %v (%s)", c.id, got, c.want, c.why)
			}
		})
	}
}

// ============================================================
// SemanticKey
// ============================================================

func TestSemanticKey_Basic(t *testing.T) {
	n := mkNode("button", "Submit", withAncestors(anc("form", "Login")))
	got := n.SemanticKey()
	// Expected shape: "form[Login] > button[Submit]". Allow either > or another
	// separator — we just require both segments are present in order with no
	// segment between them.
	if !strings.Contains(got, "form[Login]") || !strings.Contains(got, "button[Submit]") {
		t.Fatalf("missing segments: %q", got)
	}
	if strings.Index(got, "form[Login]") > strings.Index(got, "button[Submit]") {
		t.Fatalf("ancestor must precede self: %q", got)
	}
}

func TestSemanticKey_DistinguishesDuplicateNamesByAncestor(t *testing.T) {
	loginSubmit := mkNode("button", "Submit", withAncestors(anc("form", "Login")))
	signupSubmit := mkNode("button", "Submit", withAncestors(anc("form", "Signup")))
	if loginSubmit.SemanticKey() == signupSubmit.SemanticKey() {
		t.Fatalf("two Submit buttons under different forms must have different semantic keys")
	}
}

func TestSemanticKey_NoAncestors(t *testing.T) {
	n := mkNode("button", "Go")
	got := n.SemanticKey()
	if !strings.Contains(got, "button[Go]") {
		t.Fatalf("self segment missing: %q", got)
	}
}

func TestSemanticKey_EmptyRoleAndName(t *testing.T) {
	// A wrapper div with neither role nor name still needs *some* key, otherwise
	// every empty wrapper collides. Implementation may use tag or "" — but two
	// wrappers under same parent should NOT silently collapse to identical key
	// once positional disambiguation is layered on by DiffSnapshots.
	a := mkNode("", "", withTag("div"), withAncestors(anc("main", "")))
	b := mkNode("", "", withTag("div"), withAncestors(anc("main", "")))
	// Same key here is acceptable — DiffSnapshots will disambiguate by sibling
	// position. We just require SemanticKey doesn't panic and returns a string.
	_ = a.SemanticKey()
	_ = b.SemanticKey()
}

// ============================================================
// Baseline reset
// ============================================================

func TestDiff_NilPrev_BaselineReset(t *testing.T) {
	curr := snap("https://x", mkNode("button", "Go", withRef("1")))
	d := DiffSnapshots(nil, curr)
	if !d.BaselineReset {
		t.Fatal("expected BaselineReset=true when prev is nil")
	}
	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added on baseline reset, got %d", len(d.Added))
	}
	if len(d.Removed) != 0 || len(d.Changed) != 0 {
		t.Fatalf("removed/changed must be empty on baseline reset")
	}
	if d.Stats.Added != 1 {
		t.Fatalf("stats.added = %d", d.Stats.Added)
	}
}

func TestDiff_URLChange_BaselineReset(t *testing.T) {
	prev := snap("https://a", mkNode("button", "Go", withRef("1")))
	curr := snap("https://b", mkNode("button", "Go", withRef("1")))
	d := DiffSnapshots(prev, curr)
	if !d.BaselineReset {
		t.Fatal("URL change must trigger baseline reset")
	}
	if len(d.Removed) != 0 {
		t.Fatalf("baseline reset must not produce Removed entries: %+v", d.Removed)
	}
	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(d.Added))
	}
}

func TestDiff_EmptyToEmpty_NoChanges(t *testing.T) {
	prev := snap("https://x")
	curr := snap("https://x")
	d := DiffSnapshots(prev, curr)
	if d.BaselineReset {
		t.Fatal("same URL must not trigger baseline reset")
	}
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("expected zero diffs: %+v", d)
	}
	if d.Diff != "" {
		t.Fatalf("expected empty diff string: %q", d.Diff)
	}
}

// ============================================================
// Identity tier 1: stable id wins even when refs and order shuffle
// ============================================================

func TestDiff_StableID_MatchesAcrossRefShuffle(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Submit", withRef("1"), withID("login-submit")),
		mkNode("button", "Cancel", withRef("2"), withID("login-cancel")),
	)
	// Refs reassigned (now Cancel=1, Submit=2) and order swapped — both should
	// still match by id.
	curr := snap("u",
		mkNode("button", "Cancel", withRef("1"), withID("login-cancel")),
		mkNode("button", "Submit", withRef("2"), withID("login-submit")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("ref+order shuffle with stable ids should be no-op: %+v", d)
	}
	if d.Stats.Unchanged != 2 {
		t.Fatalf("stats.unchanged = %d, want 2", d.Stats.Unchanged)
	}
}

func TestDiff_AutoGeneratedID_IgnoredFallsBackToSemantic(t *testing.T) {
	// Same logical button but framework auto-id changed. Semantic path matches.
	prev := snap("u",
		mkNode("button", "Save", withRef("1"), withID("react-aria-1111"),
			withAncestors(anc("form", "Settings"))),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1"), withID("react-aria-2222"),
			withAncestors(anc("form", "Settings"))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatalf("autogen-id churn must NOT cause add/remove: %+v", d)
	}
}

func TestDiff_StableIDInPrev_LostInCurr_FallsBackToSemantic(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Save", withRef("1"), withID("save-btn"),
			withAncestors(anc("form", "Settings"))),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1"),
			withAncestors(anc("form", "Settings"))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatalf("losing id but matching semantic must not produce add/remove: %+v", d)
	}
}

// ============================================================
// Identity tier 2: semantic path
// ============================================================

func TestDiff_DuplicateNames_DisambiguatedByAncestor(t *testing.T) {
	// Two "Submit" buttons in different forms. After re-snapshot (refs change),
	// each must match its own form's submit, not cross-match.
	prev := snap("u",
		mkNode("button", "Submit", withRef("1"), withAncestors(anc("form", "Login"))),
		mkNode("button", "Submit", withRef("2"), withAncestors(anc("form", "Signup"))),
	)
	curr := snap("u",
		mkNode("button", "Submit", withRef("9"), withAncestors(anc("form", "Login"))),
		mkNode("button", "Submit", withRef("8"), withAncestors(anc("form", "Signup"))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("duplicate names under different ancestors should match cleanly: %+v", d)
	}
}

func TestDiff_ReorderUnderSameParent_NoChange(t *testing.T) {
	prev := snap("u",
		mkNode("listitem", "Apple", withRef("1"), withAncestors(anc("list", "Fruits"))),
		mkNode("listitem", "Banana", withRef("2"), withAncestors(anc("list", "Fruits"))),
		mkNode("listitem", "Cherry", withRef("3"), withAncestors(anc("list", "Fruits"))),
	)
	curr := snap("u",
		mkNode("listitem", "Cherry", withRef("1"), withAncestors(anc("list", "Fruits"))),
		mkNode("listitem", "Apple", withRef("2"), withAncestors(anc("list", "Fruits"))),
		mkNode("listitem", "Banana", withRef("3"), withAncestors(anc("list", "Fruits"))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed) != 0 {
		t.Fatalf("reorder must not trigger add/remove: %+v", d)
	}
	if d.Stats.Unchanged != 3 {
		t.Fatalf("stats.unchanged = %d, want 3", d.Stats.Unchanged)
	}
}

// ============================================================
// Identity tier 3: xpath fallback when role/name absent
// ============================================================

func TestDiff_XPathFallback_ForRolelessNodes(t *testing.T) {
	// Two structural divs with no role/name. xpath is the only thing we have.
	prev := snap("u",
		mkNode("", "", withTag("div"), withRef("1"), withXPath("/html/body/div[1]")),
		mkNode("", "", withTag("div"), withRef("2"), withXPath("/html/body/div[2]")),
	)
	curr := snap("u",
		mkNode("", "", withTag("div"), withRef("1"), withXPath("/html/body/div[1]")),
		mkNode("", "", withTag("div"), withRef("2"), withXPath("/html/body/div[2]")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed) != 0 {
		t.Fatalf("xpath-stable structural divs must match: %+v", d)
	}
}

// ============================================================
// Wrappers — adding/removing structural wrappers should NOT churn
// ============================================================

func TestDiff_WrapperInserted_DoesNotChurn(t *testing.T) {
	// Adding a wrapper div around a button: the button's semantic ancestor
	// chain is the same once empty-role wrappers are skipped.
	prev := snap("u",
		mkNode("button", "Save", withRef("1"), withAncestors(anc("main", ""))),
	)
	curr := snap("u",
		// Wrapper has empty role/name and should be skipped in semantic-key
		// computation by the impl. The button's effective ancestor chain
		// remains [main].
		mkNode("button", "Save", withRef("1"),
			withAncestors(anc("main", ""), anc("", ""))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Fatalf("wrapper insertion should not churn the wrapped button: %+v", d)
	}
}

// ============================================================
// Positional disambiguation — N identical "Add to cart" buttons
// ============================================================

func TestDiff_IdenticalSiblings_MatchByPosition(t *testing.T) {
	mk := func(ref string) *DiffNode {
		return mkNode("button", "Add to cart", withRef(ref),
			withAncestors(anc("main", "Products")))
	}
	prev := snap("u", mk("1"), mk("2"), mk("3"))
	curr := snap("u", mk("10"), mk("11"), mk("12"))
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("identical siblings must match 1:1 by position: %+v", d)
	}
	if d.Stats.Unchanged != 3 {
		t.Fatalf("stats.unchanged = %d, want 3", d.Stats.Unchanged)
	}
}

func TestDiff_IdenticalSiblings_OneRemoved(t *testing.T) {
	mk := func(ref string) *DiffNode {
		return mkNode("button", "Add to cart", withRef(ref),
			withAncestors(anc("main", "Products")))
	}
	prev := snap("u", mk("1"), mk("2"), mk("3"))
	curr := snap("u", mk("1"), mk("2"))
	d := DiffSnapshots(prev, curr)
	if len(d.Removed) != 1 {
		t.Fatalf("expected exactly 1 removed when going from 3→2 identical siblings, got %d: %+v", len(d.Removed), d.Removed)
	}
	if len(d.Added) != 0 {
		t.Fatalf("expected 0 added: %+v", d.Added)
	}
}

func TestDiff_IdenticalSiblings_OneAdded(t *testing.T) {
	mk := func(ref string) *DiffNode {
		return mkNode("button", "Add to cart", withRef(ref),
			withAncestors(anc("main", "Products")))
	}
	prev := snap("u", mk("1"), mk("2"))
	curr := snap("u", mk("1"), mk("2"), mk("3"))
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 1 || len(d.Removed) != 0 {
		t.Fatalf("expected 1 added, 0 removed: %+v", d)
	}
}

// ============================================================
// Pure additions and pure removals
// ============================================================

func TestDiff_NewElement_Added(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Save", withRef("1")),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1")),
		mkNode("alert", "Saved", withRef("2")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added: %+v", d.Added)
	}
	if d.Added[0].Role != "alert" || d.Added[0].Name != "Saved" {
		t.Fatalf("wrong added entry: %+v", d.Added[0])
	}
	// Added entries must carry the *current* ref so the agent can act on it.
	if d.Added[0].Ref != "2" {
		t.Fatalf("added must carry current ref, got %q", d.Added[0].Ref)
	}
}

func TestDiff_RemovedElement_KeepsPreviousRef(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Save", withRef("1")),
		mkNode("listitem", "Item 3", withRef("18")),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Removed) != 1 {
		t.Fatalf("expected 1 removed: %+v", d.Removed)
	}
	// Removed entries echo the *previous* ref as a debugging hint.
	if d.Removed[0].Ref != "18" {
		t.Fatalf("removed must carry previous ref 18, got %q", d.Removed[0].Ref)
	}
	if d.Removed[0].Role != "listitem" || d.Removed[0].Name != "Item 3" {
		t.Fatalf("wrong removed entry: %+v", d.Removed[0])
	}
}

// ============================================================
// Attribute changes (Changed)
// ============================================================

func TestDiff_AriaDisabledFlip_Changed(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Submit", withRef("1"),
			withAttrs("aria-disabled", "true"),
			withAncestors(anc("form", "Login"))),
	)
	curr := snap("u",
		mkNode("button", "Submit", withRef("1"),
			withAttrs("aria-disabled", "false"),
			withAncestors(anc("form", "Login"))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 1 {
		t.Fatalf("expected 1 changed: %+v", d)
	}
	c := d.Changed[0]
	if c.Ref != "1" {
		t.Fatalf("Changed.Ref must be current ref, got %q", c.Ref)
	}
	delta, ok := c.AttrChanges["aria-disabled"]
	if !ok {
		t.Fatalf("aria-disabled delta missing: %+v", c.AttrChanges)
	}
	if delta.Old != "true" || delta.New != "false" {
		t.Fatalf("delta wrong: %+v", delta)
	}
}

func TestDiff_MultipleAttrChanges_OneChangedEntry(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Toggle", withRef("1"),
			withAttrs("aria-pressed", "false", "aria-disabled", "false")),
	)
	curr := snap("u",
		mkNode("button", "Toggle", withRef("1"),
			withAttrs("aria-pressed", "true", "aria-disabled", "true")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 1 {
		t.Fatalf("multiple attr changes on one node should be one Changed entry: %+v", d)
	}
	c := d.Changed[0]
	if len(c.AttrChanges) != 2 {
		t.Fatalf("expected 2 attr deltas, got %d: %+v", len(c.AttrChanges), c.AttrChanges)
	}
}

func TestDiff_UntrackedAttrChange_Ignored(t *testing.T) {
	// class is not in trackedAttrs — change should be silent.
	prev := snap("u",
		mkNode("button", "Save", withRef("1"),
			withAttrs("class", "btn primary")),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1"),
			withAttrs("class", "btn secondary")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 0 {
		t.Fatalf("untracked-attr change must not produce a Changed entry: %+v", d.Changed)
	}
}

func TestDiff_TrackedAttrAddedOrRemoved(t *testing.T) {
	// aria-pressed appears in curr but was absent in prev → Changed with Old=""
	prev := snap("u",
		mkNode("button", "Toggle", withRef("1")),
	)
	curr := snap("u",
		mkNode("button", "Toggle", withRef("1"),
			withAttrs("aria-pressed", "true")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 1 {
		t.Fatalf("attr appearance must produce Changed: %+v", d)
	}
	delta := d.Changed[0].AttrChanges["aria-pressed"]
	if delta.Old != "" || delta.New != "true" {
		t.Fatalf("expected old='' new='true', got %+v", delta)
	}

	// Reverse: aria-pressed disappears.
	d2 := DiffSnapshots(curr, prev)
	if len(d2.Changed) != 1 {
		t.Fatalf("attr disappearance must produce Changed: %+v", d2)
	}
	delta2 := d2.Changed[0].AttrChanges["aria-pressed"]
	if delta2.Old != "true" || delta2.New != "" {
		t.Fatalf("expected old='true' new='', got %+v", delta2)
	}
}

// ============================================================
// Name changes
// ============================================================

func TestDiff_NameChange_ProducesNameDelta(t *testing.T) {
	// Same logical node (matched by id), but accessible name changed.
	prev := snap("u",
		mkNode("button", "Save", withRef("1"), withID("submit-btn")),
	)
	curr := snap("u",
		mkNode("button", "Saving…", withRef("1"), withID("submit-btn")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 1 {
		t.Fatalf("name change should produce Changed: %+v", d)
	}
	if d.Changed[0].NameChanged == nil {
		t.Fatalf("NameChanged must be set: %+v", d.Changed[0])
	}
	if d.Changed[0].NameChanged.Old != "Save" || d.Changed[0].NameChanged.New != "Saving…" {
		t.Fatalf("NameDelta wrong: %+v", d.Changed[0].NameChanged)
	}
}

// ============================================================
// Stats consistency
// ============================================================

func TestDiff_StatsMatchSlices(t *testing.T) {
	prev := snap("u",
		mkNode("button", "A", withRef("1"), withID("a")),
		mkNode("button", "B", withRef("2"), withID("b")),
		mkNode("button", "C", withRef("3"), withID("c")),
	)
	curr := snap("u",
		mkNode("button", "A", withRef("1"), withID("a")), // unchanged
		mkNode("button", "B", withRef("2"), withID("b"),  // changed (attr)
			withAttrs("aria-disabled", "true")),
		// "C" removed
		mkNode("alert", "Z", withRef("3")), // added
	)
	d := DiffSnapshots(prev, curr)
	if d.Stats.Added != len(d.Added) {
		t.Fatalf("stats.added=%d slice=%d", d.Stats.Added, len(d.Added))
	}
	if d.Stats.Removed != len(d.Removed) {
		t.Fatalf("stats.removed=%d slice=%d", d.Stats.Removed, len(d.Removed))
	}
	if d.Stats.Changed != len(d.Changed) {
		t.Fatalf("stats.changed=%d slice=%d", d.Stats.Changed, len(d.Changed))
	}
	if d.Stats.Added != 1 || d.Stats.Removed != 1 || d.Stats.Changed != 1 {
		t.Fatalf("expected 1/1/1, got %+v", d.Stats)
	}
	if d.Stats.Unchanged != 1 {
		t.Fatalf("expected 1 unchanged (A), got %d", d.Stats.Unchanged)
	}
}

// ============================================================
// Diff string formatting
// ============================================================

func TestDiff_StringFormat_HasPlusMinusTilde(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Save", withRef("1"), withID("save"),
			withAttrs("aria-disabled", "true")),
		mkNode("listitem", "Old", withRef("2"), withID("old")),
	)
	curr := snap("u",
		mkNode("button", "Save", withRef("1"), withID("save"),
			withAttrs("aria-disabled", "false")),
		mkNode("alert", "Saved", withRef("2"), withID("saved-alert")),
	)
	d := DiffSnapshots(prev, curr)
	if !strings.Contains(d.Diff, "+ alert") {
		t.Fatalf("missing '+ alert' line: %q", d.Diff)
	}
	if !strings.Contains(d.Diff, "- listitem") {
		t.Fatalf("missing '- listitem' line: %q", d.Diff)
	}
	if !strings.Contains(d.Diff, "~ button") {
		t.Fatalf("missing '~ button' line: %q", d.Diff)
	}
	// Removed line should expose previous ref for human debugging.
	if !strings.Contains(d.Diff, "was [ref=2]") {
		t.Fatalf("removed line should mention previous ref: %q", d.Diff)
	}
	// Added line carries CURRENT ref, actionable.
	if !strings.Contains(d.Diff, "[ref=2]") {
		t.Fatalf("added line should carry current ref: %q", d.Diff)
	}
	// Attribute delta visible.
	if !strings.Contains(d.Diff, "aria-disabled") {
		t.Fatalf("changed line should include attr name: %q", d.Diff)
	}
	if !strings.Contains(d.Diff, "true") || !strings.Contains(d.Diff, "false") {
		t.Fatalf("changed line should include both old and new attr values: %q", d.Diff)
	}
}

func TestDiff_StringFormat_EmptyWhenNoChanges(t *testing.T) {
	prev := snap("u", mkNode("button", "Go", withRef("1"), withID("go")))
	curr := snap("u", mkNode("button", "Go", withRef("1"), withID("go")))
	d := DiffSnapshots(prev, curr)
	if d.Diff != "" {
		t.Fatalf("expected empty diff string when nothing changed, got %q", d.Diff)
	}
}

// ============================================================
// Edge cases
// ============================================================

func TestDiff_OnlyRefDiffers_NoChange(t *testing.T) {
	// Same logical node, only walk-order ref differs. This is the whole point
	// of the diff feature.
	prev := snap("u",
		mkNode("button", "Go", withRef("1"), withID("go")),
	)
	curr := snap("u",
		mkNode("button", "Go", withRef("99"), withID("go")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("ref-only change must not appear in diff: %+v", d)
	}
}

func TestDiff_NodeWithoutRef_Diffable(t *testing.T) {
	// Some nodes (full-tree non-interactive) come through without highlight
	// indices (Ref==""). They still need to participate in the diff.
	prev := snap("u",
		mkNode("region", "Sidebar", withAncestors(anc("main", ""))),
	)
	curr := snap("u",
		mkNode("region", "Sidebar", withAncestors(anc("main", ""))),
		mkNode("region", "Help", withAncestors(anc("main", ""))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added) != 1 {
		t.Fatalf("expected 1 added refless region: %+v", d.Added)
	}
}

func TestDiff_ChangedEntry_HasCurrentAndPreviousRefs(t *testing.T) {
	prev := snap("u",
		mkNode("button", "Submit", withRef("5"), withID("submit"),
			withAttrs("aria-disabled", "true")),
	)
	curr := snap("u",
		mkNode("button", "Submit", withRef("12"), withID("submit"),
			withAttrs("aria-disabled", "false")),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Changed) != 1 {
		t.Fatalf("expected 1 change: %+v", d)
	}
	c := d.Changed[0]
	if c.Ref != "12" {
		t.Fatalf("Changed.Ref should be current (12), got %q", c.Ref)
	}
	if c.PrevRef != "5" {
		t.Fatalf("Changed.PrevRef should be previous (5), got %q", c.PrevRef)
	}
}

func TestDiff_AddedRemovedChanged_KeyFieldPopulated(t *testing.T) {
	// Every entry must have a non-empty Key so callers can reliably correlate
	// across categories (e.g. produce a flat keyed map).
	prev := snap("u",
		mkNode("button", "Removed", withRef("1"), withID("rm-id")),
		mkNode("button", "Toggle", withRef("2"), withID("toggle-id"),
			withAttrs("aria-pressed", "false")),
	)
	curr := snap("u",
		mkNode("button", "Toggle", withRef("2"), withID("toggle-id"),
			withAttrs("aria-pressed", "true")),
		mkNode("alert", "New", withRef("3")),
	)
	d := DiffSnapshots(prev, curr)
	for _, e := range d.Added {
		if e.Key == "" {
			t.Fatalf("Added entry missing Key: %+v", e)
		}
	}
	for _, e := range d.Removed {
		if e.Key == "" {
			t.Fatalf("Removed entry missing Key: %+v", e)
		}
	}
	for _, c := range d.Changed {
		if c.Key == "" {
			t.Fatalf("Changed entry missing Key: %+v", c)
		}
	}
	// And Key must be unique-enough that we can find a specific entry by it.
	if changeFor(t, d.Changed, d.Changed[0].Key) == nil {
		t.Fatalf("could not find Changed by its own Key")
	}
	if keyMatch(t, d.Removed, d.Removed[0].Key) == nil {
		t.Fatalf("could not find Removed by its own Key")
	}
}

func TestDiff_GreedyTier_IDBeatsSemantic(t *testing.T) {
	// Two nodes with identical role+name+ancestor (same semantic key) but
	// distinct stable ids. The id tier must pair them correctly even though
	// semantic-key matching alone would also produce a valid pairing — we want
	// the id-pairing.
	prev := snap("u",
		mkNode("button", "Submit", withRef("1"), withID("login-submit"),
			withAncestors(anc("section", ""))),
		mkNode("button", "Submit", withRef("2"), withID("signup-submit"),
			withAncestors(anc("section", ""))),
	)
	// Reversed order in curr; ids prove which is which.
	curr := snap("u",
		mkNode("button", "Submit", withRef("9"), withID("signup-submit"),
			withAncestors(anc("section", ""))),
		mkNode("button", "Submit", withRef("8"), withID("login-submit"),
			withAncestors(anc("section", ""))),
	)
	d := DiffSnapshots(prev, curr)
	if len(d.Added)+len(d.Removed)+len(d.Changed) != 0 {
		t.Fatalf("id-tier must pair across reorder when semantic keys collide: %+v", d)
	}
}

func TestDiff_BaselineReset_Diff_StringNotEmpty(t *testing.T) {
	curr := snap("u",
		mkNode("button", "Go", withRef("1")),
		mkNode("button", "Stop", withRef("2")),
	)
	d := DiffSnapshots(nil, curr)
	if d.Diff == "" {
		t.Fatalf("baseline reset Diff string should still render added nodes: %+v", d)
	}
	if !strings.Contains(d.Diff, "+ button") {
		t.Fatalf("baseline reset diff should use '+' prefix: %q", d.Diff)
	}
}
