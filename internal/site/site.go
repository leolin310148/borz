// Package site handles loading, scanning, and executing site adapters.
package site

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

const communityRepoURL = "https://github.com/epiral/bb-sites.git"

// SiteMeta holds adapter metadata parsed from @meta blocks.
type SiteMeta struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	Args        map[string]ArgDef `json:"args"`
	ArgOrder    []string          `json:"argOrder,omitempty"` // declaration order of Args keys
	ReadOnly    bool              `json:"readOnly"`
	Example     string            `json:"example"`
	Entry       string            `json:"entry,omitempty"`
	TimeoutMs   int               `json:"timeoutMs,omitempty"`
	Output      json.RawMessage   `json:"output,omitempty"`
	FilePath    string            `json:"-"`
	Source      string            `json:"source,omitempty"`     // "local" or "community"
	SourceRepo  string            `json:"sourceRepo,omitempty"` // repo URL for community adapters
	SHA256      string            `json:"sha256,omitempty"`
	Trusted     bool              `json:"trusted,omitempty"`
	UsageCount  int               `json:"usageCount,omitempty"`
	LastUsed    string            `json:"lastUsed,omitempty"`
}

// ArgDef defines a site adapter argument.
type ArgDef struct {
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Default     string `json:"default"`
}

// EvalOptions controls adapter request construction.
type EvalOptions struct {
	Force     bool
	TimeoutMs int
}

// TrustStatus describes whether a community adapter hash is trusted.
type TrustStatus struct {
	Hash         string
	Trusted      bool
	PreviousHash string
}

// LintIssue is one adapter lint finding.
type LintIssue struct {
	Level   string
	Message string
}

type trustEntry struct {
	Hash       string `json:"hash"`
	Source     string `json:"source"`
	SourceRepo string `json:"sourceRepo,omitempty"`
	TrustedAt  string `json:"trustedAt"`
}

type usageEntry struct {
	Count    int    `json:"count"`
	LastUsed string `json:"lastUsed"`
}

var (
	metaRegexp = regexp.MustCompile(`/\*\s*@meta\s*([\s\S]*?)\*/`)

	cacheMu   sync.Mutex
	cacheData struct {
		localDir     string
		communityDir string
		localSig     string
		communitySig string
		sites        []*SiteMeta
	}
)

// ResetCacheForTests clears the in-process site scan cache.
func ResetCacheForTests() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheData = struct {
		localDir     string
		communityDir string
		localSig     string
		communitySig string
		sites        []*SiteMeta
	}{}
}

// ParseSiteMeta reads a JS adapter file and extracts its @meta block.
func ParseSiteMeta(filePath, source string) (*SiteMeta, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	content := string(data)

	matches := metaRegexp.FindStringSubmatch(content)
	if matches == nil || len(matches) < 2 {
		return nil, fmt.Errorf("no @meta block found in %s", filePath)
	}

	var meta SiteMeta
	if err := json.Unmarshal([]byte(matches[1]), &meta); err != nil {
		return nil, fmt.Errorf("invalid @meta JSON in %s: %w", filePath, err)
	}

	// Preserve declared order of args keys, since json.Unmarshal into a map
	// loses it (and Go map iteration is randomized).
	meta.ArgOrder = extractArgOrder([]byte(matches[1]))

	if meta.Name == "" {
		rel, _ := filepath.Rel(filepath.Dir(filepath.Dir(filePath)), filePath)
		meta.Name = strings.TrimSuffix(rel, ".js")
		meta.Name = strings.ReplaceAll(meta.Name, string(filepath.Separator), "/")
	}
	if meta.Args == nil {
		meta.Args = map[string]ArgDef{}
	}

	meta.FilePath = filePath
	meta.Source = source
	if source == "community" {
		meta.SourceRepo = communityRepoURL
	} else if source == "local" {
		meta.SourceRepo = "local"
	}
	meta.SHA256 = sha256Hex(data)
	return &meta, nil
}

// ScanSites recursively scans a directory for .js adapter files.
func ScanSites(dir, source string) []*SiteMeta {
	var sites []*SiteMeta
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("site: cannot scan %s: %v", dir, err)
		}
		return sites
	}

	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("site: cannot access %s: %v", path, err)
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		meta, err := ParseSiteMeta(path, source)
		if err != nil {
			log.Printf("site: skip %s: %v", path, err)
			return nil
		}
		sites = append(sites, meta)
		return nil
	}); err != nil {
		log.Printf("site: scan %s failed: %v", dir, err)
	}

	return sites
}

// AllSites returns all adapters (local takes priority over community).
func AllSites() []*SiteMeta {
	localDir := config.SitesDir()
	communityDir := config.CommunitySitesDir()
	localSig := scanSignature(localDir)
	communitySig := scanSignature(communityDir)

	cacheMu.Lock()
	if cacheData.localDir == localDir &&
		cacheData.communityDir == communityDir &&
		cacheData.localSig == localSig &&
		cacheData.communitySig == communitySig &&
		cacheData.sites != nil {
		out := append([]*SiteMeta(nil), cacheData.sites...)
		cacheMu.Unlock()
		return out
	}
	cacheMu.Unlock()

	localSites := ScanSites(localDir, "local")
	communitySites := ScanSites(communityDir, "community")

	seen := make(map[string]bool)
	var all []*SiteMeta
	for _, s := range localSites {
		seen[s.Name] = true
		all = append(all, s)
	}
	for _, s := range communitySites {
		if !seen[s.Name] {
			all = append(all, s)
		}
	}
	annotateSites(all)
	sortSites(all)

	cacheMu.Lock()
	cacheData.localDir = localDir
	cacheData.communityDir = communityDir
	cacheData.localSig = localSig
	cacheData.communitySig = communitySig
	cacheData.sites = append([]*SiteMeta(nil), all...)
	cacheMu.Unlock()

	return all
}

// FindSite finds a site adapter by name.
func FindSite(name string) *SiteMeta {
	for _, s := range AllSites() {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// SearchSites searches adapters by name, description, or domain.
func SearchSites(query string) []*SiteMeta {
	query = strings.ToLower(strings.TrimSpace(query))
	sites := AllSites()
	if query == "" {
		return sites
	}

	type scored struct {
		site  *SiteMeta
		score int
	}
	var matches []scored
	for _, s := range sites {
		score := searchScore(s, query)
		if score > 0 {
			matches = append(matches, scored{site: s, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].site.Name < matches[j].site.Name
	})
	out := make([]*SiteMeta, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.site)
	}
	return out
}

// BuildAdapterScript reads an adapter file and builds the eval script.
func BuildAdapterScript(meta *SiteMeta, args map[string]interface{}) (string, error) {
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot read adapter: %w", err)
	}

	content := string(data)
	jsBody := strings.TrimSpace(metaRegexp.ReplaceAllString(content, ""))

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("cannot encode adapter args: %w", err)
	}
	entryJSON, _ := json.Marshal(meta.Entry)

	switch {
	case meta.Entry != "":
		return fmt.Sprintf(`(async () => {
const args = %s;
%s
const __borzEntry = eval(%s);
if (typeof __borzEntry !== "function") throw new Error("adapter entry not found or not a function: " + %s);
return await __borzEntry(args);
})()`, string(argsJSON), jsBody, string(entryJSON), string(entryJSON)), nil
	case looksLikeFunctionExpression(jsBody):
		return fmt.Sprintf(`(async () => {
const __borzAdapter = (%s);
if (typeof __borzAdapter !== "function") throw new Error("site adapter must export a function");
return await __borzAdapter(%s);
})()`, jsBody, string(argsJSON)), nil
	default:
		return fmt.Sprintf(`(async () => {
const args = %s;
%s
})()`, string(argsJSON), jsBody), nil
	}
}

// extractArgOrder walks the raw @meta JSON with a decoder so we can recover the
// declaration order of the "args" object's keys (map unmarshaling drops it).
func extractArgOrder(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := tok.(string)
		if key != "args" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil
			}
			continue
		}
		if _, err := dec.Token(); err != nil {
			return nil
		}
		var order []string
		for dec.More() {
			nameTok, err := dec.Token()
			if err != nil {
				return nil
			}
			name, ok := nameTok.(string)
			if !ok {
				return nil
			}
			order = append(order, name)
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil
			}
		}
		return order
	}
	return nil
}

// ParseAdapterArgs parses CLI positional + flag args into a validated adapter argument map.
func ParseAdapterArgs(meta *SiteMeta, cliArgs []string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	argNames := orderedArgNames(meta)

	posIdx := 0
	for i := 0; i < len(cliArgs); i++ {
		arg := cliArgs[i]
		if strings.HasPrefix(arg, "--") {
			key := strings.TrimPrefix(arg, "--")
			if key == "" {
				return nil, fmt.Errorf("invalid empty adapter arg flag")
			}
			if i+1 >= len(cliArgs) || strings.HasPrefix(cliArgs[i+1], "--") {
				return nil, fmt.Errorf("missing value for --%s", key)
			}
			result[key] = cliArgs[i+1]
			i++
			continue
		}
		if posIdx < len(argNames) {
			result[argNames[posIdx]] = arg
		}
		posIdx++
	}

	return NormalizeAdapterArgs(meta, result)
}

// NormalizeAdapterArgs applies defaults and validates required adapter args.
func NormalizeAdapterArgs(meta *SiteMeta, args map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(args)+len(meta.Args))
	for k, v := range args {
		result[k] = v
	}

	var missing []string
	for _, name := range orderedArgNames(meta) {
		def := meta.Args[name]
		v, ok := result[name]
		if (!ok || v == nil) && def.Default != "" {
			result[name] = def.Default
			ok = true
			v = def.Default
		}
		if def.Required && (!ok || v == nil) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required adapter arg(s): %s", strings.Join(missing, ", "))
	}
	return result, nil
}

// BuildEvalRequest creates a Request for running an adapter.
func BuildEvalRequest(meta *SiteMeta, args map[string]interface{}, tabID string) (*protocol.Request, error) {
	return BuildEvalRequestWithOptions(meta, args, tabID, EvalOptions{})
}

// BuildEvalRequestWithOptions creates a Request for running an adapter.
func BuildEvalRequestWithOptions(meta *SiteMeta, args map[string]interface{}, tabID string, opts EvalOptions) (*protocol.Request, error) {
	normalized, err := NormalizeAdapterArgs(meta, args)
	if err != nil {
		return nil, err
	}
	script, err := BuildAdapterScript(meta, normalized)
	if err != nil {
		return nil, err
	}
	if err := CheckAdapterTrust(meta, opts.Force); err != nil {
		return nil, err
	}

	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = meta.TimeoutMs
	}
	req := &protocol.Request{
		ID:         generateID(),
		Action:     protocol.ActionEval,
		Script:     script,
		SiteDomain: meta.Domain,
		Force:      opts.Force,
	}
	if timeout > 0 {
		req.EvalTimeoutMs = &timeout
	}
	if tabID != "" {
		req.TabID = tabID
	}
	return req, nil
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// AdapterHash returns the current SHA256 hash of an adapter file.
func AdapterHash(meta *SiteMeta) (string, error) {
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

// AdapterTrustStatus returns the current community trust state for an adapter.
func AdapterTrustStatus(meta *SiteMeta) (TrustStatus, error) {
	hash, err := AdapterHash(meta)
	if err != nil {
		return TrustStatus{}, err
	}
	status := TrustStatus{Hash: hash, Trusted: meta.Source != "community"}
	if meta.Source != "community" {
		return status, nil
	}
	trust, err := loadTrust()
	if err != nil {
		return status, err
	}
	if entry, ok := trust[trustKey(meta)]; ok {
		status.PreviousHash = entry.Hash
		status.Trusted = entry.Hash == hash
	}
	return status, nil
}

// CheckAdapterTrust rejects untrusted community adapters unless force is set.
func CheckAdapterTrust(meta *SiteMeta, force bool) error {
	if meta.Source != "community" || force {
		return nil
	}
	status, err := AdapterTrustStatus(meta)
	if err != nil {
		return fmt.Errorf("check adapter trust: %w", err)
	}
	if status.Trusted {
		return nil
	}
	if status.PreviousHash != "" {
		return fmt.Errorf("community adapter %q changed hash (%s -> %s); inspect it and run `borz site trust %s` or pass --force to run once", meta.Name, status.PreviousHash, status.Hash, meta.Name)
	}
	return fmt.Errorf("community adapter %q is not trusted yet (sha256 %s); inspect it and run `borz site trust %s` or pass --force to run once", meta.Name, status.Hash, meta.Name)
}

// TrustAdapter records the current adapter SHA256 as trusted.
func TrustAdapter(meta *SiteMeta) error {
	if meta.Source != "community" {
		return nil
	}
	hash, err := AdapterHash(meta)
	if err != nil {
		return err
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return err
	}
	trust, err := loadTrust()
	if err != nil {
		return err
	}
	trust[trustKey(meta)] = trustEntry{
		Hash:       hash,
		Source:     meta.Source,
		SourceRepo: meta.SourceRepo,
		TrustedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSONFile(config.SiteTrustPath(), trust, 0o600); err != nil {
		return err
	}
	ResetCacheForTests()
	return nil
}

// RecordUsage increments usage metadata for a site adapter.
func RecordUsage(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		log.Printf("site: cannot create home for usage: %v", err)
		return
	}
	usage, err := loadUsage()
	if err != nil {
		log.Printf("site: cannot read usage: %v", err)
		return
	}
	entry := usage[name]
	entry.Count++
	entry.LastUsed = time.Now().UTC().Format(time.RFC3339)
	usage[name] = entry
	if err := writeJSONFile(config.SitesUsagePath(), usage, 0o600); err != nil {
		log.Printf("site: cannot write usage: %v", err)
		return
	}
	ResetCacheForTests()
}

// NewAdapterScaffold creates a local adapter template and returns the file path.
func NewAdapterScaffold(name string) (string, error) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid adapter name: %q", name)
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return "", err
	}
	rel := name
	if !strings.HasSuffix(rel, ".js") {
		rel += ".js"
	}
	path := filepath.Join(config.SitesDir(), filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("adapter already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	body := fmt.Sprintf(`/* @meta
{
  "name": %q,
  "description": "Describe what this adapter does",
  "domain": "example.com",
  "args": {
    "query": {"required": true, "description": "Search query"}
  },
  "readOnly": true,
  "example": "borz %s hello"
}
*/
return { ok: true, data: { query: args.query } };
`, strings.TrimSuffix(name, ".js"), strings.TrimSuffix(name, ".js"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	ResetCacheForTests()
	return path, nil
}

// LintAdapter validates adapter metadata and buildability.
func LintAdapter(meta *SiteMeta) []LintIssue {
	var issues []LintIssue
	if strings.TrimSpace(meta.Name) == "" {
		issues = append(issues, LintIssue{Level: "error", Message: "meta.name is required"})
	}
	if strings.TrimSpace(meta.Domain) == "" {
		issues = append(issues, LintIssue{Level: "error", Message: "meta.domain is required for origin guard"})
	}
	for _, name := range orderedArgNames(meta) {
		def := meta.Args[name]
		if def.Required && def.Default != "" {
			issues = append(issues, LintIssue{Level: "warning", Message: fmt.Sprintf("arg %q is required but also has a default", name)})
		}
	}
	args, err := NormalizeAdapterArgs(meta, lintArgs(meta))
	if err != nil {
		issues = append(issues, LintIssue{Level: "error", Message: err.Error()})
	} else if _, err := BuildAdapterScript(meta, args); err != nil {
		issues = append(issues, LintIssue{Level: "error", Message: err.Error()})
	}
	return issues
}

// UpdateCommunityRepo pulls or clones the community adapter repo.
func UpdateCommunityRepo(ref string) error {
	if _, err := config.EnsureHomeDir(); err != nil {
		return err
	}
	dir := config.CommunitySitesDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		fmt.Printf("Updating community adapters in %s...\n", dir)
		if err := ensureCleanGitTree(dir); err != nil {
			return err
		}
		if ref != "" {
			if err := gitFetchCheckout(dir, ref); err != nil {
				return err
			}
		} else if err := gitPull(dir); err != nil {
			return err
		}
		return writeCommunityLock(dir, ref)
	}
	fmt.Printf("Cloning community adapters to %s...\n", dir)
	if err := gitCloneRef(communityRepoURL, dir, ref); err != nil {
		return err
	}
	return writeCommunityLock(dir, ref)
}

func gitPull(dir string) error {
	cmd := newCommand("git", "-C", dir, "pull", "--ff-only")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git pull --ff-only failed in %s: %s: %w", dir, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func gitClone(url, dir string) error {
	return gitCloneRef(url, dir, "")
}

func gitCloneRef(url, dir, ref string) error {
	args := []string{"clone", "--depth", "1", url, dir}
	cmd := newCommand("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if ref != "" {
		return gitFetchCheckout(dir, ref)
	}
	return nil
}

func gitFetchCheckout(dir, ref string) error {
	if out, err := newCommand("git", "-C", dir, "fetch", "--depth", "1", "origin", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %q failed: %s: %w", ref, strings.TrimSpace(string(out)), err)
	}
	if out, err := newCommand("git", "-C", dir, "checkout", "--detach", "FETCH_HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %q failed: %s: %w", ref, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// newCommand is a helper that wraps exec.Command.
func newCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func orderedArgNames(meta *SiteMeta) []string {
	if meta == nil {
		return nil
	}
	if len(meta.ArgOrder) > 0 {
		return append([]string(nil), meta.ArgOrder...)
	}
	names := make([]string, 0, len(meta.Args))
	for name := range meta.Args {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func scanSignature(dir string) string {
	var parts []string
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("site: cannot stat %s: %v", dir, err)
		}
		return ""
	}
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("site: cannot stat %s: %v", path, err)
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", path, info.ModTime().UnixNano(), info.Size()))
		return nil
	}); err != nil {
		log.Printf("site: signature scan %s failed: %v", dir, err)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func annotateSites(sites []*SiteMeta) {
	usage, _ := loadUsage()
	trust, _ := loadTrust()
	for _, s := range sites {
		if u, ok := usage[s.Name]; ok {
			s.UsageCount = u.Count
			s.LastUsed = u.LastUsed
		}
		if s.Source != "community" {
			s.Trusted = true
			continue
		}
		if entry, ok := trust[trustKey(s)]; ok && entry.Hash == s.SHA256 {
			s.Trusted = true
		}
	}
}

func sortSites(sites []*SiteMeta) {
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].UsageCount != sites[j].UsageCount {
			return sites[i].UsageCount > sites[j].UsageCount
		}
		if sites[i].LastUsed != sites[j].LastUsed {
			return sites[i].LastUsed > sites[j].LastUsed
		}
		return sites[i].Name < sites[j].Name
	})
}

func searchScore(s *SiteMeta, query string) int {
	name := strings.ToLower(s.Name)
	desc := strings.ToLower(s.Description)
	domain := strings.ToLower(s.Domain)
	switch {
	case name == query:
		return 1000
	case strings.HasPrefix(name, query):
		return 900
	case strings.Contains(name, query):
		return 800
	case domain == query:
		return 700
	case strings.Contains(domain, query):
		return 650
	case strings.Contains(desc, query):
		return 500
	case fuzzyContains(name, query):
		return 300
	case fuzzyContains(domain, query):
		return 250
	case fuzzyContains(desc, query):
		return 150
	default:
		return 0
	}
}

func fuzzyContains(s, query string) bool {
	if query == "" {
		return true
	}
	pos := 0
	for _, r := range s {
		if rune(query[pos]) == r {
			pos++
			if pos == len(query) {
				return true
			}
		}
	}
	return false
}

func looksLikeFunctionExpression(js string) bool {
	js = stripLeadingCommentsAndSpace(js)
	return strings.HasPrefix(js, "function") ||
		strings.HasPrefix(js, "async function") ||
		strings.HasPrefix(js, "async (") ||
		strings.HasPrefix(js, "(")
}

func stripLeadingCommentsAndSpace(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if strings.HasPrefix(s, "//") {
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
				continue
			}
			return s
		}
		return s
	}
}

func loadTrust() (map[string]trustEntry, error) {
	out := map[string]trustEntry{}
	data, err := os.ReadFile(config.SiteTrustPath())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	return out, json.Unmarshal(data, &out)
}

func loadUsage() (map[string]usageEntry, error) {
	out := map[string]usageEntry{}
	data, err := os.ReadFile(config.SitesUsagePath())
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	return out, json.Unmarshal(data, &out)
}

func writeJSONFile(path string, value interface{}, perm os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, perm)
}

func trustKey(meta *SiteMeta) string {
	return meta.Source + ":" + meta.Name
}

func ensureCleanGitTree(dir string) error {
	out, err := newCommand("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git status failed in %s: %s: %w", dir, strings.TrimSpace(string(out)), err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("community adapter repo has local changes in %s; commit, stash, or remove them before updating", dir)
	}
	return nil
}

func writeCommunityLock(dir, ref string) error {
	commitBytes, err := newCommand("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read community repo HEAD: %s: %w", strings.TrimSpace(string(commitBytes)), err)
	}
	lock := map[string]string{
		"repo":      communityRepoURL,
		"ref":       ref,
		"commit":    strings.TrimSpace(string(commitBytes)),
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}
	return writeJSONFile(config.CommunityLockPath(), lock, 0o644)
}

func lintArgs(meta *SiteMeta) map[string]interface{} {
	args := make(map[string]interface{}, len(meta.Args))
	for name, def := range meta.Args {
		if def.Default != "" {
			args[name] = def.Default
		} else if def.Required {
			args[name] = "example"
		}
	}
	return args
}
