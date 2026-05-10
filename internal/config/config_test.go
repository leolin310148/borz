package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
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

func TestEnvWithNameReportsSelectedVariable(t *testing.T) {
	t.Setenv("BORZ_TEST_CURRENT", "current")
	t.Setenv("BB_BROWSER_TEST_LEGACY", "legacy")
	if got, name := EnvWithName("BORZ_TEST_CURRENT", "BB_BROWSER_TEST_LEGACY"); got != "current" || name != "BORZ_TEST_CURRENT" {
		t.Fatalf("EnvWithName current = %q, %q; want current, BORZ_TEST_CURRENT", got, name)
	}

	t.Setenv("BORZ_TEST_CURRENT", "")
	if got, name := EnvWithName("BORZ_TEST_CURRENT", "BB_BROWSER_TEST_LEGACY"); got != "legacy" || name != "BB_BROWSER_TEST_LEGACY" {
		t.Fatalf("EnvWithName legacy = %q, %q; want legacy, BB_BROWSER_TEST_LEGACY", got, name)
	}

	t.Setenv("BB_BROWSER_TEST_LEGACY", "")
	if got, name := EnvWithName("BORZ_TEST_CURRENT", "BB_BROWSER_TEST_LEGACY"); got != "" || name != "" {
		t.Fatalf("EnvWithName unset = %q, %q; want empty values", got, name)
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

func TestHomeDir_IgnoresNonDirectoryHomes(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".borz")
	legacy := filepath.Join(home, ".bb-browser")
	if err := os.WriteFile(current, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)
	if got := HomeDir(); got != legacy {
		t.Fatalf("HomeDir with file current and legacy dir = %q, want %q", got, legacy)
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

func TestEnsureHomeDir_CurrentPathMustBeDirectory(t *testing.T) {
	home := t.TempDir()
	current := filepath.Join(home, ".borz")
	if err := os.WriteFile(current, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	t.Setenv("HOME", home)

	if _, err := EnsureHomeDir(); err == nil {
		t.Fatal("EnsureHomeDir with file current path succeeded, want error")
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

func TestEnsureHomeDir_ReturnsUserHomeError(t *testing.T) {
	wantErr := errors.New("no home")
	prev := userHomeDir
	userHomeDir = func() (string, error) { return "", wantErr }
	t.Cleanup(func() { userHomeDir = prev })

	t.Setenv("BORZ_HOME", "")
	t.Setenv("BB_BROWSER_HOME", "")
	if got, err := EnsureHomeDir(); err == nil || !errors.Is(err, wantErr) || got != "" {
		t.Fatalf("EnsureHomeDir with missing user home = %q, %v; want empty path wrapping %v", got, err, wantErr)
	}
}

func TestDerivedPaths(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })
	if err := SetProfile(""); err != nil {
		t.Fatal(err)
	}
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
		{"CommunityLockPath", CommunityLockPath, "/tmp/borz/community.lock"},
		{"SiteTrustPath", SiteTrustPath, "/tmp/borz/sites-trust.json"},
		{"SitesUsagePath", SitesUsagePath, "/tmp/borz/sites-usage.json"},
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

func TestProfileRuntimePaths(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })
	t.Setenv("BORZ_HOME", "/tmp/borz")
	t.Setenv("BB_BROWSER_HOME", "")

	if err := SetProfile("work"); err != nil {
		t.Fatal(err)
	}
	if Profile() != "work" {
		t.Fatalf("Profile = %q, want work", Profile())
	}
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"DaemonJSONPath", DaemonJSONPath(), "/tmp/borz/profiles/work/daemon.json"},
		{"ClientJSONPath", ClientJSONPath(), "/tmp/borz/client.json"},
		{"ManagedBrowserDir", ManagedBrowserDir(), "/tmp/borz/profiles/work/browser"},
		{"ManagedPortFile", ManagedPortFile(), "/tmp/borz/profiles/work/browser/cdp-port"},
		{"ManagedUserDataDir", ManagedUserDataDir(), "/tmp/borz/profiles/work/browser/user-data"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if err := SetProfile("default"); err != nil {
		t.Fatal(err)
	}
	if Profile() != "" || DaemonJSONPath() != "/tmp/borz/daemon.json" {
		t.Fatalf("default profile did not reset: profile=%q daemon=%q", Profile(), DaemonJSONPath())
	}

}

func TestSetProfileTrimsNameAndDefault(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })

	if err := SetProfile(" work "); err != nil {
		t.Fatal(err)
	}
	if got := Profile(); got != "work" {
		t.Fatalf("Profile = %q, want work", got)
	}

	for _, name := range []string{" default ", " \t\n "} {
		if err := SetProfile(name); err != nil {
			t.Fatalf("SetProfile(%q): %v", name, err)
		}
		if got := Profile(); got != "" {
			t.Fatalf("Profile after SetProfile(%q) = %q, want default profile", name, got)
		}
	}
}

func TestSetProfileRejectsPathSegments(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })

	if err := SetProfile("work"); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"../x", "a/b", `a\b`, ".", "..", "work:dev", "bad*name", `quote"name`, "pipe|name", "what?now", "<hidden>", "line\nbreak", "tab\tname", "nul\x00name"} {
		if err := SetProfile(bad); err == nil {
			t.Fatalf("SetProfile(%q) succeeded, want error", bad)
		}
		if got := Profile(); got != "work" {
			t.Fatalf("Profile after rejected SetProfile(%q) = %q, want unchanged profile", bad, got)
		}
	}
}

func TestSetProfileRejectsWindowsReservedNames(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })

	if err := SetProfile("work"); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"CON", "prn", "AUX.log", "nul", "COM1", "com9.dev", "LPT1", "lpt9.profile"} {
		if err := SetProfile(bad); err == nil {
			t.Fatalf("SetProfile(%q) succeeded, want error", bad)
		}
		if got := Profile(); got != "work" {
			t.Fatalf("Profile after rejected SetProfile(%q) = %q, want unchanged profile", bad, got)
		}
	}
}

func TestProfileConcurrentAccess(t *testing.T) {
	t.Cleanup(func() { _ = SetProfile("") })
	t.Setenv("BORZ_HOME", "/tmp/borz")
	t.Setenv("BB_BROWSER_HOME", "")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := SetProfile("work"); err != nil {
					t.Errorf("SetProfile(work): %v", err)
				}
				if err := SetProfile(""); err != nil {
					t.Errorf("SetProfile(default): %v", err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = Profile()
				_ = DaemonJSONPath()
				_ = ManagedBrowserDir()
			}
		}()
	}
	wg.Wait()
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
