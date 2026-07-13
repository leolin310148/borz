package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/site"
)

type siteScope string

const (
	siteScopeClient siteScope = "client"
	siteScopeServer siteScope = "server"
)

var (
	siteGetJSON  = client.GetJSON
	sitePostJSON = client.PostJSON
)

func resolveSiteScope(args []string) (siteScope, error) {
	raw, set := getArgValueOK(args, "--scope")
	if !set {
		return siteScopeClient, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "client":
		return siteScopeClient, nil
	case "server":
		return siteScopeServer, nil
	case "":
		return "", fmt.Errorf("--scope requires client or server")
	default:
		return "", fmt.Errorf("invalid site scope %q (expected client or server)", raw)
	}
}

func requireClientSiteScope(scope siteScope, command string) error {
	if scope == siteScopeServer {
		return fmt.Errorf("site %s only supports --scope client; manage server adapters on the daemon host", command)
	}
	return nil
}

type serverSiteListResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    struct {
		Sites []*site.SiteMeta `json:"sites"`
	} `json:"data"`
}

type serverSiteInfoResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    struct {
		Site *site.SiteMeta `json:"site"`
	} `json:"data"`
}

func loadServerSites() ([]*site.SiteMeta, error) {
	raw, err := siteGetJSON("/v1/sites", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var payload serverSiteListResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode server site list: %w", err)
	}
	if !payload.Success {
		if payload.Error == "" {
			payload.Error = "unknown error"
		}
		return nil, fmt.Errorf("server site list failed: %s", payload.Error)
	}
	if payload.Data.Sites == nil {
		return []*site.SiteMeta{}, nil
	}
	return payload.Data.Sites, nil
}

func loadServerSite(name string) (*site.SiteMeta, error) {
	raw, err := sitePostJSON("/v1/sites/info", map[string]interface{}{"name": name}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var payload serverSiteInfoResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode server site info: %w", err)
	}
	if !payload.Success || payload.Data.Site == nil {
		if payload.Error == "" {
			payload.Error = "adapter not found: " + name
		}
		return nil, fmt.Errorf("server site info failed: %s", payload.Error)
	}
	return payload.Data.Site, nil
}

func runServerSite(name string, args map[string]interface{}, tab string, force bool, timeoutMs int) (*protocol.Response, error) {
	body := map[string]interface{}{
		"name":  name,
		"args":  args,
		"force": force,
	}
	if tab != "" {
		body["tab"] = tab
	}
	if timeoutMs > 0 {
		body["timeoutMs"] = timeoutMs
	}
	raw, err := sitePostJSON("/v1/sites/run", body, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode server site result: %w", err)
	}
	return &resp, nil
}

func printSiteList(sites []*site.SiteMeta, jsonOutput bool) {
	if jsonOutput {
		printJSON(sites)
		return
	}
	grouped := make(map[string][]*site.SiteMeta)
	for _, adapter := range sites {
		parts := strings.SplitN(adapter.Name, "/", 2)
		grouped[parts[0]] = append(grouped[parts[0]], adapter)
	}
	platforms := make([]string, 0, len(grouped))
	for platform := range grouped {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	for _, platform := range platforms {
		fmt.Printf("\n%s:\n", platform)
		for _, adapter := range grouped[platform] {
			var tags []string
			if adapter.Source == "local" {
				tags = append(tags, "local")
			}
			if adapter.ReadOnly {
				tags = append(tags, "read-only")
			}
			if adapter.UsageCount > 0 {
				tags = append(tags, fmt.Sprintf("used:%d", adapter.UsageCount))
			}
			tagText := ""
			if len(tags) > 0 {
				tagText = " [" + strings.Join(tags, ",") + "]"
			}
			fmt.Printf("  %s - %s%s\n", adapter.Name, adapter.Description, tagText)
		}
	}
	fmt.Printf("\nTotal: %d adapters\n", len(sites))
}

func printSiteInfo(adapter *site.SiteMeta, jsonOutput bool) {
	if jsonOutput {
		printJSON(adapter)
		return
	}
	fmt.Printf("Name:        %s\n", adapter.Name)
	fmt.Printf("Description: %s\n", adapter.Description)
	fmt.Printf("Domain:      %s\n", adapter.Domain)
	if adapter.StartURL != "" {
		fmt.Printf("Start URL:   %s\n", adapter.StartURL)
	}
	fmt.Printf("Source:       %s\n", adapter.Source)
	fmt.Printf("Source repo:  %s\n", adapter.SourceRepo)
	fmt.Printf("SHA256:      %s\n", adapter.SHA256)
	fmt.Printf("Read-only:   %v\n", adapter.ReadOnly)
	fmt.Printf("Trusted:     %v\n", adapter.Trusted)
	if adapter.TimeoutMs > 0 {
		fmt.Printf("Timeout:     %d ms\n", adapter.TimeoutMs)
	}
	if len(adapter.ArgOrder) > 0 {
		fmt.Printf("Arg order:   %s\n", strings.Join(adapter.ArgOrder, ", "))
	}
	if adapter.Example != "" {
		fmt.Printf("Example:     %s\n", adapter.Example)
	}
	if len(adapter.Args) > 0 {
		fmt.Println("Args:")
		for idx, name := range orderedSiteArgNames(adapter) {
			arg := adapter.Args[name]
			required := ""
			if arg.Required {
				required = " (required)"
			}
			def := ""
			if arg.Default != "" {
				def = fmt.Sprintf(" default=%q", arg.Default)
			}
			fmt.Printf("  %d. %s%s%s - %s (positional or --%s)\n", idx+1, name, required, def, arg.Description, name)
		}
	}
	if len(adapter.Output) > 0 {
		fmt.Printf("Output:      %s\n", string(adapter.Output))
	}
}
