package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteScoringCommentsAndOrderingBranches(t *testing.T) {
	if got := orderedArgNames(nil); got != nil {
		t.Fatalf("orderedArgNames(nil) = %+v", got)
	}
	meta := &SiteMeta{Args: map[string]ArgDef{"b": {}, "a": {}}}
	if got := orderedArgNames(meta); !stringsEqual(got, []string{"a", "b"}) {
		t.Fatalf("orderedArgNames sorted = %+v", got)
	}

	s := &SiteMeta{Name: "alpha/search", Domain: "example.com", Description: "Find useful things"}
	cases := map[string]int{
		"alpha/search": 1000,
		"alpha":        900,
		"search":       800,
		"example.com":  700,
		"ample":        650,
		"useful":       500,
		"as":           300,
		"xm":           250,
		"ft":           150,
		"zzz":          0,
	}
	for q, want := range cases {
		if got := searchScore(s, q); got != want {
			t.Fatalf("searchScore(%q) = %d, want %d", q, got, want)
		}
	}
	if !fuzzyContains("abc", "") || fuzzyContains("abc", "acx") {
		t.Fatal("fuzzyContains edge mismatch")
	}
	if !looksLikeFunctionExpression("// leading\nasync (args) => args") {
		t.Fatal("line comments should be stripped before function detection")
	}
	if !looksLikeFunctionExpression("/* leading */\n(function(){})") {
		t.Fatal("block comments should be stripped before function detection")
	}
	if got := stripLeadingCommentsAndSpace("// no newline"); got != "" {
		t.Fatalf("single line comment strip = %q", got)
	}
	if got := stripLeadingCommentsAndSpace("/* unterminated"); got != "/* unterminated" {
		t.Fatalf("unterminated block strip = %q", got)
	}
}

func TestTrustUsageAndLintBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	ResetCacheForTests()

	localPath := writeSite(t, home, "sites/local.js", sampleJS)
	local, err := ParseSiteMeta(localPath, "local")
	if err != nil {
		t.Fatal(err)
	}
	status, err := AdapterTrustStatus(local)
	if err != nil || !status.Trusted {
		t.Fatalf("local trust status = %+v err=%v", status, err)
	}
	if err := TrustAdapter(local); err != nil {
		t.Fatalf("TrustAdapter local: %v", err)
	}

	communityPath := writeSite(t, home, "bb-sites/demo.js", sampleJS)
	community, err := ParseSiteMeta(communityPath, "community")
	if err != nil {
		t.Fatal(err)
	}
	if err := TrustAdapter(community); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(communityPath, []byte(strings.Replace(sampleJS, "Example adapter", "Changed adapter", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ParseSiteMeta(communityPath, "community")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckAdapterTrust(changed, false); err == nil || !strings.Contains(err.Error(), "changed hash") {
		t.Fatalf("changed trust error = %v", err)
	}
	if err := CheckAdapterTrust(changed, true); err != nil {
		t.Fatalf("force should bypass trust: %v", err)
	}

	if _, err := AdapterHash(&SiteMeta{FilePath: filepath.Join(home, "missing.js")}); err == nil {
		t.Fatal("AdapterHash should fail for missing file")
	}
	if _, err := AdapterTrustStatus(&SiteMeta{FilePath: filepath.Join(home, "missing.js")}); err == nil {
		t.Fatal("AdapterTrustStatus should fail for missing file")
	}

	if err := os.WriteFile(filepath.Join(home, "sites-trust.json"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if trust, err := loadTrust(); err != nil || len(trust) != 0 {
		t.Fatalf("empty trust = %+v err=%v", trust, err)
	}
	if err := os.WriteFile(filepath.Join(home, "sites-trust.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTrust(); err == nil {
		t.Fatal("loadTrust should report invalid JSON")
	}
	if err := os.WriteFile(filepath.Join(home, "sites-usage.json"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if usage, err := loadUsage(); err != nil || len(usage) != 0 {
		t.Fatalf("empty usage = %+v err=%v", usage, err)
	}
	if err := os.WriteFile(filepath.Join(home, "sites-usage.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUsage(); err == nil {
		t.Fatal("loadUsage should report invalid JSON")
	}

	RecordUsage("   ")
	issues := LintAdapter(&SiteMeta{
		Name:     "",
		Domain:   "",
		FilePath: filepath.Join(home, "missing-adapter.js"),
		Args: map[string]ArgDef{
			"q": {Required: true, Default: "cats"},
		},
	})
	if len(issues) < 3 {
		t.Fatalf("expected name/domain/default/build issues, got %+v", issues)
	}
}

func TestEvalRequestOptionsAndScaffoldValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	ResetCacheForTests()

	path := writeSite(t, home, "sites/timeout.js", strings.Replace(sampleJS, `"example": "bb example foo"`, `"timeoutMs": 1234, "example": "bb example foo"`, 1))
	meta, err := ParseSiteMeta(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	req, err := BuildEvalRequestWithOptions(meta, map[string]interface{}{"query": "cats"}, "tab-1", EvalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if req.EvalTimeoutMs == nil || *req.EvalTimeoutMs != 1234 || req.TabID != "tab-1" {
		t.Fatalf("timeout/tab request = %+v", req)
	}
	req, err = BuildEvalRequestWithOptions(meta, map[string]interface{}{"query": "cats"}, "", EvalOptions{TimeoutMs: 44, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if req.EvalTimeoutMs == nil || *req.EvalTimeoutMs != 44 || !req.Force || req.TabID != nil {
		t.Fatalf("option request = %+v", req)
	}

	for _, name := range []string{"", "../bad", `bad\slash`} {
		if _, err := NewAdapterScaffold(name); err == nil {
			t.Fatalf("NewAdapterScaffold(%q) should fail", name)
		}
	}
}

func TestCommunityUpdateGitBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	dir := filepath.Join(home, "bb-sites")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(fakeBin, "git")
	if err := os.WriteFile(git, []byte(`#!/bin/sh
case "$*" in
  *"status --porcelain"*) exit 0 ;;
  *"pull --ff-only"*) exit 0 ;;
  *"fetch --depth 1 origin feature"*) exit 0 ;;
  *"checkout --detach FETCH_HEAD"*) exit 0 ;;
  *"rev-parse HEAD"*) echo abc123; exit 0 ;;
esac
echo "unexpected $*" >&2
exit 2
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := UpdateCommunityRepo(""); err != nil {
		t.Fatalf("UpdateCommunityRepo pull: %v", err)
	}
	if err := UpdateCommunityRepo("feature"); err != nil {
		t.Fatalf("UpdateCommunityRepo ref: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(home, "community.lock")); err != nil || !strings.Contains(string(data), "abc123") {
		t.Fatalf("lock data=%q err=%v", data, err)
	}
}
