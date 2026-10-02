package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

const profileRetireUsage = "Usage: borz profile retire <name> [--replaced-by <profile>] [--reason <text>]"

// handleProfileRetire marks a name as retired so selecting it fails with a
// pointer to its replacement instead of silently launching a fresh managed
// browser (what any undeclared name resolves to).
func handleProfileRetire(name string, rawArgs []string, jsonOutput bool) {
	if err := config.ValidateProfileName(name); err != nil {
		fatal(err.Error())
	}
	if borzprofile.Normalize(name) == borzprofile.DefaultName {
		fatal("the default profile cannot be retired")
	}
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	if _, declared := registry.Profiles[name]; declared {
		fatal(fmt.Sprintf("profile %q is still declared; remove it first ('borz profile rm %s') or point --replaced-by at its new name after renaming", name, name))
	}
	replacedBy, _ := getArgValueOK(rawArgs, "--replaced-by")
	replacedBy = strings.TrimSpace(replacedBy)
	if replacedBy != "" {
		if replacedBy == name {
			fatal("--replaced-by must name a different profile")
		}
		if err := config.ValidateProfileName(replacedBy); err != nil {
			fatal(err.Error())
		}
		if _, ok := registry.Retired[replacedBy]; ok {
			fatal(fmt.Sprintf("--replaced-by %q is itself retired", replacedBy))
		}
	}
	reasonRaw, _ := getArgValueOK(rawArgs, "--reason")
	reason, err := borzprofile.NormalizeDescription(reasonRaw)
	if err != nil {
		fatal(strings.Replace(err.Error(), "description", "reason", 1))
	}
	if registry.Retired == nil {
		registry.Retired = map[string]borzprofile.Retired{}
	}
	registry.Retired[name] = borzprofile.Retired{ReplacedBy: replacedBy, Reason: reason}
	if err := borzprofile.Save(registry); err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		printJSON(map[string]interface{}{"name": name, "retired": true, "replacedBy": replacedBy, "reason": reason, "path": config.ProfilesJSONPath()})
		return
	}
	fmt.Printf("Profile name %q retired.", name)
	if replacedBy != "" {
		fmt.Printf(" Commands using it now fail and point to %q.", replacedBy)
		if _, declared := registry.Profiles[replacedBy]; !declared {
			fmt.Printf("\nWarning: %q is not declared yet.", replacedBy)
		}
	}
	fmt.Println()
}

func handleProfileUnretire(name string, jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	if _, ok := registry.Retired[name]; !ok {
		fatal(fmt.Sprintf("profile name %q is not retired", name))
	}
	delete(registry.Retired, name)
	if err := borzprofile.Save(registry); err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		printJSON(map[string]interface{}{"name": name, "retired": false, "path": config.ProfilesJSONPath()})
		return
	}
	fmt.Printf("Profile name %q is usable again.\n", name)
}

func retiredProfileNames(registry *borzprofile.File) []string {
	names := make([]string, 0, len(registry.Retired))
	for name := range registry.Retired {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func retiredProfilePayload(registry *borzprofile.File) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(registry.Retired))
	for _, name := range retiredProfileNames(registry) {
		r := registry.Retired[name]
		out = append(out, map[string]interface{}{"name": name, "replacedBy": r.ReplacedBy, "reason": borzprofile.SanitizeDescription(r.Reason)})
	}
	return out
}

func printRetiredProfiles(registry *borzprofile.File) {
	names := retiredProfileNames(registry)
	if len(names) == 0 {
		return
	}
	fmt.Println("\nRetired names (selecting them fails):")
	for _, name := range names {
		r := registry.Retired[name]
		line := "  " + name
		if r.ReplacedBy != "" {
			line += " -> " + r.ReplacedBy
		}
		if reason := borzprofile.SanitizeDescription(r.Reason); reason != "" {
			line += "  (" + reason + ")"
		}
		fmt.Println(line)
	}
}

// checkSelectedProfileNotRetired fails when the selected profile name is
// retired. Registry load errors are left to the normal resolution path, which
// reports them with full context.
func checkSelectedProfileNotRetired(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	registry, err := borzprofile.Load()
	if err != nil {
		return nil
	}
	return registry.CheckRetired(borzprofile.Normalize(name))
}
