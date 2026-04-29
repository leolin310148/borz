package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/client"
	"github.com/leolin310148/bb-browser-go/internal/config"
	"github.com/leolin310148/bb-browser-go/internal/extupdate"
)

// extensionDir is where the bb-browser Chrome extension is extracted to.
// User loads it via chrome://extensions → "Load unpacked" → this path.
func extensionDir() string {
	return filepath.Join(config.HomeDir(), "extension")
}

func handleExtension(cmdArgs []string, jsonOutput bool) {
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
			SupportedMethods []string `json:"supportedMethods"`
			ConnectedAt      int64    `json:"connectedAt"`
		}
		if err := json.Unmarshal(raw, &caps); err != nil {
			fmt.Println(string(raw))
			return
		}
		fmt.Printf("%s %s connected\n", caps.Name, caps.Version)
		fmt.Printf("Supported extension RPC methods: %d\n", len(caps.SupportedMethods))
	case "call":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser extension call <method> [json-params]")
		}
		params := map[string]any{}
		if len(cmdArgs) > 2 {
			if err := json.Unmarshal([]byte(cmdArgs[2]), &params); err != nil {
				fatal("extension call params must be a JSON object: " + err.Error())
			}
		}
		raw, err := client.PostJSON("/v1/ext/call", map[string]any{"method": cmdArgs[1], "params": params}, 15*time.Second)
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
	default:
		fmt.Fprintf(os.Stderr, "Unknown 'extension' subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "Run 'bb-browser help extension' for usage.")
		os.Exit(1)
	}
}

func runExtensionDownload() {
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
	fmt.Printf("bb-browser extension %s installed to:\n  %s\n", tag, dir)
	fmt.Println()
	fmt.Println("Load it into Chrome:")
	fmt.Println("  1. Open chrome://extensions")
	fmt.Println("  2. Enable \"Developer mode\" (top-right)")
	fmt.Println("  3. Click \"Load unpacked\" and select the directory above")
	fmt.Println()
	fmt.Println("Re-run 'bb-browser extension update' to upgrade after a new release.")
}
