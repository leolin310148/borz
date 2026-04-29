// Package site handles loading, scanning, and executing site adapters.
package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

// SiteMeta holds adapter metadata parsed from @meta blocks.
type SiteMeta struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Domain      string            `json:"domain"`
	Args        map[string]ArgDef `json:"args"`
	ArgOrder    []string          `json:"-"` // declaration order of Args keys
	ReadOnly    bool              `json:"readOnly"`
	Example     string            `json:"example"`
	FilePath    string            `json:"-"`
	Source      string            `json:"-"` // "local" or "community"
}

// ArgDef defines a site adapter argument.
type ArgDef struct {
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Default     string `json:"default"`
}

var metaRegexp = regexp.MustCompile(`/\*\s*@meta\s*([\s\S]*?)\*/`)

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

	// Default name from path
	if meta.Name == "" {
		rel, _ := filepath.Rel(filepath.Dir(filepath.Dir(filePath)), filePath)
		meta.Name = strings.TrimSuffix(rel, ".js")
		meta.Name = strings.ReplaceAll(meta.Name, string(filepath.Separator), "/")
	}

	meta.FilePath = filePath
	meta.Source = source
	return &meta, nil
}

// ScanSites recursively scans a directory for .js adapter files.
func ScanSites(dir, source string) []*SiteMeta {
	var sites []*SiteMeta

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".js") {
			return nil
		}
		meta, err := ParseSiteMeta(path, source)
		if err != nil {
			return nil
		}
		sites = append(sites, meta)
		return nil
	})

	return sites
}

// AllSites returns all adapters (local takes priority over community).
func AllSites() []*SiteMeta {
	localSites := ScanSites(config.SitesDir(), "local")
	communitySites := ScanSites(config.CommunitySitesDir(), "community")

	// Dedup: local overrides community
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
	query = strings.ToLower(query)
	var results []*SiteMeta
	for _, s := range AllSites() {
		haystack := strings.ToLower(s.Name + " " + s.Description + " " + s.Domain)
		if strings.Contains(haystack, query) {
			results = append(results, s)
		}
	}
	return results
}

// BuildAdapterScript reads an adapter file and builds the eval script.
func BuildAdapterScript(meta *SiteMeta, args map[string]interface{}) (string, error) {
	data, err := os.ReadFile(meta.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot read adapter: %w", err)
	}

	content := string(data)
	// Strip @meta block
	jsBody := metaRegexp.ReplaceAllString(content, "")
	jsBody = strings.TrimSpace(jsBody)

	argsJSON, _ := json.Marshal(args)
	script := fmt.Sprintf("(%s)(%s)", jsBody, string(argsJSON))
	return script, nil
}

// extractArgOrder walks the raw @meta JSON with a decoder so we can recover the
// declaration order of the "args" object's keys (map unmarshaling drops it).
func extractArgOrder(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Top-level '{'
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
			// Skip the value
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil
			}
			continue
		}
		// Enter the args object
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

// ParseAdapterArgs parses CLI positional + flag args into a map for the adapter.
func ParseAdapterArgs(meta *SiteMeta, cliArgs []string) map[string]interface{} {
	result := make(map[string]interface{})

	// Use the declared arg order (from the @meta JSON). Fall back to map keys
	// for metas constructed in-memory without order; this is only deterministic
	// for tests that don't rely on positional assignment.
	argNames := meta.ArgOrder
	if argNames == nil {
		for name := range meta.Args {
			argNames = append(argNames, name)
		}
	}

	// Fill positional args
	posIdx := 0
	for i := 0; i < len(cliArgs); i++ {
		arg := cliArgs[i]
		if strings.HasPrefix(arg, "--") {
			// Flag arg: --key value
			key := strings.TrimPrefix(arg, "--")
			if i+1 < len(cliArgs) {
				result[key] = cliArgs[i+1]
				i++
			}
		} else {
			// Positional
			if posIdx < len(argNames) {
				result[argNames[posIdx]] = arg
			}
			posIdx++
		}
	}

	return result
}

// RunAdapter builds and sends the adapter script via the daemon eval command.
func RunAdapter(meta *SiteMeta, args map[string]interface{}, tabID string) (*protocol.Response, error) {
	script, err := BuildAdapterScript(meta, args)
	if err != nil {
		return nil, err
	}

	req := &protocol.Request{
		ID:     generateID(),
		Action: protocol.ActionEval,
		Script: script,
	}
	if tabID != "" {
		req.TabID = tabID
	}

	// Import cycle avoidance: we call the client from the CLI layer, not here.
	// This returns the request to be sent.
	return nil, fmt.Errorf("use client.SendCommand with the built request instead")
}

// BuildEvalRequest creates a Request for running an adapter.
func BuildEvalRequest(meta *SiteMeta, args map[string]interface{}, tabID string) (*protocol.Request, error) {
	script, err := BuildAdapterScript(meta, args)
	if err != nil {
		return nil, err
	}

	req := &protocol.Request{
		ID:     generateID(),
		Action: protocol.ActionEval,
		Script: script,
	}
	if tabID != "" {
		req.TabID = tabID
	}
	return req, nil
}

func generateID() string {
	// Simple unique ID
	b := make([]byte, 16)
	for i := range b {
		b[i] = "0123456789abcdef"[int(b[i])%16]
	}
	return fmt.Sprintf("%x", b)
}

// UpdateCommunityRepo pulls or clones the community adapter repo.
func UpdateCommunityRepo() error {
	if _, err := config.EnsureHomeDir(); err != nil {
		return err
	}
	dir := config.CommunitySitesDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		// Git pull
		fmt.Printf("Updating community adapters in %s...\n", dir)
		// Use exec to run git pull
		return gitPull(dir)
	}
	// Clone
	fmt.Printf("Cloning community adapters to %s...\n", dir)
	return gitClone("https://github.com/epiral/bb-sites.git", dir)
}

func gitPull(dir string) error {
	cmd := newCommand("git", "-C", dir, "pull", "--ff-only")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitClone(url, dir string) error {
	cmd := newCommand("git", "clone", "--depth", "1", url, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// newCommand is a helper that wraps exec.Command.
func newCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
