package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

const profileAddUsage = "Usage: borz profile add <name> (--managed | --cdp <url|host:port> | --remote <url> [--token <t>]) [--no-check]"

func handleProfile(cmdArgs []string, rawArgs []string, jsonOutput bool) {
	sub := "list"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}

	switch sub {
	case "list":
		handleProfileList(jsonOutput)
	case "show":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz profile show <name>")
		}
		handleProfileShow(cmdArgs[1], jsonOutput)
	case "add":
		if len(cmdArgs) < 2 {
			fatal(profileAddUsage)
		}
		handleProfileAdd(cmdArgs[1], rawArgs, jsonOutput)
	case "set":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz profile set <name> [--managed | --cdp <url|host:port> | --remote <url>] [--token <t>] [--no-check]")
		}
		handleProfileSet(cmdArgs[1], rawArgs, jsonOutput)
	case "rm", "remove":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz profile rm <name>")
		}
		handleProfileRemove(cmdArgs[1], jsonOutput)
	default:
		fatal(unknownSubcommandHint("profile", sub))
	}
}

func handleProfileList(jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	names := make([]string, 0, len(registry.Profiles))
	for name := range registry.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	if jsonOutput {
		entries := make([]map[string]interface{}, 0, len(names))
		for _, name := range names {
			entries = append(entries, profilePayload(name, registry.Profiles[name]))
		}
		printJSON(map[string]interface{}{
			"path":     config.ProfilesJSONPath(),
			"profiles": entries,
		})
		return
	}

	if len(names) == 0 {
		fmt.Println("No profiles declared; every profile uses the managed transport (local browser).")
		fmt.Printf("Config path: %s\n", config.ProfilesJSONPath())
		fmt.Println("Add one with 'borz profile add <name> --remote <url> | --cdp <host:port> | --managed'")
		return
	}
	nameWidth, transportWidth := len("NAME"), len("TRANSPORT")
	for _, name := range names {
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
		if l := len(registry.Profiles[name].Transport); l > transportWidth {
			transportWidth = l
		}
	}
	fmt.Printf("%-*s  %-*s  %s\n", nameWidth, "NAME", transportWidth, "TRANSPORT", "TARGET")
	for _, name := range names {
		entry := registry.Profiles[name]
		fmt.Printf("%-*s  %-*s  %s\n", nameWidth, name, transportWidth, entry.Transport, profileTargetDescription(name, entry))
	}
	fmt.Printf("\nConfig path: %s\n", config.ProfilesJSONPath())
	fmt.Println("Undeclared names (including 'default') resolve to the managed transport.")
}

func handleProfileShow(name string, jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	entry, declared := registry.Profiles[name]
	if jsonOutput {
		payload := map[string]interface{}{
			"name":     name,
			"declared": declared,
			"path":     config.ProfilesJSONPath(),
		}
		if declared {
			for k, v := range profilePayload(name, entry) {
				payload[k] = v
			}
		} else {
			payload["transport"] = string(borzprofile.TransportManaged)
		}
		printJSON(payload)
		return
	}

	if !declared {
		fmt.Printf("Profile %q is not declared; it resolves to the managed transport (default behaviour).\n", name)
		fmt.Printf("Config path: %s\n", config.ProfilesJSONPath())
		return
	}
	fmt.Printf("Profile:   %s\n", name)
	fmt.Printf("Transport: %s\n", entry.Transport)
	fmt.Printf("Target:    %s\n", profileTargetDescription(name, entry))
	if borzprofile.TransportKind(entry.Transport) == borzprofile.TransportRemote {
		if strings.TrimSpace(entry.Token) != "" {
			fmt.Println("Token:     configured")
		} else {
			fmt.Println("Token:     not configured")
		}
	}
	fmt.Printf("Config path: %s\n", config.ProfilesJSONPath())
}

func handleProfileAdd(name string, rawArgs []string, jsonOutput bool) {
	if err := config.ValidateProfileName(name); err != nil {
		fatal(err.Error())
	}
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	if _, exists := registry.Profiles[name]; exists {
		fatal(fmt.Sprintf("profile %q already exists; use 'borz profile set %s ...' to modify it", name, name))
	}
	entry, changed, err := profileEntryFromFlags(borzprofile.Entry{}, rawArgs)
	if err != nil {
		fatal(err.Error())
	}
	if !changed {
		fatal(profileAddUsage)
	}
	saveProfileEntry(registry, name, entry, rawArgs, jsonOutput, "added")
}

func handleProfileSet(name string, rawArgs []string, jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	entry, exists := registry.Profiles[name]
	if !exists {
		fatal(fmt.Sprintf("profile %q is not declared; use 'borz profile add %s ...' to create it", name, name))
	}
	entry, changed, err := profileEntryFromFlags(entry, rawArgs)
	if err != nil {
		fatal(err.Error())
	}
	if !changed {
		fatal("nothing to change; pass --managed, --cdp <endpoint>, --remote <url>, or --token <t>")
	}
	saveProfileEntry(registry, name, entry, rawArgs, jsonOutput, "updated")
}

func handleProfileRemove(name string, jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	if _, exists := registry.Profiles[name]; !exists {
		fatal(fmt.Sprintf("profile %q is not declared", name))
	}
	delete(registry.Profiles, name)
	if err := borzprofile.Save(registry); err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		printJSON(map[string]interface{}{"name": name, "removed": true, "path": config.ProfilesJSONPath()})
		return
	}
	fmt.Printf("Profile %q removed. Its name now resolves to the managed transport again.\n", name)
}

// profileEntryFromFlags applies the transport/token flags to base. It returns
// the updated entry and whether any flag actually changed it.
func profileEntryFromFlags(base borzprofile.Entry, rawArgs []string) (borzprofile.Entry, bool, error) {
	managedSet := hasFlag(rawArgs, "--managed")
	cdpValue, cdpSet := getArgValueOK(rawArgs, "--cdp")
	remoteValue, remoteSet := getArgValueOK(rawArgs, "--remote")
	tokenValue, tokenSet := getArgValueOK(rawArgs, "--token")

	transports := 0
	for _, set := range []bool{managedSet, cdpSet, remoteSet} {
		if set {
			transports++
		}
	}
	if transports > 1 {
		return borzprofile.Entry{}, false, fmt.Errorf("--managed, --cdp, and --remote are mutually exclusive")
	}

	entry := base
	switch {
	case managedSet:
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportManaged)}
	case cdpSet:
		if strings.TrimSpace(cdpValue) == "" {
			return borzprofile.Entry{}, false, fmt.Errorf("--cdp requires a value (http://host:port or host:port)")
		}
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportCDP), CDPURL: strings.TrimSpace(cdpValue)}
	case remoteSet:
		if strings.TrimSpace(remoteValue) == "" {
			return borzprofile.Entry{}, false, fmt.Errorf("--remote requires a server URL")
		}
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportRemote), URL: strings.TrimSpace(remoteValue), Token: base.Token}
	}
	if !tokenSet && remoteSet {
		// Match 'client setup': whenever a remote target is (re)configured
		// without an explicit --token, the env token wins — even over a
		// previously stored token, so add and set resolve tokens identically.
		if envToken := strings.TrimSpace(config.Env("BORZ_TOKEN", "BB_BROWSER_TOKEN")); envToken != "" {
			tokenValue, tokenSet = envToken, true
		}
	}
	if tokenSet {
		if borzprofile.TransportKind(entry.Transport) != borzprofile.TransportRemote {
			return borzprofile.Entry{}, false, fmt.Errorf("--token only applies to remote profiles")
		}
		entry.Token = strings.TrimSpace(tokenValue)
	}
	return entry, transports == 1 || tokenSet, nil
}

// saveProfileEntry validates, optionally probes, persists, and reports one
// profile entry. Shared by 'profile add', 'profile set', and the deprecated
// 'client setup'.
func saveProfileEntry(registry *borzprofile.File, name string, entry borzprofile.Entry, rawArgs []string, jsonOutput bool, verb string) {
	target, err := borzprofile.ResolveEntry(name, entry)
	if err != nil {
		fatal(err.Error())
	}
	if !hasFlag(rawArgs, "--no-check") {
		if err := probeProfileTarget(target); err != nil {
			fatal(err.Error() + " (use --no-check to save anyway)")
		}
	}
	// Persist the normalized spelling, not the raw flag value.
	switch target.Kind {
	case borzprofile.TransportRemote:
		entry.URL = target.Remote.URL
	case borzprofile.TransportCDP:
		entry.CDPURL = fmt.Sprintf("http://%s:%d", target.CDP.Host, target.CDP.Port)
	}
	registry.Profiles[name] = entry
	if err := borzprofile.Save(registry); err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		printJSON(profilePayload(name, entry))
		return
	}
	fmt.Printf("Profile %q %s (%s -> %s)\n", name, verb, entry.Transport, profileTargetDescription(name, entry))
	fmt.Printf("Select it with 'borz --profile %s <command>' or BORZ_PROFILE=%s\n", name, name)
}

func probeProfileTarget(target borzprofile.Target) error {
	switch target.Kind {
	case borzprofile.TransportRemote:
		return client.CheckRemoteConfig(&client.RemoteConfig{URL: target.Remote.URL, Token: target.Remote.Token}, 5*time.Second)
	case borzprofile.TransportCDP:
		return client.CheckCDPEndpoint(target.CDP.Host, target.CDP.Port, 5*time.Second)
	default:
		return nil
	}
}

// profileTargetDescription renders where a profile points, never including
// the token.
func profileTargetDescription(name string, entry borzprofile.Entry) string {
	target, err := borzprofile.ResolveEntry(name, entry)
	if err != nil {
		return "invalid: " + err.Error()
	}
	switch target.Kind {
	case borzprofile.TransportRemote:
		return target.Remote.URL
	case borzprofile.TransportCDP:
		return fmt.Sprintf("http://%s:%d", target.CDP.Host, target.CDP.Port)
	default:
		return "local managed browser"
	}
}

// profilePayload is the JSON shape for one profile; tokens are redacted to a
// boolean.
func profilePayload(name string, entry borzprofile.Entry) map[string]interface{} {
	payload := map[string]interface{}{
		"name":      name,
		"transport": entry.Transport,
		"target":    profileTargetDescription(name, entry),
	}
	if borzprofile.TransportKind(entry.Transport) == borzprofile.TransportRemote {
		payload["tokenConfigured"] = strings.TrimSpace(entry.Token) != ""
	}
	return payload
}
