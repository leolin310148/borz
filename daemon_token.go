package main

import (
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

var copyDaemonTokenToClipboard = copySecretToClipboard

// handleDaemonToken prints or copies the selected local profile's daemon
// token without starting a browser. A configured stable token is available
// even while the daemon is stopped; a dynamic token requires daemon.json.
func handleDaemonToken(rawArgs []string) {
	if url, isRemote := remoteProfileURL(); isRemote {
		fatal(remoteProfileLifecycleNote("daemon token", url))
		return
	}

	// Prefer the token accepted by the daemon that is running right now. This
	// matters immediately after profile set --daemon-token: the configured
	// token only takes effect after that daemon is restarted.
	token := ""
	if info, err := client.ReadDaemonJSON(); err == nil && info != nil && client.IsProcessAlive(info.PID) {
		token = strings.TrimSpace(info.Token)
	}
	if token == "" {
		if target, err := client.ActiveTarget(); err == nil {
			token = strings.TrimSpace(target.DaemonToken)
		}
	}
	if token == "" {
		fatal("daemon token is unavailable; start this profile once, or configure a stable token with 'borz profile set " + borzprofile.Normalize(config.Profile()) + " --daemon-token generate'")
		return
	}

	profileName := borzprofile.Normalize(config.Profile())
	if hasFlag(rawArgs, "--copy") {
		if err := copyDaemonTokenToClipboard(token); err != nil {
			fatal("copy daemon token: " + err.Error())
			return
		}
		if hasFlag(rawArgs, "--json") {
			printJSON(map[string]interface{}{"profile": profileName, "copied": true})
			return
		}
		fmt.Printf("Daemon token for profile %q copied to the clipboard.\n", profileName)
		return
	}
	if hasFlag(rawArgs, "--json") {
		printJSON(map[string]interface{}{"profile": profileName, "token": token})
		return
	}
	fmt.Println(token)
}
