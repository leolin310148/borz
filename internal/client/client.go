// Package client provides the CLI-side HTTP client for communicating with the daemon.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/leolin310148/bb-browser-go/internal/config"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
)

var (
	cachedInfo  *protocol.DaemonInfo
	daemonReady bool
	useRemote   bool

	// discoverCDPPort is indirected so tests can bypass real CDP discovery.
	discoverCDPPort = DiscoverCDPPort
)

// RemoteConfig is persisted by `bb-browser client setup` and stores the server
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

// ReadRemoteConfig reads ~/.bb-browser/client.json.
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
	return &cfg, nil
}

// WriteRemoteConfig writes ~/.bb-browser/client.json with restrictive
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
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.HomeDir(), 0755); err != nil {
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
	return &RemoteConfig{URL: normalized, Token: token, Enabled: enabled}, nil
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
// routing is opt-in per invocation (bb-browser --remote ...); the persisted
// Enabled field is retained only for compatibility with older client.json files
// and client enable/disable commands.
func EnabledRemoteConfig() (*RemoteConfig, bool, error) {
	if !useRemote {
		return nil, false, nil
	}
	cfg, err := ReadRemoteConfig()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, fmt.Errorf("remote client is not configured; run 'bb-browser client setup <server-url> [--token <token>]'")
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
		return fmt.Errorf("cannot reach bb-browser server %s: %w", cfg.URL, err)
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

// ReadDaemonJSON reads ~/.bb-browser/daemon.json.
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

// httpJSONEndpoint sends an HTTP request to a bb-browser HTTP endpoint and
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
		return nil, fmt.Errorf("bb-browser HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return json.RawMessage(respBody), nil
}

// httpJSON sends an HTTP request to the local daemon and returns the raw JSON
// response.
func httpJSON(method, urlPath string, info *protocol.DaemonInfo, body interface{}, timeout time.Duration) (json.RawMessage, error) {
	return httpJSONEndpoint(method, fmt.Sprintf("http://%s:%d", info.Host, info.Port), info.Token, urlPath, body, timeout)
}

// EnsureDaemon makes sure the daemon is running and ready.
func EnsureDaemon() error {
	if daemonReady && cachedInfo != nil {
		// Quick re-check
		raw, err := httpJSON("GET", "/status", cachedInfo, nil, 2*time.Second)
		if err == nil {
			var status struct {
				Running bool `json:"running"`
			}
			json.Unmarshal(raw, &status)
			if status.Running {
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
				var status struct {
					Running bool `json:"running"`
				}
				json.Unmarshal(raw, &status)
				if status.Running {
					cachedInfo = info
					daemonReady = true
					return nil
				}
			}
		}
	}

	// Discover CDP port
	cdpInfo, err := discoverCDPPort()
	if err != nil {
		return fmt.Errorf("bb-browser: Cannot find a Chromium-based browser.\n\n" +
			"Please do one of the following:\n" +
			"  1. Install Google Chrome, Edge, or Brave\n" +
			"  2. Start Chrome with: google-chrome --remote-debugging-port=19825\n" +
			"  3. Set BB_BROWSER_CDP_URL=http://host:port")
	}

	// Spawn daemon process
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find self executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon",
		"--cdp-host", cdpInfo.Host,
		"--cdp-port", strconv.Itoa(cdpInfo.Port),
	)
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
			daemonReady = true
			return nil
		}
	}

	return fmt.Errorf("bb-browser: Daemon did not start in time.\n\n" +
		"Chrome CDP is reachable, but the daemon process failed to initialize.\n" +
		"Try: bb-browser daemon status")
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

func canConnect(host string, port int) bool {
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
	executable := findBrowserExecutable()
	if executable == "" {
		return nil, fmt.Errorf("no browser found")
	}

	userDataDir := config.ManagedUserDataDir()
	os.MkdirAll(userDataDir, 0755)

	// Write profile preferences
	defaultProfileDir := filepath.Join(userDataDir, "Default")
	os.MkdirAll(defaultProfileDir, 0755)
	prefsPath := filepath.Join(defaultProfileDir, "Preferences")
	prefs := map[string]interface{}{
		"profile": map[string]interface{}{"name": "bb-browser"},
	}
	prefsJSON, _ := json.Marshal(prefs)
	os.WriteFile(prefsPath, prefsJSON, 0644)

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
		cmd = exec.Command("/usr/bin/open", openArgs...)
	} else {
		cmd = exec.Command(executable, args...)
	}
	setDetached(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cmd.Process.Release()

	// Write port file
	os.MkdirAll(config.ManagedBrowserDir(), 0755)
	os.WriteFile(config.ManagedPortFile(), []byte(strconv.Itoa(port)), 0644)

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

// DiscoverCDPPort finds a Chrome CDP endpoint.
func DiscoverCDPPort() (*CDPEndpoint, error) {
	// Priority 1: BB_BROWSER_CDP_URL env var
	if envURL := os.Getenv("BB_BROWSER_CDP_URL"); envURL != "" {
		// Parse URL to extract host:port
		envURL = strings.TrimPrefix(envURL, "http://")
		envURL = strings.TrimPrefix(envURL, "https://")
		parts := strings.SplitN(envURL, ":", 2)
		if len(parts) == 2 {
			host := parts[0]
			portStr := strings.Split(parts[1], "/")[0]
			if port, err := strconv.Atoi(portStr); err == nil && canConnect(host, port) {
				return &CDPEndpoint{Host: host, Port: port}, nil
			}
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

	// Priority 3: Default CDP port
	if canConnect("127.0.0.1", config.DefaultCDPPort) {
		return &CDPEndpoint{Host: "127.0.0.1", Port: config.DefaultCDPPort}, nil
	}

	// Priority 4: Launch managed browser
	endpoint, err := launchManagedBrowser(config.DefaultCDPPort)
	if err == nil {
		return endpoint, nil
	}

	return nil, fmt.Errorf("no CDP endpoint found")
}
