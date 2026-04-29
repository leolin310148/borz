package main

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestIsRemoteBind(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"127.0.0.1", false},
		{"localhost", false},
		{"::1", false},
		{"0.0.0.0", true},
		{"10.0.0.1", true},
		{"example.com", true},
		{"", true},
	} {
		if got := isRemoteBind(tc.in); got != tc.want {
			t.Errorf("isRemoteBind(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"":     "",
		"e1":   "e1",
		"@e1":  "e1",
		"@@e1": "@e1", // only leading @ stripped
		"e@1":  "e@1",
	} {
		if got := normalizeRef(in); got != want {
			t.Errorf("normalizeRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewID(t *testing.T) {
	id := newID()
	if len(id) != 16 {
		t.Fatalf("length: %d", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	if newID() == id {
		t.Fatal("IDs should differ")
	}
}

func TestHasFlag(t *testing.T) {
	args := []string{"cmd", "--json", "--verbose", "value"}
	if !hasFlag(args, "--json") {
		t.Error("--json should be found")
	}
	if hasFlag(args, "--missing") {
		t.Error("--missing should not be found")
	}
	if hasFlag(nil, "--json") {
		t.Error("nil args should not find anything")
	}
}

func TestGetArgValue(t *testing.T) {
	args := []string{"cmd", "--id", "42", "--name", "foo"}
	if got := getArgValue(args, "--id"); got != "42" {
		t.Errorf("--id: got %q want 42", got)
	}
	if got := getArgValue(args, "--name"); got != "foo" {
		t.Errorf("--name: got %q want foo", got)
	}
	if got := getArgValue(args, "--missing"); got != "" {
		t.Errorf("missing: got %q want empty", got)
	}
	// Flag at the end with no following value returns empty.
	if got := getArgValue([]string{"--id"}, "--id"); got != "" {
		t.Errorf("trailing flag: got %q want empty", got)
	}
}

func TestStripFlags(t *testing.T) {
	in := []string{"open", "--json", "--id", "1", "https://x", "--filter", "foo", "arg2"}
	got := stripFlags(in, nil, nil)
	// --json is stripped (hardcoded list), --id takes a value so both are stripped,
	// --filter takes a value so both are stripped.
	want := []string{"open", "https://x", "arg2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stripFlags: got %v want %v", got, want)
	}

	// Custom value flag strips pair.
	in = []string{"cmd", "--custom", "val", "keep"}
	got = stripFlags(in, []string{"--custom"}, nil)
	if !reflect.DeepEqual(got, []string{"cmd", "keep"}) {
		t.Errorf("custom value flag: %v", got)
	}

	// Custom bool flag strips just the flag.
	in = []string{"cmd", "--bool", "keep"}
	got = stripFlags(in, nil, []string{"--bool"})
	if !reflect.DeepEqual(got, []string{"cmd", "keep"}) {
		t.Errorf("custom bool flag: %v", got)
	}

	in = []string{"--remote", "open", "https://x"}
	got = stripFlags(in, nil, []string{"--remote"})
	if !reflect.DeepEqual(got, []string{"open", "https://x"}) {
		t.Errorf("--remote global flag: %v", got)
	}
}

func TestSetTab(t *testing.T) {
	req := &protocol.Request{}
	setTab(req, "")
	if req.TabID != nil {
		t.Error("empty string should not set TabID")
	}
	setTab(req, "abc")
	if req.TabID != "abc" {
		t.Errorf("TabID: %v", req.TabID)
	}
}

func TestSetSince(t *testing.T) {
	// Empty string: no change.
	req := &protocol.Request{}
	setSince(req, "")
	if req.Since != nil {
		t.Error("empty should not set Since")
	}

	// last_action sentinel preserved as string.
	req = &protocol.Request{}
	setSince(req, "last_action")
	if s, ok := req.Since.(string); !ok || s != "last_action" {
		t.Errorf("last_action: got %v", req.Since)
	}

	// Numeric string parsed as int.
	req = &protocol.Request{}
	setSince(req, "42")
	if n, ok := req.Since.(int); !ok || n != 42 {
		t.Errorf("numeric: got %v", req.Since)
	}

	// Garbage leaves Since unchanged.
	req = &protocol.Request{}
	setSince(req, "not-numeric")
	if req.Since != nil {
		t.Errorf("garbage should be ignored: %v", req.Since)
	}
}

func TestResolveIdleTabTimeout(t *testing.T) {
	// Default when neither flag nor env present.
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")
	if got := resolveIdleTabTimeout(nil); got != config.DefaultIdleTabCloseMinutes {
		t.Errorf("default: got %d, want %d", got, config.DefaultIdleTabCloseMinutes)
	}

	// Current env wins over default.
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "20")
	if got := resolveIdleTabTimeout(nil); got != 20 {
		t.Errorf("current env: got %d, want 20", got)
	}
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")

	// Legacy env wins over default.
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "15")
	if got := resolveIdleTabTimeout(nil); got != 15 {
		t.Errorf("legacy env: got %d, want 15", got)
	}

	// Flag wins over env.
	if got := resolveIdleTabTimeout([]string{"--idle-tab-timeout", "5"}); got != 5 {
		t.Errorf("flag: got %d, want 5", got)
	}

	// 0 disables.
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "0")
	if got := resolveIdleTabTimeout(nil); got != 0 {
		t.Errorf("env=0: got %d, want 0", got)
	}

	// Negative clamps to 0.
	if got := resolveIdleTabTimeout([]string{"--idle-tab-timeout", "-7"}); got != 0 {
		t.Errorf("negative flag: got %d, want 0", got)
	}

	// Garbage flag falls through to env.
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "12")
	if got := resolveIdleTabTimeout([]string{"--idle-tab-timeout", "abc"}); got != 12 {
		t.Errorf("garbage flag: got %d, want env=12", got)
	}

	// Garbage env falls through to default.
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "abc")
	if got := resolveIdleTabTimeout(nil); got != config.DefaultIdleTabCloseMinutes {
		t.Errorf("garbage env: got %d, want %d", got, config.DefaultIdleTabCloseMinutes)
	}
}
