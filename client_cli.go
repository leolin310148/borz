package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
)

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
			fatal("Usage: borz client setup <server-url> [--token <token>] [--no-check]")
		}

		token := getArgValue(rawArgs, "--token")
		if token == "" {
			token = config.Env("BORZ_TOKEN", "BB_BROWSER_TOKEN")
		}

		cfg, err := client.NewRemoteConfig(serverURL, token)
		if err != nil {
			fatal(err.Error())
		}
		if !hasFlag(rawArgs, "--no-check") {
			if err := client.CheckRemoteConfig(cfg, 5*time.Second); err != nil {
				fatal(err.Error())
			}
		}
		if err := client.WriteRemoteConfig(cfg); err != nil {
			fatal(err.Error())
		}
		if jsonOutput {
			printJSON(clientStatusPayload(cfg))
			return
		}
		fmt.Printf("Remote client configured: %s\n", cfg.URL)
		fmt.Println("Use 'borz --remote <command>' to send a command to this server")

	case "enable":
		cfg, err := client.ReadRemoteConfig()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fatal("client is not configured; run 'borz client setup <server-url> [--token <token>]'")
			}
			fatal(err.Error())
		}
		cfg.Enabled = true
		if !hasFlag(rawArgs, "--no-check") {
			if err := client.CheckRemoteConfig(cfg, 5*time.Second); err != nil {
				fatal(err.Error())
			}
		}
		if err := client.WriteRemoteConfig(cfg); err != nil {
			fatal(err.Error())
		}
		if jsonOutput {
			printJSON(clientStatusPayload(cfg))
			return
		}
		fmt.Printf("Remote client enabled in legacy config: %s\n", cfg.URL)
		fmt.Println("Browser actions still use local by default; pass --remote to use this server")

	case "disable":
		cfg, err := client.ReadRemoteConfig()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				cfg = &client.RemoteConfig{}
			} else {
				fatal(err.Error())
			}
		} else {
			cfg.Enabled = false
			if err := client.WriteRemoteConfig(cfg); err != nil {
				fatal(err.Error())
			}
		}
		if jsonOutput {
			printJSON(clientStatusPayload(cfg))
			return
		}
		fmt.Println("Remote client disabled in legacy config")

	case "status":
		cfg, err := client.ReadRemoteConfig()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if jsonOutput {
					printJSON(map[string]interface{}{
						"configured": false,
						"enabled":    false,
						"path":       config.ClientJSONPath(),
					})
					return
				}
				fmt.Println("Remote client is not configured")
				fmt.Printf("Config path: %s\n", config.ClientJSONPath())
				return
			}
			fatal(err.Error())
		}
		if jsonOutput {
			printJSON(clientStatusPayload(cfg))
			return
		}
		state := "disabled"
		if cfg.Enabled {
			state = "enabled"
		}
		fmt.Printf("Remote client config: %s (legacy global flag)\n", state)
		if client.RemoteRoutingEnabled() {
			fmt.Println("Remote routing: active for this command")
		} else {
			fmt.Println("Remote routing: inactive; use --remote before the command to activate")
		}
		fmt.Printf("Server: %s\n", cfg.URL)
		if cfg.Token != "" {
			fmt.Println("Token: configured")
		} else {
			fmt.Println("Token: not configured")
		}
		fmt.Printf("Config path: %s\n", config.ClientJSONPath())

	default:
		fatal(unknownSubcommandHint("client", sub))
	}
}

func clientStatusPayload(cfg *client.RemoteConfig) map[string]interface{} {
	payload := map[string]interface{}{
		"configured":      cfg != nil && cfg.URL != "",
		"enabled":         false,
		"remoteActive":    client.RemoteRoutingEnabled(),
		"url":             "",
		"tokenConfigured": false,
		"path":            config.ClientJSONPath(),
	}
	if cfg != nil {
		payload["enabled"] = cfg.Enabled
		payload["url"] = cfg.URL
		payload["tokenConfigured"] = cfg.Token != ""
	}
	return payload
}
