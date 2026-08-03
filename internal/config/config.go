// Package config defines constants and path helpers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
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
	DefaultIdleTabCloseMinutes = 0

	// DefaultMaxTabs caps the number of page tabs retained by the daemon.
	// 0 disables the cap.
	DefaultMaxTabs = 30
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

// EnvWithName returns the current or legacy env var value along with the name
// that supplied it. Empty env vars are treated as unset.
func EnvWithName(name, legacyName string) (string, string) {
	if env := os.Getenv(name); env != "" {
		return env, name
	}
	if env := os.Getenv(legacyName); env != "" {
		return env, legacyName
	}
	return "", ""
}

// SetProfile selects a named local browser profile for runtime state. The
// default profile uses the historical top-level paths for compatibility.
func SetProfile(name string) error {
	name = strings.TrimSpace(name)
	profileMu.Lock()
	defer profileMu.Unlock()
	if name == "" || strings.EqualFold(name, "default") {
		activeProfile = ""
		return nil
	}
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	activeProfile = name
	return nil
}

// ValidateProfileName rejects names that cannot double as a directory name on
// every supported platform. Profile names become runtime directories for
// managed/cdp profiles, so they must stay a portable single path segment.
func ValidateProfileName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("profile name is required")
	}
	if strings.ContainsAny(name, `/\<>:"|?*`) || strings.ContainsFunc(name, unicode.IsControl) || filepath.Base(name) != name || name == "." || name == ".." || strings.HasSuffix(name, ".") || filepath.IsAbs(name) || isWindowsReservedName(name) {
		return fmt.Errorf("profile name must be a portable single path segment")
	}
	return nil
}

// Profile returns the selected local browser profile name, or "" for default.
func Profile() string {
	profileMu.RLock()
	defer profileMu.RUnlock()
	return activeProfile
}

func isWindowsReservedName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	case "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9":
		return true
	case "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
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
	return RuntimeDirFor(Profile())
}

// RuntimeDirFor returns the runtime directory of an arbitrary profile without
// changing the active one. The default profile ("" or "default") keeps the
// historical top-level paths, so its runtime directory *is* the borz home —
// callers that delete must account for that and never remove it wholesale.
func RuntimeDirFor(profile string) string {
	profile = normalizeProfileName(profile)
	if profile == "" {
		return HomeDir()
	}
	return filepath.Join(HomeDir(), "profiles", profile)
}

// ProfilesDir returns the parent directory holding every named profile's
// runtime state. The default profile is deliberately not in here.
func ProfilesDir() string {
	return filepath.Join(HomeDir(), "profiles")
}

// ManagedBrowserDirFor returns a named profile's managed browser directory.
func ManagedBrowserDirFor(profile string) string {
	return filepath.Join(RuntimeDirFor(profile), "browser")
}

// ManagedStateFileFor returns a named profile's managed browser identity file.
func ManagedStateFileFor(profile string) string {
	return filepath.Join(ManagedBrowserDirFor(profile), "browser.json")
}

// DaemonJSONPathFor returns a named profile's daemon.json.
func DaemonJSONPathFor(profile string) string {
	return filepath.Join(RuntimeDirFor(profile), "daemon.json")
}

// LogsDirFor returns a named profile's log directory. Unlike runtime state,
// the default profile's logs live under an explicit "default" directory.
func LogsDirFor(profile string) string {
	if profile = normalizeProfileName(profile); profile == "" {
		profile = "default"
	}
	return filepath.Join(HomeDir(), "logs", profile)
}

// normalizeProfileName maps the two spellings of the default profile onto the
// empty string that the runtime paths key off.
func normalizeProfileName(profile string) string {
	profile = strings.TrimSpace(profile)
	if strings.EqualFold(profile, "default") {
		return ""
	}
	return profile
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

// LogsDir returns the profile-specific directory for persistent operational
// logs. Logs live outside the runtime directory so their location stays
// predictable while keeping named profiles isolated.
func LogsDir() string {
	return LogsDirFor(Profile())
}

// ClientJSONPath returns the path to the legacy remote client configuration.
func ClientJSONPath() string {
	return filepath.Join(HomeDir(), "client.json")
}

// ProfilesJSONPath returns the path to the declarative profile registry. The
// file is shared by all profiles; it is not profile-scoped.
func ProfilesJSONPath() string {
	return filepath.Join(HomeDir(), "profiles.json")
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
	return ManagedBrowserDirFor(Profile())
}

// ManagedPortFile returns the path to the managed browser CDP port file.
func ManagedPortFile() string {
	return filepath.Join(ManagedBrowserDir(), "cdp-port")
}

// ManagedUserDataDir returns the user data directory for the managed browser.
func ManagedUserDataDir() string {
	return filepath.Join(ManagedBrowserDir(), "user-data")
}

// ManagedStateFile returns the profile-scoped managed browser identity file.
// Unlike cdp-port, this records the browser-level CDP identity so a stale port
// cannot make an unrelated Chrome look borz-owned.
func ManagedStateFile() string {
	return ManagedStateFileFor(Profile())
}

// StartupLockPath serializes client-side daemon discovery and auto-start for
// one profile across independent CLI processes.
func StartupLockPath() string {
	return filepath.Join(runtimeDir(), "startup.lock")
}

// BrowserLockPath serializes managed Chrome launch/recovery for one profile.
func BrowserLockPath() string {
	return filepath.Join(ManagedBrowserDir(), "browser.lock")
}

// DaemonLockPath is held for the lifetime of a daemon, making daemon.json a
// single-writer state file even when named profiles use dynamic listen ports.
func DaemonLockPath() string {
	return filepath.Join(runtimeDir(), "daemon.lock")
}
