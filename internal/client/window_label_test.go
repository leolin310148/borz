package client

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

func TestManagedBrowserWindowName(t *testing.T) {
	for profile, want := range map[string]string{"": "borz", "default": "borz", "teams": "borz - teams", "camera_ops": "borz - camera_ops"} {
		if got := managedBrowserWindowName(profile); got != want {
			t.Fatalf("managedBrowserWindowName(%q) = %q, want %q", profile, got, want)
		}
	}
}

func TestManagedThemeColorIsStableAndOpaque(t *testing.T) {
	if managedThemeColor("teams") != managedThemeColor("teams") {
		t.Fatal("theme color must be stable per profile")
	}
	seen := map[int32]bool{}
	for _, name := range []string{"borz", "teams", "volvo", "clean", "mdt", "camera_ops"} {
		color := managedThemeColor(name)
		if uint32(color)>>24 != 0xFF {
			t.Fatalf("%s color %#x is not opaque", name, uint32(color))
		}
		seen[color] = true
	}
	if len(seen) < 3 {
		t.Fatalf("profiles should spread across colors, got %d distinct", len(seen))
	}
}

func TestWriteManagedBrowserPreferencesPreservesExistingSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Preferences")
	existing := `{"download":{"default_directory":"/tmp/dl"},"profile":{"name":"old","content_settings":{"x":1}},"browser":{"theme":{"user_color":123}}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedBrowserPreferences(path, "teams"); err != nil {
		t.Fatal(err)
	}
	var prefs map[string]map[string]interface{}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs["download"]["default_directory"] != "/tmp/dl" || prefs["profile"]["content_settings"] == nil {
		t.Fatalf("existing preferences were dropped: %s", data)
	}
	if prefs["profile"]["name"] != "teams" {
		t.Fatalf("profile name = %v", prefs["profile"]["name"])
	}
	if theme := prefs["browser"]["theme"].(map[string]interface{}); theme["user_color"] != float64(123) {
		t.Fatalf("a user-chosen theme color must be kept: %s", data)
	}

	fresh := filepath.Join(t.TempDir(), "Preferences")
	if err := os.WriteFile(fresh, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedBrowserPreferences(fresh, "teams"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(fresh)
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatal(err)
	}
	if theme := prefs["browser"]["theme"].(map[string]interface{}); theme["user_color"] != float64(managedThemeColor("teams")) {
		t.Fatalf("new profiles get the per-profile color: %s", data)
	}
}

func TestLaunchManagedBrowserNamesWindowAfterProfile(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	oldProfile := config.Profile()
	if err := config.SetProfile("teams"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile(oldProfile) })

	oldFinder, oldCanConnect, oldCommand, oldReadBrowserID := browserExecutableFinder, canConnect, execCommand, readBrowserID
	t.Cleanup(func() {
		browserExecutableFinder, canConnect, execCommand, readBrowserID = oldFinder, oldCanConnect, oldCommand, oldReadBrowserID
	})
	browserExecutableFinder = func() string { return "/bin/echo" }
	launched := false
	var gotArgs []string
	canConnect = func(string, int) bool { return launched }
	readBrowserID = func(string, int, time.Duration) (string, error) { return "fake-browser", nil }
	execCommand = func(_ string, args ...string) *exec.Cmd {
		launched = true
		gotArgs = args
		return exec.Command("/bin/sh", "-c", "exit 0")
	}
	if _, err := launchManagedBrowser(33335); err != nil {
		t.Fatalf("launchManagedBrowser: %v", err)
	}
	if !strings.Contains(strings.Join(gotArgs, "\n"), "--window-name=borz - teams") {
		t.Fatalf("launch args missing window name: %q", gotArgs)
	}
}
