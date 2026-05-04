package daemon

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

// AncestorStep is one step in a node's role[name] ancestor chain, used for
// computing a stable semantic key independent of [ref=N] which gets reassigned
// every snapshot.
type AncestorStep struct {
	Role string
	Name string
}

// DiffNode is the per-element view of a snapshot needed to diff against
// another. Refs are NOT used for cross-snapshot identity (they are
// walk-order indexes that get reassigned each snapshot); they are kept on
// the struct so the diff result can echo the *current* ref back to agents.
type DiffNode struct {
	Ref       string
	Role      string
	Name      string
	TagName   string
	XPath     string
	ID        string
	Attrs     map[string]string
	Ancestors []AncestorStep
}

// DiffSnapshot is the input to DiffSnapshots.
type DiffSnapshot struct {
	URL   string
	Nodes []*DiffNode
}

// Re-exports of the protocol types for ergonomics inside the daemon
// package. Tests and callers can use the short names without importing
// protocol.
type (
	DiffEntry        = protocol.DiffEntry
	DiffChange       = protocol.DiffChange
	AttrDelta        = protocol.AttrDelta
	DiffStats       = protocol.DiffStats
	SnapshotDiffData = protocol.SnapshotDiffData
)

// trackedAttrs is the closed set of element attributes that DiffSnapshots
// watches for changes. Other attribute changes (class, style, data-*) are
// ignored — they're too noisy to be useful for an agent.
var trackedAttrs = []string{
	"aria-pressed", "aria-checked", "aria-expanded", "aria-selected",
	"aria-disabled", "disabled",
	"aria-hidden",
	"value", "checked",
	"aria-current", "aria-busy", "aria-invalid",
}

// BuildDiffSnapshot walks a buildDomTreeResult and produces a DiffSnapshot
// suitable as input to DiffSnapshots. The walk is the same shape as
// ConvertBuildDomTreeResult but emits one DiffNode per element node with
// role+name ancestor chains populated.
//
// Only attributes in trackedAttrs (plus the raw "id") are kept on each
// DiffNode — other attributes (class, style, data-*) are not retained
// because DiffSnapshots ignores them anyway, and skipping them keeps the
// per-tab baseline small.
func BuildDiffSnapshot(result *buildDomTreeResult, url string) *DiffSnapshot {
	if result == nil || result.Map == nil || result.RootID == "" {
		return &DiffSnapshot{URL: url}
	}
	var nodes []*DiffNode
	var walk func(nodeID string, ancestors []AncestorStep)
	walk = func(nodeID string, ancestors []AncestorStep) {
		raw, ok := result.Map[nodeID]
		if !ok {
			return
		}
		isText, _, el := parseNode(raw)
		if isText {
			return
		}
		role := getRole(el)
		name := getName(el, result.Map)
		ref := ""
		if el.HighlightIndex != nil {
			ref = fmt.Sprintf("%d", *el.HighlightIndex)
		}
		node := &DiffNode{
			Ref:       ref,
			Role:      role,
			Name:      name,
			TagName:   strings.ToLower(el.TagName),
			XPath:     el.XPath,
			ID:        el.Attributes["id"],
			Attrs:     copyTrackedAttrs(el.Attributes),
			Ancestors: append([]AncestorStep(nil), ancestors...),
		}
		nodes = append(nodes, node)

		// Children inherit this node as the next ancestor step. We allocate a
		// fresh slice rather than appending in place so sibling subtrees don't
		// share storage and corrupt each other's ancestor chains.
		childAncestors := make([]AncestorStep, len(ancestors)+1)
		copy(childAncestors, ancestors)
		childAncestors[len(ancestors)] = AncestorStep{Role: role, Name: name}
		for _, childID := range el.Children {
			walk(childID, childAncestors)
		}
	}
	walk(result.RootID, nil)
	return &DiffSnapshot{URL: url, Nodes: nodes}
}

func copyTrackedAttrs(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	var dst map[string]string
	for _, k := range trackedAttrs {
		if v, ok := src[k]; ok {
			if dst == nil {
				dst = make(map[string]string, len(trackedAttrs))
			}
			dst[k] = v
		}
	}
	return dst
}

// Auto-generated id patterns. ids matching any of these are not stable
// across renders and must NOT be used for cross-snapshot identity.
var (
	autogenReact     = regexp.MustCompile(`^react-`)
	autogenRadix     = regexp.MustCompile(`^radix-`)
	autogenMUI       = regexp.MustCompile(`^mui-`)
	autogenRadixBare = regexp.MustCompile(`^:r[0-9a-zA-Z]+:$`)
	autogenHexHash   = regexp.MustCompile(`^[0-9a-fA-F]{8,}$`)
)

// IsStableID returns true when id looks like a human-authored, stable
// identifier. It returns false for empty strings and for common
// auto-generated id patterns from React, Radix, MUI, and hash-y ids.
func IsStableID(id string) bool {
	if id == "" {
		return false
	}
	if autogenReact.MatchString(id) ||
		autogenRadix.MatchString(id) ||
		autogenMUI.MatchString(id) ||
		autogenRadixBare.MatchString(id) ||
		autogenHexHash.MatchString(id) {
		return false
	}
	return true
}

// SemanticKey returns the role[name] > role[name] > ... chain for n
// (root → self). Empty-role-and-name ancestors are skipped so that
// inserting a structural wrapper does not churn the key.
func (n *DiffNode) SemanticKey() string {
	var parts []string
	for _, a := range n.Ancestors {
		if a.Role == "" && a.Name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", a.Role, a.Name))
	}
	parts = append(parts, fmt.Sprintf("%s[%s]", n.Role, n.Name))
	return strings.Join(parts, " > ")
}

// bestKey returns the most-specific stable key for n: stable id > semantic
// key > xpath. Always non-empty.
func bestKey(n *DiffNode) string {
	if IsStableID(n.ID) {
		return "id:" + n.ID
	}
	if n.Role == "" && n.Name == "" && n.XPath != "" {
		return "xpath:" + n.XPath
	}
	return "sem:" + n.SemanticKey()
}

// DiffSnapshots computes the structural diff prev → curr.
//
// Behavior:
//   - prev == nil OR prev.URL != curr.URL: BaselineReset=true and every
//     node in curr.Nodes lands in Added; Removed/Changed are empty.
//   - Otherwise nodes are matched in tiers: stable id, then semantic key
//     (with positional disambiguation among siblings sharing a key), then
//     xpath as a last-resort structural fallback.
//   - Unmatched prev nodes → Removed (carrying their previous ref).
//   - Unmatched curr nodes → Added (carrying their current ref).
//   - Matched pairs with tracked-attr or name changes → Changed.
func DiffSnapshots(prev, curr *DiffSnapshot) *SnapshotDiffData {
	if curr == nil {
		return &SnapshotDiffData{}
	}

	if prev == nil || prev.URL != curr.URL {
		d := &SnapshotDiffData{BaselineReset: true}
		for _, n := range curr.Nodes {
			d.Added = append(d.Added, entryFor(n))
		}
		d.Stats.Added = len(d.Added)
		d.Diff = formatDiff(d)
		return d
	}

	prevMatched := make([]bool, len(prev.Nodes))
	currMatched := make([]bool, len(curr.Nodes))

	type pair struct {
		pi, ci int
		key    string
	}
	var pairs []pair

	// Pass 1: stable id.
	prevByID := map[string]int{}
	for i, n := range prev.Nodes {
		if !IsStableID(n.ID) {
			continue
		}
		if _, ok := prevByID[n.ID]; !ok {
			prevByID[n.ID] = i
		}
	}
	for j, n := range curr.Nodes {
		if !IsStableID(n.ID) {
			continue
		}
		if i, ok := prevByID[n.ID]; ok && !prevMatched[i] && !currMatched[j] {
			prevMatched[i] = true
			currMatched[j] = true
			pairs = append(pairs, pair{pi: i, ci: j, key: "id:" + n.ID})
		}
	}

	// Pass 2: semantic key with positional disambiguation among unmatched.
	prevSem := map[string][]int{}
	for i, n := range prev.Nodes {
		if prevMatched[i] {
			continue
		}
		k := n.SemanticKey()
		prevSem[k] = append(prevSem[k], i)
	}
	currSem := map[string][]int{}
	for j, n := range curr.Nodes {
		if currMatched[j] {
			continue
		}
		k := n.SemanticKey()
		currSem[k] = append(currSem[k], j)
	}
	// Iterate prev keys in a deterministic order so pairing is stable.
	semKeys := make([]string, 0, len(prevSem))
	for k := range prevSem {
		semKeys = append(semKeys, k)
	}
	sort.Strings(semKeys)
	for _, k := range semKeys {
		prevIdxs := prevSem[k]
		currIdxs := currSem[k]
		m := len(prevIdxs)
		if len(currIdxs) < m {
			m = len(currIdxs)
		}
		for x := 0; x < m; x++ {
			pi := prevIdxs[x]
			ci := currIdxs[x]
			prevMatched[pi] = true
			currMatched[ci] = true
			pairs = append(pairs, pair{pi: pi, ci: ci, key: "sem:" + k})
		}
	}

	// Pass 3: xpath fallback among remaining unmatched.
	prevByXP := map[string]int{}
	for i, n := range prev.Nodes {
		if prevMatched[i] || n.XPath == "" {
			continue
		}
		if _, ok := prevByXP[n.XPath]; !ok {
			prevByXP[n.XPath] = i
		}
	}
	for j, n := range curr.Nodes {
		if currMatched[j] || n.XPath == "" {
			continue
		}
		if i, ok := prevByXP[n.XPath]; ok && !prevMatched[i] {
			prevMatched[i] = true
			currMatched[j] = true
			pairs = append(pairs, pair{pi: i, ci: j, key: "xpath:" + n.XPath})
		}
	}

	d := &SnapshotDiffData{}

	// Added: curr nodes still unmatched, in input order.
	for j, n := range curr.Nodes {
		if currMatched[j] {
			continue
		}
		d.Added = append(d.Added, entryFor(n))
	}
	// Removed: prev nodes still unmatched, in input order.
	for i, n := range prev.Nodes {
		if prevMatched[i] {
			continue
		}
		d.Removed = append(d.Removed, entryFor(n))
	}

	// Changed: matched pairs whose tracked attrs or name differ.
	// Sort by curr index for stable output.
	sort.Slice(pairs, func(a, b int) bool { return pairs[a].ci < pairs[b].ci })
	for _, p := range pairs {
		pn := prev.Nodes[p.pi]
		cn := curr.Nodes[p.ci]
		attrChanges := map[string]AttrDelta{}
		for _, attr := range trackedAttrs {
			pv := pn.Attrs[attr]
			cv := cn.Attrs[attr]
			if pv != cv {
				attrChanges[attr] = AttrDelta{Old: pv, New: cv}
			}
		}
		var nameChanged *AttrDelta
		if pn.Name != cn.Name {
			nameChanged = &AttrDelta{Old: pn.Name, New: cn.Name}
		}
		if len(attrChanges) == 0 && nameChanged == nil {
			d.Stats.Unchanged++
			continue
		}
		d.Changed = append(d.Changed, &DiffChange{
			Ref:         cn.Ref,
			PrevRef:     pn.Ref,
			Role:        cn.Role,
			Name:        cn.Name,
			TagName:     cn.TagName,
			XPath:       cn.XPath,
			Key:         p.key,
			NameChanged: nameChanged,
			AttrChanges: attrChanges,
		})
	}

	d.Stats.Added = len(d.Added)
	d.Stats.Removed = len(d.Removed)
	d.Stats.Changed = len(d.Changed)
	d.Diff = formatDiff(d)
	return d
}

func entryFor(n *DiffNode) *DiffEntry {
	return &DiffEntry{
		Ref:     n.Ref,
		Role:    n.Role,
		Name:    n.Name,
		TagName: n.TagName,
		XPath:   n.XPath,
		Key:     bestKey(n),
	}
}

func formatDiff(d *SnapshotDiffData) string {
	var lines []string
	for _, e := range d.Added {
		lines = append(lines, formatEntryLine("+", e, false))
	}
	for _, c := range d.Changed {
		lines = append(lines, formatChangeLine(c))
	}
	for _, e := range d.Removed {
		lines = append(lines, formatEntryLine("-", e, true))
	}
	return strings.Join(lines, "\n")
}

func formatEntryLine(prefix string, e *DiffEntry, isRemoved bool) string {
	role := e.Role
	if role == "" {
		role = e.TagName
	}
	line := prefix + " " + role
	if e.Ref != "" {
		if isRemoved {
			line += fmt.Sprintf(" (was [ref=%s])", e.Ref)
		} else {
			line += fmt.Sprintf(" [ref=%s]", e.Ref)
		}
	}
	if e.Name != "" {
		line += fmt.Sprintf(" %q", e.Name)
	}
	return line
}

func formatChangeLine(c *DiffChange) string {
	role := c.Role
	if role == "" {
		role = c.TagName
	}
	line := "~ " + role
	if c.Ref != "" {
		line += fmt.Sprintf(" [ref=%s]", c.Ref)
	}
	if c.Name != "" {
		line += fmt.Sprintf(" %q", c.Name)
	}
	var details []string
	if c.NameChanged != nil {
		details = append(details, fmt.Sprintf("name %q→%q", c.NameChanged.Old, c.NameChanged.New))
	}
	keys := make([]string, 0, len(c.AttrChanges))
	for k := range c.AttrChanges {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ad := c.AttrChanges[k]
		details = append(details, fmt.Sprintf("%s %s→%s", k, ad.Old, ad.New))
	}
	if len(details) > 0 {
		line += "  " + strings.Join(details, " ")
	}
	return line
}
