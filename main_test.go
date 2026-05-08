package main

import (
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
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

func TestServerOptionsFromArgs(t *testing.T) {
	t.Setenv("BORZ_SERVER_HOST", "")
	t.Setenv("BB_BROWSER_SERVER_HOST", "")
	t.Setenv("BORZ_SERVER_PORT", "")
	t.Setenv("BB_BROWSER_SERVER_PORT", "")
	t.Setenv("BORZ_TOKEN", "")
	t.Setenv("BB_BROWSER_TOKEN", "")
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")

	opts, err := serverOptionsFromArgs([]string{"server", "--host", "127.0.0.1", "--port", "19999", "--cdp-host", "chrome", "--cdp-port", "9222", "--idle-tab-timeout", "7"}, "0.0.0.0")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "127.0.0.1" || opts.Port != 19999 || opts.CDPHost != "chrome" || opts.CDPPort != 9222 || opts.IdleTabCloseMinutes != 7 || opts.Version != version {
		t.Fatalf("unexpected options: %+v", opts)
	}

	if _, err := serverOptionsFromArgs([]string{"server", "--host", "0.0.0.0"}, "0.0.0.0"); err == nil {
		t.Fatal("expected non-loopback host without token to fail")
	}

	opts, err = serverOptionsFromArgs([]string{"service", "install"}, "127.0.0.1")
	if err != nil {
		t.Fatalf("service default should be loopback without token: %v", err)
	}
	if opts.Host != "127.0.0.1" {
		t.Fatalf("service default host = %q, want loopback", opts.Host)
	}
}

func TestServerOptionsFromArgsTrimsPortValues(t *testing.T) {
	t.Setenv("BORZ_SERVER_HOST", "")
	t.Setenv("BB_BROWSER_SERVER_HOST", "")
	t.Setenv("BORZ_SERVER_PORT", " 20001 ")
	t.Setenv("BB_BROWSER_SERVER_PORT", "")
	t.Setenv("BORZ_TOKEN", "")
	t.Setenv("BB_BROWSER_TOKEN", "")
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")

	opts, err := serverOptionsFromArgs([]string{"server", "--host", "127.0.0.1", "--cdp-port", " 9224 "}, "0.0.0.0")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs returned error: %v", err)
	}
	if opts.Port != 20001 || opts.CDPPort != 9224 {
		t.Fatalf("ports = %d/%d, want 20001/9224", opts.Port, opts.CDPPort)
	}

	opts, err = serverOptionsFromArgs([]string{"server", "--host", "127.0.0.1", "--port", " 20002 "}, "0.0.0.0")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs with port flag returned error: %v", err)
	}
	if opts.Port != 20002 {
		t.Fatalf("port flag = %d, want 20002", opts.Port)
	}
}

func TestServerOptionsFromArgsTrimsStringValues(t *testing.T) {
	t.Setenv("BORZ_SERVER_HOST", " 127.0.0.1 ")
	t.Setenv("BB_BROWSER_SERVER_HOST", "")
	t.Setenv("BORZ_SERVER_PORT", "")
	t.Setenv("BB_BROWSER_SERVER_PORT", "")
	t.Setenv("BORZ_TOKEN", "")
	t.Setenv("BB_BROWSER_TOKEN", "")
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")

	opts, err := serverOptionsFromArgs([]string{"server", "--cdp-host", " chrome "}, "0.0.0.0")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "127.0.0.1" || opts.CDPHost != "chrome" {
		t.Fatalf("hosts = %q/%q, want 127.0.0.1/chrome", opts.Host, opts.CDPHost)
	}

	t.Setenv("BORZ_SERVER_HOST", "")
	t.Setenv("BORZ_TOKEN", " secret ")
	opts, err = serverOptionsFromArgs([]string{"server", "--host", " 0.0.0.0 "}, "0.0.0.0")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs with trimmed token returned error: %v", err)
	}
	if opts.Host != "0.0.0.0" || opts.Token != "secret" {
		t.Fatalf("host/token = %q/%q, want 0.0.0.0/secret", opts.Host, opts.Token)
	}
}

func TestServerOptionsFromArgsRejectsBlankToken(t *testing.T) {
	t.Setenv("BORZ_SERVER_HOST", "")
	t.Setenv("BB_BROWSER_SERVER_HOST", "")
	t.Setenv("BORZ_SERVER_PORT", "")
	t.Setenv("BB_BROWSER_SERVER_PORT", "")
	t.Setenv("BORZ_TOKEN", "   ")
	t.Setenv("BB_BROWSER_TOKEN", "")
	t.Setenv("BORZ_TAB_IDLE_TIMEOUT", "")
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "")

	if _, err := serverOptionsFromArgs([]string{"server", "--host", "0.0.0.0"}, "0.0.0.0"); err == nil {
		t.Fatal("expected whitespace-only token to fail for non-loopback host")
	}
}

func TestServiceRunArgs(t *testing.T) {
	t.Cleanup(func() { _ = config.SetProfile("") })
	opts, err := serverOptionsFromArgs([]string{"service", "install", "--host", "127.0.0.1", "--port", "19824", "--token", "secret"}, "127.0.0.1")
	if err != nil {
		t.Fatalf("serverOptionsFromArgs returned error: %v", err)
	}
	got := strings.Join(serviceRunArgs("borz-test", opts), " ")
	for _, want := range []string{
		"service run",
		"--name borz-test",
		"--host 127.0.0.1",
		"--port 19824",
		"--token secret",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("service args %q missing %q", got, want)
		}
	}

	if err := config.SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	got = strings.Join(serviceRunArgs("borz-test", opts), " ")
	if !strings.Contains(got, "--profile work") {
		t.Fatalf("service args %q missing profile", got)
	}
}

func TestNormalizeRef(t *testing.T) {
	for in, want := range map[string]string{
		"":      "",
		"e1":    "e1",
		"@e1":   "e1",
		" @e1 ": "e1",
		"@ e1 ": "e1",
		"@@e1":  "@e1", // only leading @ stripped
		"e@1":   "e@1",
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

func TestNewIDFailsWhenRandomReadFails(t *testing.T) {
	old := randomRead
	randomRead = func([]byte) (int, error) {
		return 0, errors.New("entropy unavailable")
	}
	t.Cleanup(func() { randomRead = old })

	expectExit(t, 1, func() { _ = newID() })
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
	args := []string{"cmd", "--id", "42", "--name=foo", "--empty="}
	if got := getArgValue(args, "--id"); got != "42" {
		t.Errorf("--id: got %q want 42", got)
	}
	if got := getArgValue(args, "--name"); got != "foo" {
		t.Errorf("--name: got %q want foo", got)
	}
	if got := getArgValue(args, "--empty"); got != "" {
		t.Errorf("--empty: got %q want empty", got)
	}
	if got := getArgValue(args, "--missing"); got != "" {
		t.Errorf("missing: got %q want empty", got)
	}
	// Flag at the end with no following value returns empty.
	if got := getArgValue([]string{"--id"}, "--id"); got != "" {
		t.Errorf("trailing flag: got %q want empty", got)
	}
	if got := getArgValue([]string{"--id", "--json"}, "--id"); got != "" {
		t.Errorf("missing value before next flag: got %q want empty", got)
	}
}

func TestGetAllArgValues(t *testing.T) {
	args := []string{"cmd", "--json-arg", "user={}", "--json-arg=limit=3", "--json-arg", "--json"}
	got := getAllArgValues(args, "--json-arg")
	want := []string{"user={}", "limit=3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getAllArgValues: got %v want %v", got, want)
	}
}

func TestParsePositiveIntArg(t *testing.T) {
	got, err := parsePositiveIntArg("--timeout", " 2500 ")
	if err != nil {
		t.Fatalf("parsePositiveIntArg returned error: %v", err)
	}
	if got != 2500 {
		t.Fatalf("parsePositiveIntArg = %d, want 2500", got)
	}

	if got, err := parsePositiveIntArg("--timeout", ""); err != nil || got != 0 {
		t.Fatalf("empty parsePositiveIntArg = %d, %v; want 0, nil", got, err)
	}

	for _, raw := range []string{"0", "-1", "abc"} {
		if _, err := parsePositiveIntArg("--timeout", raw); err == nil || !strings.Contains(err.Error(), "--timeout must be a positive integer") {
			t.Fatalf("parsePositiveIntArg(%q) error = %v", raw, err)
		}
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

	in = []string{"cmd", "--custom=val", "keep"}
	got = stripFlags(in, []string{"--custom"}, nil)
	if !reflect.DeepEqual(got, []string{"cmd", "keep"}) {
		t.Errorf("inline custom value flag: %v", got)
	}

	in = []string{"snapshot", "-d=3", "--selector=main", "--diff", "keep"}
	got = stripFlags(in, nil, nil)
	if !reflect.DeepEqual(got, []string{"snapshot", "keep"}) {
		t.Errorf("inline built-in value flags: %v", got)
	}

	in = []string{"screenshot", "--viewport", "1280x720", "--rect", "1,2,3,4", "keep"}
	got = stripFlags(in, nil, nil)
	if !reflect.DeepEqual(got, []string{"screenshot", "keep"}) {
		t.Errorf("shared CLI value flags: %v", got)
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

	in = []string{"--profile", "work", "open", "https://x"}
	got = stripFlags(in, []string{"--profile"}, nil)
	if !reflect.DeepEqual(got, []string{"open", "https://x"}) {
		t.Errorf("--profile global flag: %v", got)
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
	if got := resolveIdleTabTimeout([]string{"--idle-tab-timeout", " 6 "}); got != 6 {
		t.Errorf("whitespace flag: got %d, want 6", got)
	}

	// 0 disables.
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", "0")
	if got := resolveIdleTabTimeout(nil); got != 0 {
		t.Errorf("env=0: got %d, want 0", got)
	}
	t.Setenv("BB_BROWSER_TAB_IDLE_TIMEOUT", " 9 ")
	if got := resolveIdleTabTimeout(nil); got != 9 {
		t.Errorf("whitespace env: got %d, want 9", got)
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
