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

func TestDerivedPaths(t *testing.T) {
	t.Setenv("BORZ_HOME", "/tmp/borz")
	t.Setenv("BB_BROWSER_HOME", "")

	cases := []struct {
		name string
		fn   func() string
		want string
	}{
		{"DaemonJSONPath", DaemonJSONPath, "/tmp/borz/daemon.json"},
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
