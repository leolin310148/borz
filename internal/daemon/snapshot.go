package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

// Raw DOM types from buildDomTree.js output
type rawDomTextNode struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	IsVisible bool   `json:"isVisible"`
}

type rawDomElementNode struct {
	TagName        string            `json:"tagName"`
	XPath          string            `json:"xpath"`
	Attributes     map[string]string `json:"attributes"`
	Children       []string          `json:"children"`
	IsVisible      bool              `json:"isVisible"`
	IsInteractive  bool              `json:"isInteractive"`
	IsTopElement   bool              `json:"isTopElement"`
	IsInViewport   bool              `json:"isInViewport"`
	HighlightIndex *int              `json:"highlightIndex"`
	ShadowRoot     bool              `json:"shadowRoot"`
}

type buildDomTreeResult struct {
	RootID string                     `json:"rootId"`
	Map    map[string]json.RawMessage `json:"map"`
}

func parseNode(raw json.RawMessage) (isText bool, text rawDomTextNode, el rawDomElementNode) {
	// Try text node first
	var t rawDomTextNode
	if json.Unmarshal(raw, &t) == nil && t.Type == "TEXT_NODE" {
		return true, t, rawDomElementNode{}
	}
	var e rawDomElementNode
	json.Unmarshal(raw, &e)
	return false, rawDomTextNode{}, e
}

var roleMap = map[string]string{
	"a": "link", "button": "button", "select": "combobox",
	"textarea": "textbox", "img": "image", "nav": "navigation",
	"main": "main", "header": "banner", "footer": "contentinfo",
	"aside": "complementary", "form": "form", "table": "table",
	"ul": "list", "ol": "list", "li": "listitem",
	"h1": "heading", "h2": "heading", "h3": "heading",
	"h4": "heading", "h5": "heading", "h6": "heading",
	"dialog": "dialog", "article": "article", "section": "region",
	"label": "label", "details": "group", "summary": "button",
}

var inputRoleMap = map[string]string{
	"text": "textbox", "password": "textbox", "email": "textbox",
	"url": "textbox", "tel": "textbox", "search": "searchbox",
	"number": "spinbutton", "range": "slider", "checkbox": "checkbox",
	"radio": "radio", "button": "button", "submit": "button",
	"reset": "button", "file": "button",
}

func getRole(el rawDomElementNode) string {
	tagName := strings.ToLower(el.TagName)
	if role, ok := el.Attributes["role"]; ok && role != "" {
		return role
	}
	if tagName == "input" {
		inputType := strings.ToLower(el.Attributes["type"])
		if inputType == "" {
			inputType = "text"
		}
		if role, ok := inputRoleMap[inputType]; ok {
			return role
		}
		return "textbox"
	}
	if role, ok := roleMap[tagName]; ok {
		return role
	}
	return tagName
}

func collectTextContent(el rawDomElementNode, nodeMap map[string]json.RawMessage, depthLimit int) string {
	var texts []string
	var visit func(nodeID string, depth int)
	visit = func(nodeID string, depth int) {
		if depth > depthLimit {
			return
		}
		raw, ok := nodeMap[nodeID]
		if !ok {
			return
		}
		isText, textNode, childEl := parseNode(raw)
		if isText {
			t := strings.TrimSpace(textNode.Text)
			if t != "" {
				texts = append(texts, t)
			}
			return
		}
		for _, childID := range childEl.Children {
			visit(childID, depth+1)
		}
	}
	for _, childID := range el.Children {
		visit(childID, 0)
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

func getName(el rawDomElementNode, nodeMap map[string]json.RawMessage) string {
	attrs := el.Attributes
	if v := attrs["aria-label"]; v != "" {
		return v
	}
	if v := attrs["title"]; v != "" {
		return v
	}
	if v := attrs["placeholder"]; v != "" {
		return v
	}
	if v := attrs["alt"]; v != "" {
		return v
	}
	if v := attrs["value"]; v != "" {
		return v
	}
	if text := collectTextContent(el, nodeMap, 5); text != "" {
		return text
	}
	if v := attrs["name"]; v != "" {
		return v
	}
	return ""
}

func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

func matchesSelector(el rawDomElementNode, role, name, selector string) bool {
	if selector == "" {
		return true
	}
	lower := strings.ToLower(selector)
	haystack := strings.ToLower(strings.Join([]string{
		el.TagName, role, name, el.XPath,
	}, " "))
	for _, v := range el.Attributes {
		haystack += " " + strings.ToLower(v)
	}
	return strings.Contains(haystack, lower)
}

// matchesRole returns true when roleFilter is empty or exactly matches the
// element's role (case-insensitive). Unlike selector, this is not a substring
// match — a filter of "button" will not match role="textbox".
func matchesRole(role, roleFilter string) bool {
	if roleFilter == "" {
		return true
	}
	return strings.EqualFold(role, roleFilter)
}

// ConvertBuildDomTreeResult converts buildDomTree.js output to an accessibility tree.
//
// selector is a case-insensitive substring match across tag/role/name/xpath/attr values.
// roleFilter, when non-empty, requires an exact (case-insensitive) role match and is AND'd
// with selector. Both default to "match anything" when empty.
func ConvertBuildDomTreeResult(result *buildDomTreeResult, interactiveOnly, compact bool, maxDepth *int, selector, roleFilter string) *protocol.SnapshotData {
	if result == nil || result.Map == nil || result.RootID == "" {
		return &protocol.SnapshotData{Snapshot: "", Refs: map[string]*protocol.RefInfo{}, Elements: []*protocol.ElementInfo{}}
	}

	refs := make(map[string]*protocol.RefInfo)
	var elements []*protocol.ElementInfo
	var lines []string

	if interactiveOnly {
		// Collect interactive nodes sorted by highlightIndex
		type indexedNode struct {
			index int
			el    rawDomElementNode
		}
		var nodes []indexedNode

		for _, raw := range result.Map {
			isText, _, el := parseNode(raw)
			if isText || el.HighlightIndex == nil {
				continue
			}
			nodes = append(nodes, indexedNode{index: *el.HighlightIndex, el: el})
		}

		// Sort by highlight index
		for i := 0; i < len(nodes); i++ {
			for j := i + 1; j < len(nodes); j++ {
				if nodes[j].index < nodes[i].index {
					nodes[i], nodes[j] = nodes[j], nodes[i]
				}
			}
		}

		for _, n := range nodes {
			refID := fmt.Sprintf("%d", n.index)
			role := getRole(n.el)
			name := getName(n.el, result.Map)
			if !matchesSelector(n.el, role, name, selector) || !matchesRole(role, roleFilter) {
				continue
			}
			line := fmt.Sprintf("%s [ref=%s]", role, refID)
			if name != "" {
				line += fmt.Sprintf(" %q", truncateText(name, 50))
			}
			lines = append(lines, line)
			tag := strings.ToLower(n.el.TagName)
			xpath := el2xpath(n.el)
			refs[refID] = &protocol.RefInfo{
				XPath:   xpath,
				Role:    role,
				Name:    name,
				TagName: tag,
			}
			elements = append(elements, &protocol.ElementInfo{
				Ref:     refID,
				XPath:   xpath,
				Role:    role,
				Name:    name,
				TagName: tag,
			})
		}
		return &protocol.SnapshotData{Snapshot: strings.Join(lines, "\n"), Refs: refs, Elements: elements}
	}

	// Full tree walk
	var walk func(nodeID string, depth int)
	walk = func(nodeID string, depth int) {
		if maxDepth != nil && depth > *maxDepth {
			return
		}
		raw, ok := result.Map[nodeID]
		if !ok {
			return
		}

		isText, textNode, el := parseNode(raw)
		if isText {
			text := strings.TrimSpace(textNode.Text)
			if text == "" {
				return
			}
			maxLen := 120
			if compact {
				maxLen = 80
			}
			lines = append(lines, fmt.Sprintf("%s- text %q", strings.Repeat("  ", depth), truncateText(text, maxLen)))
			return
		}

		role := getRole(el)
		name := getName(el, result.Map)
		if !matchesSelector(el, role, name, selector) || !matchesRole(role, roleFilter) {
			for _, childID := range el.Children {
				walk(childID, depth+1)
			}
			return
		}

		indent := strings.Repeat("  ", depth)
		var refID string
		if el.HighlightIndex != nil {
			refID = fmt.Sprintf("%d", *el.HighlightIndex)
		}

		line := fmt.Sprintf("%s- %s", indent, role)
		if refID != "" {
			line += fmt.Sprintf(" [ref=%s]", refID)
		}
		nameMaxLen := 80
		if compact {
			nameMaxLen = 50
		}
		if name != "" {
			line += fmt.Sprintf(" %q", truncateText(name, nameMaxLen))
		}
		if !compact {
			line += fmt.Sprintf(" <%s>", strings.ToLower(el.TagName))
		}
		lines = append(lines, line)

		if refID != "" {
			tag := strings.ToLower(el.TagName)
			xpath := el2xpath(el)
			refs[refID] = &protocol.RefInfo{
				XPath:   xpath,
				Role:    role,
				Name:    name,
				TagName: tag,
			}
			elements = append(elements, &protocol.ElementInfo{
				Ref:     refID,
				XPath:   xpath,
				Role:    role,
				Name:    name,
				TagName: tag,
			})
		}

		for _, childID := range el.Children {
			walk(childID, depth+1)
		}
	}

	walk(result.RootID, 0)
	return &protocol.SnapshotData{Snapshot: strings.Join(lines, "\n"), Refs: refs, Elements: elements}
}

func el2xpath(el rawDomElementNode) string {
	if el.XPath != "" {
		return el.XPath
	}
	return ""
}
