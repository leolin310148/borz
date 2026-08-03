// Package profile resolves a profile name into the browser target it
// declares: a locally managed Chrome, an existing CDP endpoint, or a remote
// borz server. The registry lives in ~/.borz/profiles.json; an absent file
// or an unknown profile name resolves to the managed transport, i.e. the
// historical default behaviour.
package profile

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/observability"
)

// TransportKind names how a profile reaches its browser.
type TransportKind string

const (
	// TransportManaged launches and owns a Chrome under the profile's
	// runtime directory. This is the default when a profile is undeclared.
	TransportManaged TransportKind = "managed"
	// TransportCDP attaches the local daemon to an existing CDP endpoint.
	TransportCDP TransportKind = "cdp"
	// TransportRemote sends every command to a remote borz server over
	// HTTP; no local daemon or browser is involved.
	TransportRemote TransportKind = "remote"
)

// RemoteTarget is the resolved remote-server destination.
type RemoteTarget struct {
	URL   string
	Token string
}

// CDPTarget is the resolved CDP endpoint.
type CDPTarget struct {
	Host string
	Port int
}

// Target is the single resolved answer to "which browser am I driving".
type Target struct {
	Kind   TransportKind
	Remote RemoteTarget // set when Kind == TransportRemote
	CDP    CDPTarget    // set when Kind == TransportCDP
	// DaemonPort pins the local daemon listen port when non-zero. Named
	// profiles otherwise keep the historical dynamic-port behavior.
	DaemonPort int
	// DaemonToken pins the local daemon bearer token when non-empty. It is
	// stored only in the 0600 profiles registry and never rendered by list or
	// show. Empty preserves the historical rotate-on-start behavior.
	DaemonToken string
	// IdleTabTimeout is the profile's idle-tab auto-close threshold in
	// minutes (0 disables it). nil means the profile does not declare one,
	// so the flag/env/default chain decides. Never set for remote targets.
	IdleTabTimeout *int
	// MaxTabs is the profile's maximum retained page-tab count (0 disables
	// the cap). nil means the flag/env/default chain decides. Never set for
	// remote targets.
	MaxTabs *int
}

// Entry is one declared profile in profiles.json.
type Entry struct {
	Transport string `json:"transport"`
	// Description says what this profile is for in one line ("MDT VPN
	// Chrome over the SSH tunnel"). It never affects resolution; it exists
	// so 'profile list'/'show' can tell a human or an agent which browser
	// a name means instead of leaving them to guess from the name.
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	Token       string `json:"token,omitempty"`
	CDPURL      string `json:"cdpUrl,omitempty"`
	CDPHost     string `json:"cdpHost,omitempty"`
	CDPPort     int    `json:"cdpPort,omitempty"`
	// DaemonPort and DaemonToken apply only to local managed/CDP profiles.
	// Together they let the Chrome extension keep a durable endpoint across
	// daemon restarts without a hand-written service wrapper.
	DaemonPort  int    `json:"daemonPort,omitempty"`
	DaemonToken string `json:"daemonToken,omitempty"`
	// IdleTabTimeout, in minutes, overrides the daemon's idle-tab reaper for
	// managed and cdp transports (0 disables auto-close). It is invalid on
	// remote profiles: the server on the other side owns tab lifecycle.
	IdleTabTimeout *int `json:"idleTabTimeout,omitempty"`
	// MaxTabs caps retained page tabs for managed and cdp transports (0 means
	// unlimited). It is invalid on remote profiles for the same lifecycle
	// ownership reason as IdleTabTimeout.
	MaxTabs *int `json:"maxTabs,omitempty"`
}

// File is the on-disk shape of profiles.json.
type File struct {
	Version  int              `json:"version"`
	Profiles map[string]Entry `json:"profiles"`
}

// CurrentVersion is the profiles.json schema version this binary writes.
const CurrentVersion = 1

// DefaultName is the profile selected when no --profile / BORZ_PROFILE is set.
const DefaultName = "default"

// LegacyRemoteName is the profile that client.json migrates into and that the
// deprecated bare --remote flag selects.
const LegacyRemoteName = "remote"

// Normalize maps the empty profile name to DefaultName.
func Normalize(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultName
	}
	return name
}

// Load reads profiles.json, first migrating a legacy client.json when no
// registry exists yet. A missing registry yields an empty File, not an error.
func Load() (*File, error) {
	MigrateClientJSON()
	return loadWithoutMigration()
}

func loadWithoutMigration() (*File, error) {
	path := config.ProfilesJSONPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Version: CurrentVersion, Profiles: map[string]Entry{}}, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	if f.Version > CurrentVersion {
		return nil, fmt.Errorf("%s has version %d, but this borz only understands version %d — upgrade borz", path, f.Version, CurrentVersion)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]Entry{}
	}
	return &f, nil
}

// Save writes profiles.json atomically (temp file + rename) with 0600
// permissions because entries may hold bearer tokens.
func Save(f *File) error {
	if f == nil {
		return fmt.Errorf("missing profiles file")
	}
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(config.ProfilesJSONPath(), append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".profiles-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ResolveTarget resolves a profile name (empty means default) into a Target.
// Missing registry or missing entry both mean the managed transport so users
// who never touch profiles see zero change.
func ResolveTarget(name string) (Target, error) {
	name = Normalize(name)
	f, err := Load()
	if err != nil {
		return Target{}, err
	}
	entry, ok := f.Profiles[name]
	if !ok {
		return Target{Kind: TransportManaged}, nil
	}
	return ResolveEntry(name, entry)
}

// MaxDescriptionLen bounds a profile description so 'profile list' stays a
// readable one-line-per-profile table.
const MaxDescriptionLen = 200

// NormalizeDescription trims a description written through the CLI and
// rejects what would break that table. Resolution deliberately does not call
// it: a hand-edited profiles.json must never stop borz from reaching the
// browser, so reads sanitize (SanitizeDescription) instead of failing.
func NormalizeDescription(raw string) (string, error) {
	desc := strings.TrimSpace(raw)
	if desc == "" {
		return "", nil
	}
	if strings.ContainsAny(desc, "\n\r\t") {
		return "", fmt.Errorf("profile description must be a single line (no newlines or tabs)")
	}
	if len([]rune(desc)) > MaxDescriptionLen {
		return "", fmt.Errorf("profile description must be at most %d characters", MaxDescriptionLen)
	}
	return desc, nil
}

// SanitizeDescription renders a possibly hand-edited description as one line,
// so a stray newline in profiles.json cannot scramble the listing.
func SanitizeDescription(raw string) string {
	desc := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, raw))
	if runes := []rune(desc); len(runes) > MaxDescriptionLen {
		return strings.TrimSpace(string(runes[:MaxDescriptionLen-1])) + "…"
	}
	return desc
}

// ResolveEntry validates one declared entry and converts it into a Target.
func ResolveEntry(name string, entry Entry) (Target, error) {
	if entry.IdleTabTimeout != nil && *entry.IdleTabTimeout < 0 {
		return Target{}, fmt.Errorf("profile %q: idleTabTimeout must be >= 0 minutes (0 disables idle-tab auto-close)", name)
	}
	if entry.MaxTabs != nil && *entry.MaxTabs < 0 {
		return Target{}, fmt.Errorf("profile %q: maxTabs must be >= 0 (0 disables the tab cap)", name)
	}
	if entry.DaemonPort != 0 && (entry.DaemonPort < 1 || entry.DaemonPort > 65535) {
		return Target{}, fmt.Errorf("profile %q: daemonPort must be a TCP port between 1 and 65535", name)
	}
	daemonToken := strings.TrimSpace(entry.DaemonToken)
	switch TransportKind(strings.TrimSpace(entry.Transport)) {
	case TransportManaged:
		return Target{Kind: TransportManaged, DaemonPort: entry.DaemonPort, DaemonToken: daemonToken, IdleTabTimeout: entry.IdleTabTimeout, MaxTabs: entry.MaxTabs}, nil
	case TransportRemote:
		if entry.IdleTabTimeout != nil {
			return Target{}, fmt.Errorf("profile %q: idleTabTimeout does not apply to the remote transport (the server owns tab lifecycle) — remove it", name)
		}
		if entry.MaxTabs != nil {
			return Target{}, fmt.Errorf("profile %q: maxTabs does not apply to the remote transport (the server owns tab lifecycle) — remove it", name)
		}
		if entry.DaemonPort != 0 || daemonToken != "" {
			return Target{}, fmt.Errorf("profile %q: daemonPort/daemonToken do not apply to the remote transport (there is no local daemon) — remove them", name)
		}
		normalized, err := NormalizeServerURL(entry.URL)
		if err != nil {
			return Target{}, fmt.Errorf("profile %q: %w", name, err)
		}
		return Target{Kind: TransportRemote, Remote: RemoteTarget{URL: normalized, Token: strings.TrimSpace(entry.Token)}}, nil
	case TransportCDP:
		cdp, err := resolveCDPFields(entry)
		if err != nil {
			return Target{}, fmt.Errorf("profile %q: %w", name, err)
		}
		return Target{Kind: TransportCDP, CDP: cdp, DaemonPort: entry.DaemonPort, DaemonToken: daemonToken, IdleTabTimeout: entry.IdleTabTimeout, MaxTabs: entry.MaxTabs}, nil
	case "":
		return Target{}, fmt.Errorf("profile %q: missing transport (expected managed, cdp, or remote)", name)
	default:
		return Target{}, fmt.Errorf("profile %q: unknown transport %q (expected managed, cdp, or remote)", name, entry.Transport)
	}
}

func resolveCDPFields(entry Entry) (CDPTarget, error) {
	hasURL := strings.TrimSpace(entry.CDPURL) != ""
	hasHostPort := strings.TrimSpace(entry.CDPHost) != "" || entry.CDPPort != 0

	var fromURL CDPTarget
	if hasURL {
		host, port, ok := ParseCDPEndpoint(entry.CDPURL)
		if !ok {
			return CDPTarget{}, fmt.Errorf("invalid cdpUrl %q (expected http://host:port or host:port)", entry.CDPURL)
		}
		fromURL = CDPTarget{Host: host, Port: port}
	}

	var fromHostPort CDPTarget
	if hasHostPort {
		host := strings.TrimSpace(entry.CDPHost)
		if host == "" {
			host = "127.0.0.1"
		}
		if entry.CDPPort <= 0 || entry.CDPPort > 65535 {
			return CDPTarget{}, fmt.Errorf("cdpPort must be a TCP port between 1 and 65535")
		}
		fromHostPort = CDPTarget{Host: host, Port: entry.CDPPort}
	}

	switch {
	case hasURL && hasHostPort:
		if fromURL != fromHostPort {
			return CDPTarget{}, fmt.Errorf("cdpUrl (%s:%d) and cdpHost/cdpPort (%s:%d) disagree — keep one spelling", fromURL.Host, fromURL.Port, fromHostPort.Host, fromHostPort.Port)
		}
		return fromURL, nil
	case hasURL:
		return fromURL, nil
	case hasHostPort:
		return fromHostPort, nil
	default:
		return CDPTarget{}, fmt.Errorf("cdp transport requires cdpUrl or cdpHost/cdpPort")
	}
}

// MigrateClientJSON writes profiles.json from a legacy client.json exactly
// once: only when no registry exists yet and client.json declares a server
// URL. client.json is left on disk so a rollback keeps working. Best-effort:
// a malformed client.json is ignored rather than failing the command.
func MigrateClientJSON() {
	if _, err := os.Stat(config.ProfilesJSONPath()); err == nil || !os.IsNotExist(err) {
		return
	}
	data, err := os.ReadFile(config.ClientJSONPath())
	if err != nil {
		return
	}
	var legacy struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return
	}
	normalized, err := NormalizeServerURL(legacy.URL)
	if err != nil {
		return
	}
	f := &File{
		Version: CurrentVersion,
		Profiles: map[string]Entry{
			LegacyRemoteName: {Transport: string(TransportRemote), URL: normalized, Token: strings.TrimSpace(legacy.Token)},
		},
	}
	if err := Save(f); err != nil {
		return
	}
	if logger, err := observability.Open("client", ""); err == nil {
		_ = logger.Log("debug", "client_json_migrated_to_profiles", observability.Fields{})
		_ = logger.Close()
	}
}

// NormalizeServerURL validates and canonicalizes a borz server URL. A bare
// host[:port] gets an http:// scheme; query, fragment, and trailing slashes
// are stripped.
func NormalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("server URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("server URL must use http or https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("server URL must include a host")
	}
	if err := validateServerURLPort(u); err != nil {
		return "", err
	}
	if u.User != nil {
		return "", fmt.Errorf("server URL must not include user info")
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func validateServerURLPort(u *url.URL) error {
	port := u.Port()
	if port == "" {
		if hasExplicitPort(u.Host) {
			return fmt.Errorf("server URL port must be a TCP port between 1 and 65535")
		}
		return nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("server URL port must be a TCP port between 1 and 65535")
	}
	return nil
}

func hasExplicitPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		close := strings.LastIndex(host, "]")
		return close >= 0 && close+1 < len(host) && host[close+1] == ':'
	}
	return strings.Count(host, ":") == 1
}

// ParseCDPEndpoint accepts "http://host:port", "ws://host:port", or a bare
// "host:port" and returns the host and port.
func ParseCDPEndpoint(raw string) (host string, port int, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, false
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", 0, false
	}
	host = u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return "", 0, false
	}
	port, err = strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}
