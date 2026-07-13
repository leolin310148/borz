package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

// handleClient keeps the deprecated `borz client` surface alive on top of
// profiles.json. `setup` writes a remote-transport profile (named 'remote'
// unless --as is given); enable/disable are no-ops retained for muscle
// memory; status reports the profile that bare --remote resolves to.
func handleClient(cmdArgs []string, rawArgs []string, jsonOutput bool) {
	sub := "status"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}

	switch sub {
	case "setup":
		serverURL := getArgValue(rawArgs, "--url")
		if serverURL == "" && len(cmdArgs) > 1 {
			serverURL = cmdArgs[1]
		}
		if serverURL == "" {
			serverURL = config.Env("BORZ_SERVER_URL", "BB_BROWSER_SERVER_URL")
		}
		if serverURL == "" {
			fatal("Usage: borz client setup <server-url> [--token <token>] [--as <profile>] [--no-check]")
		}

		token := getArgValue(rawArgs, "--token")
		if token == "" {
			token = config.Env("BORZ_TOKEN", "BB_BROWSER_TOKEN")
		}
		name := getArgValue(rawArgs, "--as")
		if name == "" {
			name = borzprofile.LegacyRemoteName
		}
		if err := config.ValidateProfileName(name); err != nil {
			fatal(err.Error())
		}
		registry, err := borzprofile.Load()
		if err != nil {
			fatal(err.Error())
		}
		fmt.Fprintf(os.Stderr, "Warning: 'borz client setup' is deprecated; use 'borz profile add %s --remote <url> --token <t>'\n", name)
		entry := borzprofile.Entry{
			Transport: string(borzprofile.TransportRemote),
			URL:       strings.TrimSpace(serverURL),
			Token:     strings.TrimSpace(token),
		}
		saveProfileEntry(registry, name, entry, rawArgs, jsonOutput, "configured")
		if !jsonOutput && name == borzprofile.LegacyRemoteName {
			fmt.Println("The deprecated '--remote' flag selects this profile.")
		}

	case "enable", "disable":
		fmt.Fprintf(os.Stderr, "Warning: 'borz client %s' is deprecated and does nothing; routing follows the profile's transport (see 'borz profile list')\n", sub)
		if jsonOutput {
			printJSON(clientStatusPayload())
		}

	case "status":
		if jsonOutput {
			printJSON(clientStatusPayload())
			return
		}
		registry, err := borzprofile.Load()
		if err != nil {
			fatal(err.Error())
		}
		entry, declared := registry.Profiles[borzprofile.LegacyRemoteName]
		if !declared {
			fmt.Println("Remote client is not configured")
			fmt.Printf("Configure it with 'borz profile add %s --remote <url> --token <t>'\n", borzprofile.LegacyRemoteName)
			fmt.Printf("Config path: %s\n", config.ProfilesJSONPath())
			return
		}
		fmt.Printf("Remote profile %q: %s\n", borzprofile.LegacyRemoteName, profileTargetDescription(borzprofile.LegacyRemoteName, entry))
		if strings.TrimSpace(entry.Token) != "" {
			fmt.Println("Token: configured")
		} else {
			fmt.Println("Token: not configured")
		}
		if client.RemoteRoutingEnabled() {
			fmt.Println("Remote routing: active for this command")
		} else {
			fmt.Printf("Remote routing: inactive; use '--profile %s' (or the deprecated '--remote') to activate\n", borzprofile.LegacyRemoteName)
		}
		fmt.Printf("Config path: %s\n", config.ProfilesJSONPath())

	default:
		fatal(unknownSubcommandHint("client", sub))
	}
}

func clientStatusPayload() map[string]interface{} {
	payload := map[string]interface{}{
		"configured":      false,
		"deprecated":      true,
		"remoteActive":    client.RemoteRoutingEnabled(),
		"url":             "",
		"tokenConfigured": false,
		"path":            config.ProfilesJSONPath(),
	}
	registry, err := borzprofile.Load()
	if err != nil {
		return payload
	}
	entry, declared := registry.Profiles[borzprofile.LegacyRemoteName]
	if !declared || borzprofile.TransportKind(entry.Transport) != borzprofile.TransportRemote {
		return payload
	}
	payload["configured"] = true
	payload["url"] = entry.URL
	payload["tokenConfigured"] = strings.TrimSpace(entry.Token) != ""
	return payload
}
