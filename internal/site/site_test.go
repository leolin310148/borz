package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func writeSite(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleJS = `/* @meta
{
  "name": "example",
  "description": "Example adapter",
  "domain": "example.com",
  "args": {
    "query": {"required": true, "description": "search term"},
    "limit": {"required": false, "default": "10"}
  },
  "readOnly": true,
  "example": "bb example foo"
}
*/
(function(args) { return args; })
`

func TestParseSiteMeta_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/example.js", sampleJS)
	meta, err := ParseSiteMeta(path, "local")
	if err != nil {
		t.Fatalf("ParseSiteMeta: %v", err)
	}
	if meta.Name != "example" || meta.Domain != "example.com" || !meta.ReadOnly {
		t.Errorf("meta fields: %+v", meta)
	}
	if meta.Source != "local" {
		t.Errorf("Source = %q, want local", meta.Source)
	}
	if meta.FilePath != path {
		t.Errorf("FilePath = %q, want %q", meta.FilePath, path)
	}
	if q, ok := meta.Args["query"]; !ok || !q.Required {
		t.Errorf("Args.query: %+v", meta.Args)
	}
	if got, want := meta.ArgOrder, []string{"query", "limit"}; !stringsEqual(got, want) {
		t.Errorf("ArgOrder = %v, want %v", got, want)
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Regression: with ≥5 args, positional assignment used to be randomized by
// Go map iteration. ArgOrder now pins it to declaration order.
func TestParseSiteMeta_ArgOrderFiveArgs(t *testing.T) {
	dir := t.TempDir()
	body := `/* @meta
{
  "name": "reddit",
  "args": {
    "query":    {"required": true},
    "sort":     {"required": false},
    "time":     {"required": false},
    "count":    {"required": false},
    "subreddit":{"required": false}
  }
}
*/
(function(){})`
	path := writeSite(t, dir, "sites/reddit.js", body)
	want := []string{"query", "sort", "time", "count", "subreddit"}
	// Run multiple times to shake out map-iteration nondeterminism.
	for i := 0; i < 50; i++ {
		meta, err := ParseSiteMeta(path, "local")
		if err != nil {
			t.Fatalf("ParseSiteMeta: %v", err)
		}
		if !stringsEqual(meta.ArgOrder, want) {
			t.Fatalf("iter %d: ArgOrder = %v, want %v", i, meta.ArgOrder, want)
		}
		got := ParseAdapterArgs(meta, []string{"hello", "--sort", "top"})
		if got["query"] != "hello" {
			t.Fatalf("iter %d: positional assigned to %+v, want query=hello", i, got)
		}
		if got["sort"] != "top" {
			t.Fatalf("iter %d: sort flag = %+v", i, got)
		}
	}
}

func TestParseSiteMeta_DefaultName(t *testing.T) {
	dir := t.TempDir()
	// name is empty in the meta → derived from path: <parentDir>/<file without .js>
	body := `/* @meta
{"description":"d"}
*/
(function(){})`
	path := writeSite(t, dir, "group/tool.js", body)
	meta, err := ParseSiteMeta(path, "community")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// rel(dir(dir(path)), path) = group/tool.js → strip .js → "group/tool"
	if meta.Name != "group/tool" {
		t.Errorf("default Name = %q, want group/tool", meta.Name)
	}
}

func TestParseSiteMeta_MissingMeta(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/no-meta.js", "(function(){})")
	_, err := ParseSiteMeta(path, "local")
	if err == nil || !strings.Contains(err.Error(), "no @meta") {
		t.Fatalf("expected no-meta error, got %v", err)
	}
}

func TestParseSiteMeta_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/bad.js", `/* @meta { bad json } */`)
	_, err := ParseSiteMeta(path, "local")
	if err == nil || !strings.Contains(err.Error(), "invalid @meta JSON") {
		t.Fatalf("expected invalid-json error, got %v", err)
	}
}

func TestParseSiteMeta_FileMissing(t *testing.T) {
	_, err := ParseSiteMeta(filepath.Join(t.TempDir(), "nope.js"), "local")
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}

func TestScanSites(t *testing.T) {
	dir := t.TempDir()
	writeSite(t, dir, "sites/a.js", sampleJS)
	writeSite(t, dir, "sites/sub/b.js", sampleJS)
	writeSite(t, dir, "sites/bad.js", "no meta here")
	writeSite(t, dir, "sites/ignore.txt", "not js")

	got := ScanSites(filepath.Join(dir, "sites"), "local")
	if len(got) != 2 {
		t.Fatalf("ScanSites len = %d, want 2 (bad.js skipped)", len(got))
	}
	for _, s := range got {
		if s.Source != "local" {
			t.Errorf("source = %q, want local", s.Source)
		}
	}
}

func TestScanSites_NonexistentDir(t *testing.T) {
	got := ScanSites(filepath.Join(t.TempDir(), "missing"), "local")
	if len(got) != 0 {
		t.Errorf("ScanSites on missing dir = %v, want empty", got)
	}
}

func TestAllSites_LocalOverridesCommunity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)

	// Local and community both have "shared"; local should win.
	writeSite(t, home, "sites/shared.js", strings.Replace(sampleJS, `"name": "example"`, `"name": "shared"`, 1))
	writeSite(t, home, "bb-sites/shared.js", strings.Replace(sampleJS, `"name": "example"`, `"name": "shared"`, 1))
	writeSite(t, home, "bb-sites/only-community.js", strings.Replace(sampleJS, `"name": "example"`, `"name": "only-community"`, 1))

	all := AllSites()
	byName := map[string]*SiteMeta{}
	for _, s := range all {
		if prev, dup := byName[s.Name]; dup {
			t.Fatalf("duplicate %q: %+v / %+v", s.Name, prev, s)
		}
		byName[s.Name] = s
	}
	if byName["shared"] == nil || byName["shared"].Source != "local" {
		t.Errorf("shared source = %+v, want local", byName["shared"])
	}
	if byName["only-community"] == nil || byName["only-community"].Source != "community" {
		t.Errorf("only-community = %+v", byName["only-community"])
	}
}

func TestFindSite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeSite(t, home, "sites/example.js", sampleJS)

	if s := FindSite("example"); s == nil || s.Name != "example" {
		t.Errorf("FindSite(example) = %+v", s)
	}
	if s := FindSite("nope"); s != nil {
		t.Errorf("FindSite(nope) = %+v, want nil", s)
	}
}

func TestSearchSites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	writeSite(t, home, "sites/example.js", sampleJS)
	otherJS := strings.Replace(sampleJS, `"name": "example"`, `"name": "other"`, 1)
	otherJS = strings.Replace(otherJS, `"domain": "example.com"`, `"domain": "other.net"`, 1)
	otherJS = strings.Replace(otherJS, `"description": "Example adapter"`, `"description": "Other adapter"`, 1)
	writeSite(t, home, "sites/other.js", otherJS)

	if got := SearchSites("EXAMPLE"); len(got) != 1 || got[0].Name != "example" {
		t.Errorf("SearchSites(EXAMPLE) = %+v", got)
	}
	if got := SearchSites(""); len(got) != 2 {
		t.Errorf("SearchSites empty = %d results, want 2", len(got))
	}
	if got := SearchSites("no-match-xyzzy"); len(got) != 0 {
		t.Errorf("SearchSites(no-match) = %+v, want none", got)
	}
}

func TestBuildAdapterScript(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/a.js", sampleJS)
	meta, err := ParseSiteMeta(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	script, err := BuildAdapterScript(meta, map[string]interface{}{"query": "cats", "limit": "5"})
	if err != nil {
		t.Fatalf("BuildAdapterScript: %v", err)
	}
	if strings.Contains(script, "@meta") {
		t.Errorf("script should strip @meta block: %s", script)
	}
	if !strings.Contains(script, `"query":"cats"`) || !strings.Contains(script, `"limit":"5"`) {
		t.Errorf("args not embedded: %s", script)
	}
	// Wrapped as invocation
	if !strings.HasPrefix(script, "(") || !strings.Contains(script, ")(") {
		t.Errorf("script not wrapped as IIFE: %s", script)
	}
}

func TestBuildAdapterScript_MissingFile(t *testing.T) {
	meta := &SiteMeta{FilePath: filepath.Join(t.TempDir(), "ghost.js")}
	if _, err := BuildAdapterScript(meta, nil); err == nil {
		t.Fatal("expected read error")
	}
}

func TestParseAdapterArgs_Positional(t *testing.T) {
	meta := &SiteMeta{
		Args:     map[string]ArgDef{"q": {}, "limit": {}},
		ArgOrder: []string{"q", "limit"},
	}
	got := ParseAdapterArgs(meta, []string{"hello", "5"})
	if got["q"] != "hello" || got["limit"] != "5" {
		t.Errorf("positional assignment = %+v, want q=hello limit=5", got)
	}
}

func TestParseAdapterArgs_Flags(t *testing.T) {
	meta := &SiteMeta{Args: map[string]ArgDef{"q": {}}}
	got := ParseAdapterArgs(meta, []string{"--q", "cats", "--limit", "10"})
	if got["q"] != "cats" || got["limit"] != "10" {
		t.Errorf("flag parsing = %+v", got)
	}
}

func TestParseAdapterArgs_FlagMissingValue(t *testing.T) {
	meta := &SiteMeta{Args: map[string]ArgDef{}}
	got := ParseAdapterArgs(meta, []string{"--dangling"})
	if len(got) != 0 {
		t.Errorf("dangling flag should not add key: %+v", got)
	}
}

func TestParseAdapterArgs_ExcessPositionalIgnored(t *testing.T) {
	meta := &SiteMeta{Args: map[string]ArgDef{"only": {}}}
	got := ParseAdapterArgs(meta, []string{"a", "b", "c"})
	if len(got) != 1 {
		t.Errorf("expected 1 arg stored, got %+v", got)
	}
}

func TestBuildEvalRequest(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/a.js", sampleJS)
	meta, _ := ParseSiteMeta(path, "local")

	req, err := BuildEvalRequest(meta, map[string]interface{}{"query": "q"}, "tab-9")
	if err != nil {
		t.Fatal(err)
	}
	if req.Action != protocol.ActionEval {
		t.Errorf("action = %v, want eval", req.Action)
	}
	if req.Script == "" {
		t.Errorf("script empty")
	}
	if req.TabID != "tab-9" {
		t.Errorf("TabID = %v, want tab-9", req.TabID)
	}
	if req.ID == "" {
		t.Errorf("ID empty")
	}
}

func TestBuildEvalRequest_NoTabID(t *testing.T) {
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/a.js", sampleJS)
	meta, _ := ParseSiteMeta(path, "local")

	req, err := BuildEvalRequest(meta, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if req.TabID != nil {
		t.Errorf("TabID = %v, want nil", req.TabID)
	}
}

func TestBuildEvalRequest_MissingFile(t *testing.T) {
	meta := &SiteMeta{FilePath: filepath.Join(t.TempDir(), "ghost.js")}
	if _, err := BuildEvalRequest(meta, nil, ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunAdapter_ReturnsExpectedError(t *testing.T) {
	// RunAdapter is a stub that always errors; with a valid adapter it returns the "use client.SendCommand" message.
	dir := t.TempDir()
	path := writeSite(t, dir, "sites/a.js", sampleJS)
	meta, _ := ParseSiteMeta(path, "local")
	_, err := RunAdapter(meta, nil, "")
	if err == nil || !strings.Contains(err.Error(), "client.SendCommand") {
		t.Fatalf("expected stub error, got %v", err)
	}
}

func TestGenerateID_Format(t *testing.T) {
	id := generateID()
	if len(id) == 0 {
		t.Fatal("empty id")
	}
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("id has non-hex char %q in %q", r, id)
			break
		}
	}
}
