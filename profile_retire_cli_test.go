package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	borzprofile "github.com/leolin310148/borz/internal/profile"
)

func TestProfileRetireLifecycle(t *testing.T) {
	setupProfileHome(t)
	runProfileCLI(t, "profile", "add", "mdt-vpn", "--cdp", "127.0.0.1:19845", "--no-check")

	// A declared name cannot be retired, and nor can default.
	expectExit(t, 1, func() { runMainArgsForExit("profile", "retire", "mdt-vpn") })
	expectExit(t, 1, func() { runMainArgsForExit("profile", "retire", "default") })
	expectExit(t, 1, func() { runMainArgsForExit("profile", "retire", "mdt", "--replaced-by", "mdt") })

	out := runProfileCLI(t, "profile", "retire", "mdt", "--replaced-by", "mdt-vpn", "--reason", "VPN jump host only")
	if !strings.Contains(out, `"mdt" retired`) || !strings.Contains(out, `"mdt-vpn"`) {
		t.Fatalf("retire output = %q", out)
	}

	registry, err := borzprofile.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Retired["mdt"]; got.ReplacedBy != "mdt-vpn" || got.Reason != "VPN jump host only" {
		t.Fatalf("stored retired entry = %+v", got)
	}

	// Resolution fails with a typed error naming the replacement.
	_, err = borzprofile.ResolveTarget("mdt")
	var retiredErr *borzprofile.RetiredError
	if !errors.As(err, &retiredErr) {
		t.Fatalf("ResolveTarget(mdt) err = %v, want RetiredError", err)
	}
	for _, want := range []string{"retired", "VPN jump host only", "--profile mdt-vpn", "profile unretire mdt"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if err := checkSelectedProfileNotRetired("mdt"); err == nil {
		t.Fatal("selected retired profile was not rejected")
	}
	if err := checkSelectedProfileNotRetired(""); err != nil {
		t.Fatalf("default selection rejected: %v", err)
	}

	// Every command refuses the name, including daemon/server; re-adding it is refused too.
	for _, args := range [][]string{
		{"--profile", "mdt", "status"},
		{"--profile", "mdt", "daemon"},
		{"--profile", "mdt", "server"},
		{"profile", "add", "mdt", "--managed"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			expectExit(t, 1, func() { runMainArgsForExit(args...) })
		})
	}
	t.Run("BORZ_PROFILE env", func(t *testing.T) {
		t.Setenv("BORZ_PROFILE", "mdt")
		expectExit(t, 1, func() { runMainArgsForExit("status") })
	})

	out = runProfileCLI(t, "profile", "list")
	if !strings.Contains(out, "Retired names") || !strings.Contains(out, "mdt -> mdt-vpn") {
		t.Fatalf("list output = %q", out)
	}
	out = runProfileCLI(t, "profile", "list", "--json")
	var listed struct {
		Retired []map[string]string `json:"retired"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed.Retired) != 1 || listed.Retired[0]["replacedBy"] != "mdt-vpn" {
		t.Fatalf("list --json retired = %+v (err %v) from %q", listed.Retired, err, out)
	}
	out = runProfileCLI(t, "profile", "show", "mdt")
	if !strings.Contains(out, "retired") || !strings.Contains(out, "mdt-vpn") {
		t.Fatalf("show output = %q", out)
	}

	out = runProfileCLI(t, "profile", "unretire", "mdt")
	if !strings.Contains(out, "usable again") {
		t.Fatalf("unretire output = %q", out)
	}
	if _, err := borzprofile.ResolveTarget("mdt"); err != nil {
		t.Fatalf("unretired name still fails: %v", err)
	}
	expectExit(t, 1, func() { runMainArgsForExit("profile", "unretire", "mdt") })
}

func TestProfileFileWithoutRetiredOmitsField(t *testing.T) {
	home := setupProfileHome(t)
	runProfileCLI(t, "profile", "add", "plain", "--managed")
	data, err := os.ReadFile(filepath.Join(home, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "retired") {
		t.Fatalf("profiles.json gained a retired field: %s", data)
	}
}
