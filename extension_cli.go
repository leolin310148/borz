package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/extupdate"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

// extensionDir is where the borz Chrome extension is extracted to.
// User loads it via chrome://extensions → "Load unpacked" → this path.
func extensionDir() string {
	return filepath.Join(config.HomeDir(), "extension")
}

func handleExtension(cmdArgs []string, jsonOutput bool, rawArgSets ...[]string) {
	rawArgs := []string{}
	if len(rawArgSets) > 0 {
		rawArgs = rawArgSets[0]
	}
	sub := "download"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "download", "update", "install":
		runExtensionDownload()
	case "path":
		fmt.Println(extensionDir())
	case "status", "capabilities":
		if hasFlag(rawArgs, "--all-profiles") {
			handleExtensionStatusAll(jsonOutput)
			return
		}
		raw, err := client.GetJSON("/v1/ext/capabilities", 10*time.Second)
		if err != nil {
			fatal(err.Error())
		}
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var caps struct {
			Name             string   `json:"name"`
			Version          string   `json:"version"`
			Profile          string   `json:"profile"`
			SupportedMethods []string `json:"supportedMethods"`
			ConnectedAt      int64    `json:"connectedAt"`
		}
		if err := json.Unmarshal(raw, &caps); err != nil {
			fmt.Println(string(raw))
			return
		}
		fmt.Printf("%s %s connected", caps.Name, caps.Version)
		if caps.Profile != "" {
			fmt.Printf(" to profile %q", caps.Profile)
		}
		fmt.Println()
		fmt.Printf("Supported extension RPC methods: %d\n", len(caps.SupportedMethods))
	case "ping":
		runExtensionCall("ping", map[string]any{}, jsonOutput)
	case "call":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz extension call <method> [json-params]")
		}
		params := map[string]any{}
		if len(cmdArgs) > 2 {
			if err := json.Unmarshal([]byte(cmdArgs[2]), &params); err != nil {
				fatal("extension call params must be a JSON object: " + err.Error())
			}
		}
		runExtensionCall(cmdArgs[1], params, jsonOutput)
	default:
		fatal(unknownSubcommandHint("extension", sub))
	}
}

func runExtensionCall(method string, params map[string]any, jsonOutput bool) {
	raw, err := client.PostJSON("/v1/ext/call", map[string]any{"method": method, "params": params}, 15*time.Second)
	if err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		fmt.Println(string(raw))
		return
	}
	var pretty any
	if json.Unmarshal(raw, &pretty) == nil {
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Println(string(raw))
	}
}

type extensionProfileStatus struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	State     string `json:"state"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handleExtensionStatusAll audits every declared profile plus default without
// auto-starting offline local browsers.
func handleExtensionStatusAll(jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	names := make([]string, 0, len(registry.Profiles)+1)
	names = append(names, borzprofile.DefaultName)
	for name := range registry.Profiles {
		if name != borzprofile.DefaultName {
			names = append(names, name)
		}
	}
	sort.Strings(names[1:])

	statuses := make([]extensionProfileStatus, 0, len(names))
	for _, name := range names {
		target, targetErr := borzprofile.ResolveTarget(name)
		transport := string(borzprofile.TransportManaged)
		if targetErr == nil {
			transport = string(target.Kind)
		}
		status := extensionProfileStatus{Name: name, Transport: transport, State: "disconnected"}
		if targetErr != nil {
			status.State, status.Error = "invalid", targetErr.Error()
			statuses = append(statuses, status)
			continue
		}
		raw, getErr := client.GetJSONForProfile(name, "/v1/ext/capabilities", 3*time.Second)
		if getErr != nil {
			status.Error = getErr.Error()
			switch {
			case strings.Contains(status.Error, "503"), strings.Contains(status.Error, "no extension connected"):
				status.State = "no extension"
			case strings.Contains(status.Error, "daemon is not running"):
				status.State = "offline"
			default:
				status.State = "unreachable"
			}
			statuses = append(statuses, status)
			continue
		}
		var caps struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(raw, &caps)
		status.State = "connected"
		status.Version = caps.Version
		statuses = append(statuses, status)
	}

	if jsonOutput {
		printJSON(map[string]interface{}{"profiles": statuses})
		return
	}
	fmt.Printf("%-18s  %-9s  %-13s  %s\n", "PROFILE", "TRANSPORT", "EXTENSION", "VERSION / ERROR")
	for _, status := range statuses {
		detail := status.Version
		if detail == "" && status.Error != "" {
			detail = status.Error
		}
		fmt.Printf("%-18s  %-9s  %-13s  %s\n", status.Name, status.Transport, status.State, detail)
	}
}

func runExtensionDownload() {
	if _, err := config.EnsureHomeDir(); err != nil {
		fatal(err.Error())
	}
	dir := extensionDir()
	res, err := extupdate.Run(context.Background(), extupdate.Options{
		DestDir: dir,
	})
	if err != nil {
		fatal(err.Error())
	}
	printExtensionSetupHint(res.Tag, res.DestDir)
}

func printExtensionSetupHint(tag, dir string) {
	fmt.Println()
	fmt.Printf("borz extension %s installed to:\n  %s\n", tag, dir)
	fmt.Println()
	fmt.Println("Load it into Chrome:")
	fmt.Println("  1. Open chrome://extensions")
	fmt.Println("  2. Enable \"Developer mode\" (top-right)")
	fmt.Println("  3. Click \"Load unpacked\" and select the directory above")
	fmt.Println()
	fmt.Println("Re-run 'borz extension update' to upgrade after a new release.")
}
