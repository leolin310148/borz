// Package config defines constants and path helpers.
package config

import (
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

// HomeDir returns the bb-browser home directory (~/.bb-browser).
func HomeDir() string {
	if env := os.Getenv("BB_BROWSER_HOME"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".bb-browser")
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
