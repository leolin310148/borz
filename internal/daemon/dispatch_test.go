package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestIntPtr(t *testing.T) {
	p := intPtr(42)
	if p == nil || *p != 42 {
		t.Fatalf("intPtr: got %v", p)
	}
}

func TestDerefInt(t *testing.T) {
	if derefInt(nil) != 0 {
		t.Fatal("nil -> 0")
	}
	v := 7
	if derefInt(&v) != 7 {
		t.Fatal("non-nil -> value")
	}
}

func TestOkResp(t *testing.T) {
	r := okResp("id1", &protocol.ResponseData{})
	if r.ID != "id1" || !r.Success || r.Data == nil {
		t.Fatalf("okResp: %+v", r)
	}
}

func TestFailResp(t *testing.T) {
	r := failResp("id2", "boom")
	if r.ID != "id2" || r.Success || r.Error != "boom" {
		t.Fatalf("failResp string: %+v", r)
	}
	// Non-string error is formatted via %v.
	r = failResp("id3", 42)
	if r.Error != "42" {
		t.Fatalf("failResp int: %+v", r)
	}
}

func TestSiteDomainMatchesURL(t *testing.T) {
	cases := []struct {
		domain string
		rawURL string
		want   bool
	}{
		{"example.com", "https://example.com/path", true},
		{"example.com", "https://app.example.com/path", true},
		{"https://example.com/docs", "https://example.com/path", true},
		{"*.example.com", "https://cdn.example.com", true},
		{"example.com", "https://evil-example.com", false},
		{"example.com", "about:blank", false},
	}
	for _, tc := range cases {
		if got := siteDomainMatchesURL(tc.domain, tc.rawURL); got != tc.want {
			t.Errorf("siteDomainMatchesURL(%q, %q) = %v, want %v", tc.domain, tc.rawURL, got, tc.want)
		}
	}
}

func TestLoadBuildDomTreeScript(t *testing.T) {
	s := loadBuildDomTreeScript()
	if s == "" {
		t.Fatal("embedded buildDomTree.js missing")
	}
	// Calling twice uses the once-cached value.
	if loadBuildDomTreeScript() != s {
		t.Fatal("cached script should be identical")
	}
}

func TestResolveKey_Special(t *testing.T) {
	def := resolveKey("Enter")
	if def.Key != "Enter" || def.Code != "Enter" || def.KeyCode != 13 || def.Text != "\r" {
		t.Fatalf("Enter: %+v", def)
	}
	if got := resolveKey("Esc"); got.Key != "Escape" {
		t.Fatalf("Esc alias: %+v", got)
	}
}

func TestResolveKey_SinglePrintable(t *testing.T) {
	// Lowercase letter.
	def := resolveKey("a")
	if def.Key != "a" || def.Text != "a" || def.KeyCode != int('A') || def.Code != "KeyA" {
		t.Fatalf("a: %+v", def)
	}
	// Uppercase letter.
	def = resolveKey("Z")
	if def.KeyCode != int('Z') || def.Code != "KeyZ" {
		t.Fatalf("Z: %+v", def)
	}
	// Digit.
	def = resolveKey("7")
	if def.KeyCode != int('7') || def.Code != "Digit7" {
		t.Fatalf("7: %+v", def)
	}
	// Space.
	def = resolveKey(" ")
	if def.KeyCode != 32 || def.Code != "Space" {
		t.Fatalf("space: %+v", def)
	}
	// Punctuation (no switch branch applies).
	def = resolveKey("#")
	if def.Key != "#" || def.Text != "#" || def.KeyCode != 0 || def.Code != "" {
		t.Fatalf("#: %+v", def)
	}
	// Multi-rune unknown key: only Key is set.
	def = resolveKey("HelloWorld")
	if def.Key != "HelloWorld" || def.Code != "" || def.KeyCode != 0 {
		t.Fatalf("multi-rune: %+v", def)
	}
}

func TestModifierMask(t *testing.T) {
	if modifierMask(nil) != 0 {
		t.Fatal("nil -> 0")
	}
	if modifierMask([]string{}) != 0 {
		t.Fatal("empty -> 0")
	}
	if modifierMask([]string{"alt"}) != 1 {
		t.Fatal("alt -> 1")
	}
	if modifierMask([]string{"ctrl"}) != 2 || modifierMask([]string{"control"}) != 2 {
		t.Fatal("ctrl/control -> 2")
	}
	if modifierMask([]string{"meta"}) != 4 || modifierMask([]string{"cmd"}) != 4 || modifierMask([]string{"command"}) != 4 {
		t.Fatal("meta family -> 4")
	}
	if modifierMask([]string{"shift"}) != 8 {
		t.Fatal("shift -> 8")
	}
	// Case insensitive.
	if modifierMask([]string{"SHIFT", "Alt"}) != 9 {
		t.Fatal("case insensitive combo")
	}
	// All four combined.
	if got := modifierMask([]string{"alt", "ctrl", "meta", "shift"}); got != 15 {
		t.Fatalf("all: got %d want 15", got)
	}
	// Unknown modifier is ignored.
	if modifierMask([]string{"fn"}) != 0 {
		t.Fatal("unknown -> 0")
	}
}

func TestNormalizeHeaders(t *testing.T) {
	// Valid map with mixed types.
	raw := json.RawMessage(`{"Content-Type":"application/json","X-Count":3}`)
	got := normalizeHeaders(raw)
	if got["Content-Type"] != "application/json" {
		t.Fatalf("string header: %+v", got)
	}
	if got["X-Count"] != "3" {
		t.Fatalf("numeric header coerced to string: %+v", got)
	}

	// Invalid JSON returns nil.
	if got := normalizeHeaders(json.RawMessage(`not json`)); got != nil {
		t.Fatalf("invalid: got %+v", got)
	}

	// Null returns nil (json.Unmarshal of null into map -> success with nil map).
	got = normalizeHeaders(json.RawMessage(`null`))
	if len(got) != 0 {
		t.Fatalf("null: got %+v", got)
	}
}

func TestDispatchRequest_NotConnected(t *testing.T) {
	// Without a live CDP connection, DispatchRequest short-circuits with an
	// error. That's enough to cover the early-exit path.
	tabs := NewTabStateManager()
	cdp := NewCdpConnection("127.0.0.1", 9222, tabs)
	resp := DispatchRequest(cdp, &protocol.Request{ID: "x", Action: "does-not-exist"})
	if resp == nil || resp.Success {
		t.Fatalf("expected failure, got %+v", resp)
	}
	if !strings.Contains(strings.ToLower(resp.Error), "cdp") {
		t.Fatalf("expected CDP-related error, got %q", resp.Error)
	}
}
