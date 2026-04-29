package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDir_EnvOverride(t *testing.T) {
	t.Setenv("BORZ_HOME", "/tmp/borz-override")
	t.Setenv("BB_BROWSER_HOME", "/tmp/legacy-override")
	if got := HomeDir(); got != "/tmp/borz-override" {
		t.Fatalf("HomeDir with override = %q, want /tmp/borz-override", got)
	}
}

func TestEnvPrefersCurrentNameThenLegacy(t *testing.T) {
	t.Setenv("BORZ_TEST_CURRENT", "current")
	t.Setenv("BB_BROWSER_TEST_LEGACY", "legacy")
	if got := Env("BORZ_TEST_CURRENT", "BB_BROWSER_TEST_LEGACY"); got != "current" {
		t.Fatalf("Env current = %q", got)
	}
	t.Setenv("BORZ_TEST_CURRENT", "")
	if got := Env("BORZ_TEST_CURRENT", "BB_BROWSER_TEST_LEGACY"); got != "legacy" {
		t.Fatalf("Env legacy = %q", got)
	}
}

func TestHomeDir_LegacyEnvOverride(t *testing.T) {
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "/tmp/legacy-override")
	if got := HomeDir(); got != "/tmp/legacy-override" {
		t.Fatalf("HomeDir with legacy override = %q, want /tmp/legacy-override", got)
	}
}

func TestHomeDir_Default(t *testing.T) {
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome")
	want := filepath.Join("/tmp/fakehome", ".borz")
	if got := HomeDir(); got != want {
		t.Fatalf("HomeDir default = %q, want %q", got, want)
	}
}

func TestHomeDir_ReadsLegacyDirWhenCurrentMissing(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".bb-browser")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)
	if got := HomeDir(); got != legacy {
		t.Fatalf("HomeDir with only legacy dir = %q, want %q", got, legacy)
	}
}

func TestHomeDir_PrefersCurrentDirOverLegacyDir(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".borz")
	legacy := filepath.Join(home, ".bb-browser")
	if err := os.Mkdir(current, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)
	if got := HomeDir(); got != current {
		t.Fatalf("HomeDir with current and legacy dirs = %q, want %q", got, current)
	}
}

func TestEnsureHomeDir_MigratesLegacyDir(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".bb-browser")
	current := filepath.Join(home, ".borz")
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "client.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)

	got, err := EnsureHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("EnsureHomeDir = %q, want %q", got, current)
	}
	if _, err := os.Stat(filepath.Join(current, "client.json")); err != nil {
		t.Fatalf("migrated client.json missing: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy dir still exists after migration: %v", err)
	}
}

func TestEnsureHomeDir_EnvOverridesCreateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom-borz-home")
	t.Setenv("BORZ_HOME", dir)
	got, err := EnsureHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("EnsureHomeDir = %q, want %q", got, dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("custom home not created: stat=%v err=%v", st, err)
	}

	legacy := filepath.Join(t.TempDir(), "legacy-home")
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", legacy)
	got, err = EnsureHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("EnsureHomeDir legacy = %q, want %q", got, legacy)
	}
}

func TestEnsureHomeDir_DefaultCreatesCurrentDir(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".borz")
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)
	got, err := EnsureHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("EnsureHomeDir default = %q, want %q", got, current)
	}
	if st, err := os.Stat(current); err != nil || !st.IsDir() {
		t.Fatalf("current home not created: stat=%v err=%v", st, err)
	}
}

func TestDerivedPaths(t *testing.T) {
	t.Setenv("BORZ_HOME", "/tmp/borz")
	t.Setenv("BB_BROWSER_HOME", "")

	cases := []struct {
		name string
		fn   func() string
		want string
	}{
		{"DaemonJSONPath", DaemonJSONPath, "/tmp/borz/daemon.json"},
		{"ClientJSONPath", ClientJSONPath, "/tmp/borz/client.json"},
		{"SitesDir", SitesDir, "/tmp/borz/sites"},
		{"CommunitySitesDir", CommunitySitesDir, "/tmp/borz/bb-sites"},
		{"ManagedBrowserDir", ManagedBrowserDir, "/tmp/borz/browser"},
		{"ManagedPortFile", ManagedPortFile, "/tmp/borz/browser/cdp-port"},
		{"ManagedUserDataDir", ManagedUserDataDir, "/tmp/borz/browser/user-data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.fn(); got != c.want {
				t.Fatalf("%s = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if DaemonPort != 19824 {
		t.Errorf("DaemonPort = %d, want 19824", DaemonPort)
	}
	if DaemonHost != "127.0.0.1" {
		t.Errorf("DaemonHost = %q, want 127.0.0.1", DaemonHost)
	}
	if CommandTimeout != 30 {
		t.Errorf("CommandTimeout = %d, want 30", CommandTimeout)
	}
	if DefaultCDPPort != 19825 {
		t.Errorf("DefaultCDPPort = %d, want 19825", DefaultCDPPort)
	}
}
