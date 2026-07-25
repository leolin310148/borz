package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

const profileAddUsage = "Usage: borz profile add <name> (--managed | --cdp <url|host:port> | --remote <url> [--token <t>]) [--description <text>] [--idle-tab-timeout <m>] [--max-tabs <n>] [--no-check]"

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
			fatal("Usage: borz profile set <name> [--managed | --cdp <url|host:port> | --remote <url>] [--token <t>] [--description <text>] [--idle-tab-timeout <m|default>] [--max-tabs <n|default>] [--no-check]")
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
	nameWidth, transportWidth, targetWidth := len("NAME"), len("TRANSPORT"), len("TARGET")
	targets := make(map[string]string, len(names))
	anyDescribed := false
	for _, name := range names {
		entry := registry.Profiles[name]
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
		if l := len(entry.Transport); l > transportWidth {
			transportWidth = l
		}
		target := profileTargetDescription(name, entry)
		if entry.IdleTabTimeout != nil {
			target += fmt.Sprintf(" [idleTabTimeout=%d]", *entry.IdleTabTimeout)
		}
		if entry.MaxTabs != nil {
			target += fmt.Sprintf(" [maxTabs=%d]", *entry.MaxTabs)
		}
		targets[name] = target
		if len(target) > targetWidth {
			targetWidth = len(target)
		}
		if borzprofile.SanitizeDescription(entry.Description) != "" {
			anyDescribed = true
		}
	}
	// The DESCRIPTION column only appears once some profile has one, so the
	// listing keeps its old shape for registries that never set descriptions.
	if anyDescribed {
		fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameWidth, "NAME", transportWidth, "TRANSPORT", targetWidth, "TARGET", "DESCRIPTION")
	} else {
		fmt.Printf("%-*s  %-*s  %s\n", nameWidth, "NAME", transportWidth, "TRANSPORT", "TARGET")
	}
	for _, name := range names {
		entry := registry.Profiles[name]
		if anyDescribed {
			fmt.Printf("%-*s  %-*s  %-*s  %s\n", nameWidth, name, transportWidth, entry.Transport, targetWidth, targets[name], borzprofile.SanitizeDescription(entry.Description))
			continue
		}
		fmt.Printf("%-*s  %-*s  %s\n", nameWidth, name, transportWidth, entry.Transport, targets[name])
	}
	fmt.Printf("\nConfig path: %s\n", config.ProfilesJSONPath())
	fmt.Println("Undeclared names (including 'default') resolve to the managed transport.")
	if !anyDescribed {
		fmt.Println("No profile says what it is for; add one with 'borz profile set <name> --description \"...\"'.")
	}
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
	if desc := borzprofile.SanitizeDescription(entry.Description); desc != "" {
		fmt.Printf("Purpose:   %s\n", desc)
	}
	if borzprofile.TransportKind(entry.Transport) == borzprofile.TransportRemote {
		if strings.TrimSpace(entry.Token) != "" {
			fmt.Println("Token:     configured")
		} else {
			fmt.Println("Token:     not configured")
		}
	} else {
		fmt.Printf("Idle tab timeout: %s\n", idleTabTimeoutDescription(entry.IdleTabTimeout))
		fmt.Printf("Max tabs:         %s\n", maxTabsDescription(entry.MaxTabs))
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
	stored := entry
	entry, changed, err := profileEntryFromFlags(entry, rawArgs)
	if err != nil {
		fatal(err.Error())
	}
	if !changed {
		fatal("nothing to change; pass --managed, --cdp <endpoint>, --remote <url>, --token <t>, --description <text>, --idle-tab-timeout <m|default>, or --max-tabs <n|default>")
	}
	if profileTargetUnchanged(stored, entry) {
		// Editing only the description or tab-lifecycle fields must not fail
		// because a tunnel happens to be down: the target is already declared
		// and nothing about how borz reaches it changed.
		rawArgs = append(rawArgs, "--no-check")
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

// profileEntryFromFlags applies the transport/token/description/tab-lifecycle
// flags to base. It returns the updated entry and whether any flag actually
// changed it.
func profileEntryFromFlags(base borzprofile.Entry, rawArgs []string) (borzprofile.Entry, bool, error) {
	managedSet := hasFlag(rawArgs, "--managed")
	cdpValue, cdpSet := getArgValueOK(rawArgs, "--cdp")
	remoteValue, remoteSet := getArgValueOK(rawArgs, "--remote")
	tokenValue, tokenSet := getArgValueOK(rawArgs, "--token")
	descValue, descSet := getArgValueOK(rawArgs, "--description")
	idleValue, idleSet := getArgValueOK(rawArgs, "--idle-tab-timeout")
	maxTabsValue, maxTabsSet := getArgValueOK(rawArgs, "--max-tabs")

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
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportManaged), Description: base.Description, IdleTabTimeout: base.IdleTabTimeout, MaxTabs: base.MaxTabs}
	case cdpSet:
		if strings.TrimSpace(cdpValue) == "" {
			return borzprofile.Entry{}, false, fmt.Errorf("--cdp requires a value (http://host:port or host:port)")
		}
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportCDP), Description: base.Description, CDPURL: strings.TrimSpace(cdpValue), IdleTabTimeout: base.IdleTabTimeout, MaxTabs: base.MaxTabs}
	case remoteSet:
		if strings.TrimSpace(remoteValue) == "" {
			return borzprofile.Entry{}, false, fmt.Errorf("--remote requires a server URL")
		}
		// Tab lifecycle settings do not apply to remote targets, so they are dropped
		// rather than carried into an entry that would fail validation. The
		// description survives: it describes the profile, not the transport.
		entry = borzprofile.Entry{Transport: string(borzprofile.TransportRemote), Description: base.Description, URL: strings.TrimSpace(remoteValue), Token: base.Token}
	}
	if descSet {
		desc, err := borzprofile.NormalizeDescription(descValue)
		if err != nil {
			return borzprofile.Entry{}, false, err
		}
		entry.Description = desc
	}
	if idleSet {
		if strings.EqualFold(strings.TrimSpace(idleValue), "default") {
			entry.IdleTabTimeout = nil
		} else {
			n, err := strconv.Atoi(strings.TrimSpace(idleValue))
			if err != nil || n < 0 {
				return borzprofile.Entry{}, false, fmt.Errorf("--idle-tab-timeout must be a non-negative number of minutes (0 disables auto-close) or 'default'")
			}
			entry.IdleTabTimeout = &n
		}
	}
	if maxTabsSet {
		if strings.EqualFold(strings.TrimSpace(maxTabsValue), "default") {
			entry.MaxTabs = nil
		} else {
			n, err := strconv.Atoi(strings.TrimSpace(maxTabsValue))
			if err != nil || n < 0 {
				return borzprofile.Entry{}, false, fmt.Errorf("--max-tabs must be a non-negative tab count (0 disables the cap) or 'default'")
			}
			entry.MaxTabs = &n
		}
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
	return entry, transports == 1 || tokenSet || descSet || idleSet || maxTabsSet, nil
}

// profileTargetUnchanged reports whether two entries reach the same browser
// the same way, i.e. whether re-probing the target could tell us anything new.
func profileTargetUnchanged(old, updated borzprofile.Entry) bool {
	return old.Transport == updated.Transport &&
		old.URL == updated.URL &&
		old.Token == updated.Token &&
		old.CDPURL == updated.CDPURL &&
		old.CDPHost == updated.CDPHost &&
		old.CDPPort == updated.CDPPort
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
	if desc := borzprofile.SanitizeDescription(entry.Description); desc != "" {
		payload["description"] = desc
	}
	if borzprofile.TransportKind(entry.Transport) == borzprofile.TransportRemote {
		payload["tokenConfigured"] = strings.TrimSpace(entry.Token) != ""
	}
	if entry.IdleTabTimeout != nil {
		payload["idleTabTimeout"] = *entry.IdleTabTimeout
	}
	if entry.MaxTabs != nil {
		payload["maxTabs"] = *entry.MaxTabs
	}
	return payload
}

// maxTabsDescription renders a profile's maxTabs for humans.
func maxTabsDescription(maxTabs *int) string {
	switch {
	case maxTabs == nil:
		return fmt.Sprintf("default (%d; flag/env may override)", config.DefaultMaxTabs)
	case *maxTabs == 0:
		return "0 (tab cap disabled)"
	default:
		return strconv.Itoa(*maxTabs)
	}
}

// idleTabTimeoutDescription renders a profile's idleTabTimeout for humans.
func idleTabTimeoutDescription(minutes *int) string {
	switch {
	case minutes == nil:
		return fmt.Sprintf("default (%d minutes; flag/env may override)", config.DefaultIdleTabCloseMinutes)
	case *minutes == 0:
		return "0 (idle-tab auto-close disabled)"
	default:
		return fmt.Sprintf("%d minutes", *minutes)
	}
}
