// Package config defines constants and path helpers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var userHomeDir = os.UserHomeDir

var (
	profileMu     sync.RWMutex
	activeProfile string
)

const (
	DaemonPort     = 19824
	DaemonHost     = "127.0.0.1"
	CommandTimeout = 30 // seconds
	DefaultCDPPort = 19825

	// DefaultIdleTabCloseMinutes is how long a tab may sit without a
	// user-initiated action before the daemon auto-closes it. 0 disables.
	DefaultIdleTabCloseMinutes = 30
)

const (
	HomeEnv        = "BORZ_HOME"
	LegacyHomeEnv  = "BB_BROWSER_HOME"
	HomeName       = ".borz"
	LegacyHomeName = ".bb-browser"
)

// Env returns the current env var when set, falling back to the legacy name.
func Env(name, legacyName string) string {
	if env := os.Getenv(name); env != "" {
		return env
	}
	return os.Getenv(legacyName)
}

// SetProfile selects a named local browser profile for runtime state. The
// default profile uses the historical top-level paths for compatibility.
func SetProfile(name string) error {
	name = strings.TrimSpace(name)
	profileMu.Lock()
	defer profileMu.Unlock()
	if name == "" || name == "default" {
		activeProfile = ""
		return nil
	}
	if strings.ContainsAny(name, `/\`) || filepath.Base(name) != name || name == "." || name == ".." || filepath.IsAbs(name) {
		return fmt.Errorf("profile name must be a single path segment")
	}
	activeProfile = name
	return nil
}

// Profile returns the selected local browser profile name, or "" for default.
func Profile() string {
	profileMu.RLock()
	defer profileMu.RUnlock()
	return activeProfile
}

// HomeDir returns the borz home directory. For read paths, it prefers ~/.borz
// when present, otherwise falls back to an existing legacy ~/.bb-browser.
func HomeDir() string {
	if env := os.Getenv(HomeEnv); env != "" {
		return env
	}
	if env := os.Getenv(LegacyHomeEnv); env != "" {
		return env
	}
	current, legacy, err := defaultHomeDirs()
	if err != nil {
		return HomeName
	}
	if dirExists(current) {
		return current
	}
	if dirExists(legacy) {
		return legacy
	}
	return current
}

// EnsureHomeDir returns a writable home directory, migrating ~/.bb-browser to
// ~/.borz when the new directory does not already exist.
func EnsureHomeDir() (string, error) {
	if env := os.Getenv(HomeEnv); env != "" {
		return env, os.MkdirAll(env, 0o755)
	}
	if env := os.Getenv(LegacyHomeEnv); env != "" {
		return env, os.MkdirAll(env, 0o755)
	}
	current, legacy, err := defaultHomeDirs()
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(current); err == nil && !st.IsDir() {
		return "", fmt.Errorf("%s exists and is not a directory", current)
	}
	if !dirExists(current) && dirExists(legacy) {
		if err := os.Rename(legacy, current); err != nil {
			return "", fmt.Errorf("migrate %s to %s: %w", legacy, current, err)
		}
		return current, nil
	}
	if err := os.MkdirAll(current, 0o755); err != nil {
		return "", err
	}
	return current, nil
}

func defaultHomeDirs() (current string, legacy string, err error) {
	home, err := userHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("find user home directory: %w", err)
	}
	return filepath.Join(home, HomeName), filepath.Join(home, LegacyHomeName), nil
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func runtimeDir() string {
	profile := Profile()
	if profile == "" {
		return HomeDir()
	}
	return filepath.Join(HomeDir(), "profiles", profile)
}

// EnsureRuntimeDir creates the directory used for daemon/browser runtime files.
func EnsureRuntimeDir() (string, error) {
	if _, err := EnsureHomeDir(); err != nil {
		return "", err
	}
	dir := runtimeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DaemonJSONPath returns the path to daemon.json.
func DaemonJSONPath() string {
	return filepath.Join(runtimeDir(), "daemon.json")
}

// ClientJSONPath returns the path to the remote client configuration.
func ClientJSONPath() string {
	return filepath.Join(HomeDir(), "client.json")
}

// SitesDir returns the local site adapters directory.
func SitesDir() string {
	return filepath.Join(HomeDir(), "sites")
}

// CommunitySitesDir returns the community site adapters directory.
func CommunitySitesDir() string {
	return filepath.Join(HomeDir(), "bb-sites")
}

// CommunityLockPath returns the pinned community adapter repo metadata path.
func CommunityLockPath() string {
	return filepath.Join(HomeDir(), "community.lock")
}

// SiteTrustPath returns the trusted adapter hash database path.
func SiteTrustPath() string {
	return filepath.Join(HomeDir(), "sites-trust.json")
}

// SitesUsagePath returns the site adapter usage database path.
func SitesUsagePath() string {
	return filepath.Join(HomeDir(), "sites-usage.json")
}

// ManagedBrowserDir returns the managed browser directory.
func ManagedBrowserDir() string {
	return filepath.Join(runtimeDir(), "browser")
}

// ManagedPortFile returns the path to the managed browser CDP port file.
func ManagedPortFile() string {
	return filepath.Join(ManagedBrowserDir(), "cdp-port")
}

// ManagedUserDataDir returns the user data directory for the managed browser.
func ManagedUserDataDir() string {
	return filepath.Join(ManagedBrowserDir(), "user-data")
}
