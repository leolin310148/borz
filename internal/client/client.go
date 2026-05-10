// Package client provides the CLI-side HTTP client for communicating with the daemon.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

var (
	cachedInfo          *protocol.DaemonInfo
	daemonReady         bool
	useRemote           bool
	localVersion        string
	versionWarningShown bool
	cachedProfile       string

	// discoverCDPPort is indirected so tests can bypass real CDP discovery.
	discoverCDPPort = DiscoverCDPPort

	osExecutable            = os.Executable
	execCommand             = exec.Command
	browserExecutableFinder = findBrowserExecutable
	canConnect              = defaultCanConnect
)

// SetLocalVersion records the CLI version so daemon mismatches can be warned
// without changing daemon/browser lifecycle.
func SetLocalVersion(v string) { localVersion = v }

// RemoteConfig is persisted by `borz client setup` and stores the server
// used when a CLI invocation opts into remote routing with --remote.
type RemoteConfig struct {
	URL     string `json:"url"`
	Token   string `json:"token,omitempty"`
	Enabled bool   `json:"enabled"`
}

// ResetForTests clears the package's cached daemon info. Test-only —
// used by callers in other packages that swap the daemon out per-test.
func ResetForTests() {
	cachedInfo = nil
	daemonReady = false
	useRemote = false
	localVersion = ""
	versionWarningShown = false
	cachedProfile = ""
	_ = config.SetProfile("")
}

// SetRemoteRouting controls whether this process sends browser actions to the
// configured remote server. Normal CLI invocations stay local unless main
// enables this after seeing the global --remote flag.
func SetRemoteRouting(enabled bool) {
	useRemote = enabled
}

// RemoteRoutingEnabled reports whether this process is currently in explicit
// remote routing mode.
func RemoteRoutingEnabled() bool {
	return useRemote
}

// ReadRemoteConfig reads ~/.borz/client.json.
func ReadRemoteConfig() (*RemoteConfig, error) {
	data, err := os.ReadFile(config.ClientJSONPath())
	if err != nil {
		return nil, err
	}
	var cfg RemoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("invalid client.json: missing url")
	}
	normalized, err := normalizeServerURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid client.json: %w", err)
	}
	cfg.URL = normalized
	cfg.Token = strings.TrimSpace(cfg.Token)
	return &cfg, nil
}

// WriteRemoteConfig writes ~/.borz/client.json with restrictive
// permissions because it may contain a bearer token.
func WriteRemoteConfig(cfg *RemoteConfig) error {
	if cfg == nil {
		return fmt.Errorf("missing remote client config")
	}
	normalized, err := normalizeServerURL(cfg.URL)
	if err != nil {
		return err
	}
	out := *cfg
	out.URL = normalized
	out.Token = strings.TrimSpace(out.Token)
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return err
	}
	return os.WriteFile(config.ClientJSONPath(), append(data, '\n'), 0600)
}

// NewRemoteConfig builds a server URL/token pair while preserving the previous
// enabled state when a config already exists.
func NewRemoteConfig(serverURL, token string) (*RemoteConfig, error) {
	normalized, err := normalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	enabled := false
	if existing, err := ReadRemoteConfig(); err == nil && existing != nil {
		enabled = existing.Enabled
	}
	return &RemoteConfig{URL: normalized, Token: strings.TrimSpace(token), Enabled: enabled}, nil
}

// ConfigureRemote stores a server URL/token pair.
func ConfigureRemote(serverURL, token string) (*RemoteConfig, error) {
	cfg, err := NewRemoteConfig(serverURL, token)
	if err != nil {
		return nil, err
	}
	if err := WriteRemoteConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SetRemoteEnabled toggles remote client mode in the persisted config.
func SetRemoteEnabled(enabled bool) (*RemoteConfig, error) {
	cfg, err := ReadRemoteConfig()
	if err != nil {
		return nil, err
	}
	cfg.Enabled = enabled
	if err := WriteRemoteConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// EnabledRemoteConfig returns the active remote config for this process. Remote
// routing is opt-in per invocation (borz --remote ...); the persisted
// Enabled field is retained only for compatibility with older client.json files
// and client enable/disable commands.
func EnabledRemoteConfig() (*RemoteConfig, bool, error) {
	if !useRemote {
		return nil, false, nil
	}
	cfg, err := ReadRemoteConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, fmt.Errorf("remote client is not configured; run 'borz client setup <server-url> [--token <token>]'")
		}
		return nil, false, err
	}
	return cfg, true, nil
}

// CheckRemoteConfig verifies the configured server is reachable and that the
// token (if any) is accepted by an authenticated endpoint.
func CheckRemoteConfig(cfg *RemoteConfig, timeout time.Duration) error {
	if cfg == nil {
		return fmt.Errorf("missing remote client config")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if _, err := httpJSONEndpoint("GET", cfg.URL, cfg.Token, "/status", nil, timeout); err != nil {
		return fmt.Errorf("cannot reach borz server %s: %w", cfg.URL, err)
	}
	return nil
}

func normalizeServerURL(raw string) (string, error) {
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
	if u.Host == "" {
		return "", fmt.Errorf("server URL must include a host")
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

// ReadDaemonJSON reads ~/.borz/daemon.json.
func ReadDaemonJSON() (*protocol.DaemonInfo, error) {
	data, err := os.ReadFile(config.DaemonJSONPath())
	if err != nil {
		return nil, err
	}
	var info protocol.DaemonInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.PID == 0 || info.Host == "" || info.Port == 0 {
		return nil, fmt.Errorf("invalid daemon.json")
	}
	return &info, nil
}

// IsProcessAlive checks if a PID is still running.
func IsProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// httpJSONEndpoint sends an HTTP request to a borz HTTP endpoint and
// returns the raw JSON response.
func httpJSONEndpoint(method, baseURL, token, urlPath string, body interface{}, timeout time.Duration) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := strings.TrimRight(baseURL, "/") + urlPath
	req, err := http.NewRequest(method, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, formatHTTPError(resp.StatusCode, resp.Status, respBody)
	}

	return json.RawMessage(respBody), nil
}

func formatHTTPError(statusCode int, status string, body []byte) error {
	message := strings.TrimSpace(string(body))
	if extracted := jsonErrorMessage(body); extracted != "" {
		message = extracted
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if message == "" {
		message = status
	}
	return fmt.Errorf("borz HTTP %d: %s", statusCode, message)
}

func jsonErrorMessage(body []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if message := strings.TrimSpace(payload.Error); message != "" {
		return message
	}
	return strings.TrimSpace(payload.Message)
}

// httpJSON sends an HTTP request to the local daemon and returns the raw JSON
// response.
func httpJSON(method, urlPath string, info *protocol.DaemonInfo, body interface{}, timeout time.Duration) (json.RawMessage, error) {
	return httpJSONEndpoint(method, fmt.Sprintf("http://%s:%d", info.Host, info.Port), info.Token, urlPath, body, timeout)
}

func warnDaemonVersionMismatch(daemonVersion string) {
	if versionWarningShown || localVersion == "" || daemonVersion == "" || daemonVersion == localVersion {
		return
	}
	versionWarningShown = true
	fmt.Fprintf(os.Stderr, "Warning: borz daemon is version %s, but CLI is version %s; run `borz daemon stop` to refresh when convenient.\n", daemonVersion, localVersion)
}

// EnsureDaemon makes sure the daemon is running and ready.
func EnsureDaemon() error {
	if cachedProfile != config.Profile() {
		daemonReady = false
		cachedInfo = nil
		cachedProfile = config.Profile()
	}
	if daemonReady && cachedInfo != nil {
		// Quick re-check
		raw, err := httpJSON("GET", "/status", cachedInfo, nil, 2*time.Second)
		if err == nil {
			var status protocol.DaemonStatus
			json.Unmarshal(raw, &status)
			if status.Running {
				warnDaemonVersionMismatch(status.Version)
				return nil
			}
		}
		daemonReady = false
		cachedInfo = nil
	}

	// Try reading existing daemon.json
	info, err := ReadDaemonJSON()
	if err == nil && info != nil {
		if !IsProcessAlive(info.PID) {
			os.Remove(config.DaemonJSONPath())
			info = nil
		} else {
			raw, err := httpJSON("GET", "/status", info, nil, 2*time.Second)
			if err == nil {
				var status protocol.DaemonStatus
				json.Unmarshal(raw, &status)
				if status.Running {
					warnDaemonVersionMismatch(status.Version)
					cachedInfo = info
					cachedProfile = config.Profile()
					daemonReady = true
					return nil
				}
			}
		}
	}

	// Discover CDP port
	cdpInfo, err := discoverCDPPort()
	if err != nil {
		return fmt.Errorf("borz: Cannot find a Chromium-based browser.\n\n" +
			"Please do one of the following:\n" +
			"  1. Install Google Chrome, Edge, or Brave\n" +
			"  2. Start Chrome with: google-chrome --remote-debugging-port=19825\n" +
			"  3. Set BORZ_CDP_URL=http://host:port")
	}

	// Spawn daemon process
	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot find self executable: %w", err)
	}

	daemonPort, err := daemonPortForProfile()
	if err != nil {
		return err
	}
	args := []string{"daemon",
		"--cdp-host", cdpInfo.Host,
		"--cdp-port", strconv.Itoa(cdpInfo.Port),
	}
	if config.Profile() != "" {
		args = append(args, "--profile", config.Profile(), "--port", strconv.Itoa(daemonPort))
	}
	cmd := execCommand(exe, args...)
	setDetached(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}
	cmd.Process.Release()

	// Wait for daemon to become healthy
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		info, err = ReadDaemonJSON()
		if err != nil || info == nil {
			continue
		}
		raw, err := httpJSON("GET", "/status", info, nil, 2*time.Second)
		if err != nil {
			continue
		}
		var status struct {
			Running bool `json:"running"`
		}
		json.Unmarshal(raw, &status)
		if status.Running {
			cachedInfo = info
			cachedProfile = config.Profile()
			daemonReady = true
			return nil
		}
	}

	return fmt.Errorf("borz: Daemon did not start in time.\n\n" +
		"Chrome CDP is reachable, but the daemon process failed to initialize.\n" +
		"Try: borz daemon status")
}

// SendCommand sends a command to the daemon.
func SendCommand(req *protocol.Request) (*protocol.Response, error) {
	if cfg, enabled, err := EnabledRemoteConfig(); err != nil {
		return nil, err
	} else if enabled {
		raw, err := httpJSONEndpoint("POST", cfg.URL, cfg.Token, "/command", req, time.Duration(config.CommandTimeout)*time.Second)
		if err != nil {
			return nil, err
		}
		var resp protocol.Response
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("invalid response from remote server: %w", err)
		}
		return &resp, nil
	}

	if err := EnsureDaemon(); err != nil {
		return nil, err
	}
	if cachedInfo == nil {
		info, err := ReadDaemonJSON()
		if err != nil {
			return nil, fmt.Errorf("no daemon.json found. Is the daemon running?")
		}
		cachedInfo = info
	}

	raw, err := httpJSON("POST", "/command", cachedInfo, req, time.Duration(config.CommandTimeout)*time.Second)
	if err != nil {
		return nil, err
	}

	var resp protocol.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("invalid response from daemon: %w", err)
	}
	return &resp, nil
}

// StopDaemon stops the daemon.
func StopDaemon() error {
	info := cachedInfo
	if info == nil {
		var err error
		info, err = ReadDaemonJSON()
		if err != nil || info == nil {
			return fmt.Errorf("daemon is not running")
		}
	}
	_, err := httpJSON("POST", "/shutdown", info, nil, 5*time.Second)
	daemonReady = false
	cachedInfo = nil
	return err
}

// GetJSON calls a GET endpoint on the daemon and returns the raw response body.
// Used by REST endpoints that don't fit the /command protocol (e.g. /v1/cookies/all
// served by the extension bridge).
func GetJSON(path string, timeout time.Duration) (json.RawMessage, error) {
	if cfg, enabled, err := EnabledRemoteConfig(); err != nil {
		return nil, err
	} else if enabled {
		return httpJSONEndpoint("GET", cfg.URL, cfg.Token, path, nil, timeout)
	}

	if err := EnsureDaemon(); err != nil {
		return nil, err
	}
	if cachedInfo == nil {
		info, err := ReadDaemonJSON()
		if err != nil {
			return nil, fmt.Errorf("no daemon.json found. Is the daemon running?")
		}
		cachedInfo = info
	}
	return httpJSON("GET", path, cachedInfo, nil, timeout)
}

// PostJSON calls a POST endpoint on the daemon and returns the raw response body.
// Used by REST endpoints that don't fit the /command protocol.
func PostJSON(path string, body interface{}, timeout time.Duration) (json.RawMessage, error) {
	if cfg, enabled, err := EnabledRemoteConfig(); err != nil {
		return nil, err
	} else if enabled {
		return httpJSONEndpoint("POST", cfg.URL, cfg.Token, path, body, timeout)
	}

	if err := EnsureDaemon(); err != nil {
		return nil, err
	}
	if cachedInfo == nil {
		info, err := ReadDaemonJSON()
		if err != nil {
			return nil, fmt.Errorf("no daemon.json found. Is the daemon running?")
		}
		cachedInfo = info
	}
	return httpJSON("POST", path, cachedInfo, body, timeout)
}

// GetDaemonStatus returns the daemon status.
func GetDaemonStatus() (json.RawMessage, error) {
	if cfg, enabled, err := EnabledRemoteConfig(); err != nil {
		return nil, err
	} else if enabled {
		return httpJSONEndpoint("GET", cfg.URL, cfg.Token, "/status", nil, 2*time.Second)
	}
	return GetLocalDaemonStatus()
}

// GetLocalDaemonStatus returns the local daemon/server status, ignoring remote
// client mode. Lifecycle commands use this so client mode never controls the
// remote server process by accident.
func GetLocalDaemonStatus() (json.RawMessage, error) {
	info := cachedInfo
	if info == nil {
		var err error
		info, err = ReadDaemonJSON()
		if err != nil || info == nil {
			return nil, fmt.Errorf("daemon is not running")
		}
	}
	return httpJSON("GET", "/status", info, nil, 2*time.Second)
}

// --- CDP Discovery ---

// CDPEndpoint holds host:port for a CDP connection.
type CDPEndpoint struct {
	Host string
	Port int
}

func defaultCanConnect(host string, port int) bool {
	url := fmt.Sprintf("http://%s:%d/json/version", host, port)
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// findBrowserExecutable is implemented per-platform in browser_*.go.

func launchManagedBrowser(port int) (*CDPEndpoint, error) {
	executable := browserExecutableFinder()
	if executable == "" {
		return nil, fmt.Errorf("no browser found")
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return nil, err
	}

	userDataDir := config.ManagedUserDataDir()
	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare managed browser profile: %w", err)
	}

	// Write profile preferences
	defaultProfileDir := filepath.Join(userDataDir, "Default")
	if err := os.MkdirAll(defaultProfileDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare managed browser default profile: %w", err)
	}
	prefsPath := filepath.Join(defaultProfileDir, "Preferences")
	profileName := config.Profile()
	if profileName == "" {
		profileName = "borz"
	}
	prefs := map[string]interface{}{
		"profile": map[string]interface{}{"name": profileName},
	}
	prefsJSON, err := json.Marshal(prefs)
	if err != nil {
		return nil, fmt.Errorf("encode managed browser preferences: %w", err)
	}
	if err := os.WriteFile(prefsPath, prefsJSON, 0o644); err != nil {
		return nil, fmt.Errorf("write managed browser preferences: %w", err)
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-features=Translate,MediaRouter",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
		"about:blank",
	}

	// On macOS, launching the inner Mach-O binary directly bypasses
	// LaunchServices, so the window never becomes key — physical keyboard
	// input (address bar, Cmd+T, typing) is dropped. Go through `open -n -a`
	// to get proper app activation.
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" && strings.Contains(executable, ".app/Contents/MacOS/") {
		appPath := executable[:strings.Index(executable, ".app/Contents/MacOS/")+len(".app")]
		openArgs := append([]string{"-n", "-a", appPath, "--args"}, args...)
		cmd = execCommand("/usr/bin/open", openArgs...)
	} else {
		cmd = execCommand(executable, args...)
	}
	setDetached(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cmd.Process.Release()

	// Write port file
	if err := os.MkdirAll(config.ManagedBrowserDir(), 0o755); err != nil {
		return nil, fmt.Errorf("prepare managed browser directory: %w", err)
	}
	if err := os.WriteFile(config.ManagedPortFile(), []byte(strconv.Itoa(port)), 0o644); err != nil {
		return nil, fmt.Errorf("write managed browser port file: %w", err)
	}

	// Wait for browser to become reachable
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if canConnect("127.0.0.1", port) {
			return &CDPEndpoint{Host: "127.0.0.1", Port: port}, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, fmt.Errorf("browser did not start in time")
}

func freeTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, err
	}
	return port, nil
}

func daemonPortForProfile() (int, error) {
	if config.Profile() == "" {
		return config.DaemonPort, nil
	}
	port, err := freeTCPPort()
	if err != nil {
		return 0, fmt.Errorf("choose daemon port for profile %q: %w", config.Profile(), err)
	}
	return port, nil
}

// DaemonPortForProfile returns the daemon listen port to use when starting a
// new local daemon for the selected profile.
func DaemonPortForProfile() (int, error) {
	return daemonPortForProfile()
}

func cdpPortForProfile() (int, error) {
	if config.Profile() == "" {
		return config.DefaultCDPPort, nil
	}
	port, err := freeTCPPort()
	if err != nil {
		return 0, fmt.Errorf("choose CDP port for profile %q: %w", config.Profile(), err)
	}
	return port, nil
}

// DiscoverCDPPort finds a Chrome CDP endpoint.
func DiscoverCDPPort() (*CDPEndpoint, error) {
	// Priority 1: BORZ_CDP_URL env var (legacy BB_BROWSER_CDP_URL supported).
	if envURL := config.Env("BORZ_CDP_URL", "BB_BROWSER_CDP_URL"); envURL != "" {
		if host, port, ok := parseCDPEndpointURL(envURL); ok && canConnect(host, port) {
			return &CDPEndpoint{Host: host, Port: port}, nil
		}
	}

	// Priority 2: Managed browser port file
	if data, err := os.ReadFile(config.ManagedPortFile()); err == nil {
		if port, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && port > 0 {
			if canConnect("127.0.0.1", port) {
				return &CDPEndpoint{Host: "127.0.0.1", Port: port}, nil
			}
		}
	}

	// Priority 3: Default CDP port. Named profiles intentionally skip this
	// fallback so they don't attach to the default browser session.
	if config.Profile() == "" && canConnect("127.0.0.1", config.DefaultCDPPort) {
		return &CDPEndpoint{Host: "127.0.0.1", Port: config.DefaultCDPPort}, nil
	}

	// Priority 4: Launch managed browser
	cdpPort, err := cdpPortForProfile()
	if err != nil {
		return nil, err
	}
	endpoint, err := launchManagedBrowser(cdpPort)
	if err == nil {
		return endpoint, nil
	}

	return nil, fmt.Errorf("no CDP endpoint found")
}

func parseCDPEndpointURL(raw string) (host string, port int, ok bool) {
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
