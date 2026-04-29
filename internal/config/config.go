// Package config defines constants and path helpers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// HomeDir returns the borz home directory. For read paths, it prefers ~/.borz
// when present, otherwise falls back to an existing legacy ~/.bb-browser.
func HomeDir() string {
	if env := os.Getenv(HomeEnv); env != "" {
		return env
	}
	if env := os.Getenv(LegacyHomeEnv); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, HomeName)
	if pathExists(current) {
		return current
	}
	legacy := filepath.Join(home, LegacyHomeName)
	if pathExists(legacy) {
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
	home, _ := os.UserHomeDir()
	current := filepath.Join(home, HomeName)
	legacy := filepath.Join(home, LegacyHomeName)
	if !pathExists(current) && pathExists(legacy) {
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

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DaemonJSONPath returns the path to daemon.json.
func DaemonJSONPath() string {
	return filepath.Join(HomeDir(), "daemon.json")
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
	return filepath.Join(HomeDir(), "browser")
}

// ManagedPortFile returns the path to the managed browser CDP port file.
func ManagedPortFile() string {
	return filepath.Join(ManagedBrowserDir(), "cdp-port")
}

// ManagedUserDataDir returns the user data directory for the managed browser.
func ManagedUserDataDir() string {
	return filepath.Join(ManagedBrowserDir(), "user-data")
}
