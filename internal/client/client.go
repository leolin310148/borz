// Package client provides the CLI-side HTTP client for communicating with the daemon.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/observability"
	"github.com/leolin310148/borz/internal/processlock"
	"github.com/leolin310148/borz/internal/profile"
	"github.com/leolin310148/borz/internal/protocol"
)

var (
	cachedInfo          *protocol.DaemonInfo
	daemonReady         bool
	legacyRemoteFlag    bool
	localVersion        string
	clientSurface       = "cli"
	clientSessionID     string
	versionWarningShown bool
	cachedProfile       string

	// resolvedTarget memoizes profile.ResolveTarget for the active profile.
	resolvedTarget        *profile.Target
	resolvedTargetProfile string
	resolvedTargetMu      sync.Mutex
	ensureDaemonMu        sync.Mutex

	// discoverCDPPort is indirected so tests can bypass real CDP discovery.
	discoverCDPPort            = DiscoverCDPPort
	launchManagedBrowserAtPort = launchManagedBrowser

	osExecutable            = os.Executable
	execCommand             = exec.Command
	browserExecutableFinder = findBrowserExecutable
	canConnect              = defaultCanConnect
	readBrowserID           = readCDPBrowserID
	probeStartupDaemonPort  = probeDaemonPort
)

// SetLocalVersion records the CLI version so daemon mismatches can be warned
// without changing daemon/browser lifecycle.
func SetLocalVersion(v string) { localVersion = v }

// SetRequestContext identifies the calling surface and agent session on
// daemon requests. These values are correlation metadata only.
func SetRequestContext(surface, sessionID string) {
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "cli"
	}
	clientSurface = surface
	clientSessionID = strings.TrimSpace(sessionID)
}

// RemoteConfig is the legacy ~/.borz/client.json shape, kept for reading old
// installs and for the deprecated `borz client` commands. New configuration
// lives in profiles.json (internal/profile).
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
	legacyRemoteFlag = false
	localVersion = ""
	clientSurface = "cli"
	clientSessionID = ""
	versionWarningShown = false
	cachedProfile = ""
	resolvedTarget = nil
	resolvedTargetProfile = ""
	probeStartupDaemonPort = probeDaemonPort
	_ = config.SetProfile("")
}

// SetLegacyRemoteFlag records that the deprecated --remote flag was passed.
// The flag no longer routes anything by itself — main maps it to the
// "remote" profile — but it makes an unconfigured remote profile a hard
// error instead of silently running the managed transport.
func SetLegacyRemoteFlag(enabled bool) {
	legacyRemoteFlag = enabled
}

// ActiveTarget resolves the selected profile into its declared browser
// target. The result is memoized per profile name; ResetForTests clears it.
func ActiveTarget() (profile.Target, error) {
	resolvedTargetMu.Lock()
	defer resolvedTargetMu.Unlock()
	name := profile.Normalize(config.Profile())
	if resolvedTarget != nil && resolvedTargetProfile == name {
		return *resolvedTarget, nil
	}
	target, err := profile.ResolveTarget(name)
	if err != nil {
		return profile.Target{}, err
	}
	resolvedTarget = &target
	resolvedTargetProfile = name
	return target, nil
}

// RemoteRoutingEnabled reports whether browser commands from this process go
// to a remote borz server, i.e. the active profile declares the remote
// transport. Resolution errors read as "not remote".
func RemoteRoutingEnabled() bool {
	target, err := ActiveTarget()
	return err == nil && target.Kind == profile.TransportRemote
}

// ReadRemoteConfig reads the legacy ~/.borz/client.json.
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
	normalized, err := profile.NormalizeServerURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid client.json: %w", err)
	}
	cfg.URL = normalized
	cfg.Token = strings.TrimSpace(cfg.Token)
	return &cfg, nil
}

// WriteRemoteConfig writes the legacy ~/.borz/client.json with restrictive
// permissions because it may contain a bearer token. Only tests and rollback
// tooling should still need this; new config goes to profiles.json.
func WriteRemoteConfig(cfg *RemoteConfig) error {
	if cfg == nil {
		return fmt.Errorf("missing remote client config")
	}
	normalized, err := profile.NormalizeServerURL(cfg.URL)
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

// EnabledRemoteConfig returns the remote server to talk to when the active
// profile declares the remote transport. With the deprecated --remote flag
// set, an unresolved remote profile is a configuration error rather than a
// silent fallthrough to the local daemon.
func EnabledRemoteConfig() (*RemoteConfig, bool, error) {
	target, err := ActiveTarget()
	if err != nil {
		return nil, false, err
	}
	if target.Kind == profile.TransportRemote {
		return &RemoteConfig{URL: target.Remote.URL, Token: target.Remote.Token}, true, nil
	}
	if legacyRemoteFlag {
		return nil, false, fmt.Errorf("remote server is not configured; run 'borz profile add %s --remote <server-url> [--token <token>]' (or the deprecated 'borz client setup <server-url>')", profile.LegacyRemoteName)
	}
	return nil, false, nil
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
	if info.PID <= 0 || info.Host == "" || info.Port <= 0 || info.Port > 65535 {
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
	// POSIX signal(0) returns EPERM when the process exists but the caller is
	// not allowed to inspect it. Restricted agent sandboxes commonly impose
	// exactly that policy, so EPERM is evidence of liveness rather than death.
	return err == nil || errors.Is(err, syscall.EPERM)
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
	req.Header.Set("X-Borz-Surface", clientSurface)
	if clientSessionID != "" {
		req.Header.Set("X-Borz-Session-ID", clientSessionID)
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
	fmt.Fprintf(os.Stderr, "Warning: borz daemon is version %s, but CLI is version %s; run `borz daemon restart` to refresh it without closing managed Chrome.\n", daemonVersion, localVersion)
}

func daemonVersionMatches(daemonVersion string) bool {
	return localVersion == "" || daemonVersion == "" || localVersion == daemonVersion
}

// recoverDisconnectedDaemon starts the managed browser on the exact endpoint
// already configured in a running daemon. This avoids the stuck state where
// the daemon survives a browser exit but EnsureDaemon previously treated the
// daemon's HTTP liveness as sufficient.
//
// Recovery is deliberately limited to known local managed-browser endpoints.
// A remote/custom CDP endpoint remains under the caller's control, and a
// daemon version mismatch stays warning-only without changing lifecycle.
func recoverDisconnectedDaemon(status protocol.DaemonStatus) error {
	if status.CDPPort <= 0 || status.CDPPort > 65535 || !daemonVersionMatches(status.Version) {
		return nil
	}

	host := strings.TrimSpace(status.CDPHost)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil
	}

	managedPort, managedEndpoint := recordedManagedPort()
	managedEndpoint = managedEndpoint && managedPort == status.CDPPort
	if !managedEndpoint {
		return nil
	}
	if canConnect(host, status.CDPPort) {
		if err := verifyManagedEndpoint(status.CDPPort, true); err != nil {
			return fmt.Errorf("borz: managed Chrome identity check failed at %s:%d: %w", host, status.CDPPort, err)
		}
		return nil
	}
	// A live daemon WebSocket is stronger evidence than a transient failure of
	// the HTTP version endpoint. Never launch a replacement while it is still
	// connected.
	if status.CDPConnected {
		return nil
	}

	started := time.Now()
	logClientEvent("info", "browser_recovery_started", observability.Fields{})
	if _, err := launchManagedBrowserAtPort(status.CDPPort); err != nil {
		logClientEvent("warn", "browser_recovery_failed", observability.Fields{
			DurationMS: time.Since(started).Milliseconds(), ErrorCode: "browser_not_found",
		})
		return fmt.Errorf("borz: running daemon lost Chrome at %s:%d and managed browser recovery failed: %w", host, status.CDPPort, err)
	}
	logClientEvent("info", "browser_recovery_completed", observability.Fields{
		DurationMS: time.Since(started).Milliseconds(),
	})
	return nil
}

func adoptRunningDaemon(info *protocol.DaemonInfo, status protocol.DaemonStatus) error {
	warnDaemonVersionMismatch(status.Version)
	cachedInfo = info
	cachedProfile = config.Profile()
	daemonReady = true
	return recoverDisconnectedDaemon(status)
}

// tryAdoptRunningDaemon is the read-only fast path for healthy daemons. It
// deliberately runs before EnsureRuntimeDir and the startup lock so commands
// can connect to an existing daemon when its runtime directory is readable but
// not writable (for example inside a restricted agent workspace).
func tryAdoptRunningDaemon() (bool, error) {
	if daemonReady && cachedInfo != nil {
		raw, err := httpJSON("GET", "/status", cachedInfo, nil, 2*time.Second)
		if err == nil {
			var status protocol.DaemonStatus
			json.Unmarshal(raw, &status)
			if status.Running {
				return true, adoptRunningDaemon(cachedInfo, status)
			}
		}
		daemonReady = false
		cachedInfo = nil
	}

	info, err := ReadDaemonJSON()
	if err != nil || info == nil {
		return false, nil
	}
	raw, err := httpJSON("GET", "/status", info, nil, 2*time.Second)
	if err != nil {
		return false, nil
	}
	var status protocol.DaemonStatus
	json.Unmarshal(raw, &status)
	if !status.Running {
		return false, nil
	}
	return true, adoptRunningDaemon(info, status)
}

// EnsureDaemon makes sure the daemon is running and ready.
func EnsureDaemon() error {
	ensureDaemonMu.Lock()
	defer ensureDaemonMu.Unlock()

	target, err := ActiveTarget()
	if err != nil {
		return err
	}
	if target.Kind == profile.TransportRemote {
		return fmt.Errorf("profile %q is a remote profile (%s); there is no local daemon", profile.Normalize(config.Profile()), target.Remote.URL)
	}

	if cachedProfile != config.Profile() {
		daemonReady = false
		cachedInfo = nil
		cachedProfile = config.Profile()
	}
	if adopted, err := tryAdoptRunningDaemon(); adopted {
		return err
	}

	if _, err := config.EnsureRuntimeDir(); err != nil {
		return err
	}
	startupLock, err := processlock.Acquire(config.StartupLockPath(), 15*time.Second)
	if err != nil {
		return fmt.Errorf("serialize profile %q startup: %w", profile.Normalize(config.Profile()), err)
	}
	defer startupLock.Release()

	// Another CLI may have started the daemon while this process waited for
	// the cross-process lock. Re-run the read-only adoption path before doing
	// any discovery or spawning.
	if adopted, err := tryAdoptRunningDaemon(); adopted {
		return err
	}

	// A stale daemon.json is deliberately left alone. The startup lock only
	// serializes CLI processes, not daemons, so between reading the file and
	// deleting it a daemon can publish a fresh record — and deleting that
	// leaves a live daemon holding the port with no way for anyone to address
	// it, which is unrecoverable until the daemon dies. daemon.json has exactly
	// one writer, the daemon that holds the daemon lock, and a starting daemon
	// overwrites whatever a crashed predecessor left behind. Everything below
	// re-reads it; a dead record just fails /status and is replaced.

	// Resolve the CDP endpoint: a cdp profile pins it declaratively; the
	// managed transport keeps today's discovery (env var, port file,
	// default port, then launching a managed browser).
	autostartStarted := time.Now()
	logClientEvent("info", "daemon_autostart_started", observability.Fields{})
	var cdpInfo *CDPEndpoint
	if target.Kind == profile.TransportCDP {
		if !canConnect(target.CDP.Host, target.CDP.Port) {
			logClientEvent("warn", "cdp_endpoint_unreachable", observability.Fields{
				DurationMS: time.Since(autostartStarted).Milliseconds(), ErrorCode: "cdp_unreachable",
			})
			return fmt.Errorf("borz: profile %q: CDP endpoint http://%s:%d is unreachable.\n\n"+
				"This profile attaches to an existing browser, so borz will not launch one.\n"+
				"Start the browser (or the tunnel) behind that endpoint, or fix the profile\n"+
				"with 'borz profile set %s --cdp <host:port>'.",
				profile.Normalize(config.Profile()), target.CDP.Host, target.CDP.Port, profile.Normalize(config.Profile()))
		}
		cdpInfo = &CDPEndpoint{Host: target.CDP.Host, Port: target.CDP.Port}
	} else {
		cdpInfo, err = discoverCDPPort()
		if err != nil {
			logClientEvent("warn", "browser_discovery_failed", observability.Fields{
				DurationMS: time.Since(autostartStarted).Milliseconds(), ErrorCode: "browser_not_found",
			})
			return fmt.Errorf("borz: Cannot find a Chromium-based browser.\n\n" +
				"Please do one of the following:\n" +
				"  1. Install Google Chrome, Edge, or Brave\n" +
				"  2. Start Chrome with: google-chrome --remote-debugging-port=19825\n" +
				"  3. Set BORZ_CDP_URL=http://host:port")
		}
	}

	// Spawn daemon process
	exe, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot find self executable: %w", err)
	}

	daemonPort, err := daemonPortForTarget(target)
	if err != nil {
		return err
	}
	// If daemon metadata is missing but a borz daemon still owns the default
	// port, a spawn cannot possibly succeed. Diagnose it before launching a
	// doomed child and waiting the full readiness timeout.
	if squatter, ok := probeStartupDaemonPort(daemonPort); ok {
		// Logged, not just returned: this is the one autostart outcome that
		// used to leave no trace at all, so the state that causes it — a live
		// daemon whose daemon.json went missing — was invisible after the fact.
		logClientEvent("warn", "daemon_autostart_failed", observability.Fields{
			DurationMS: time.Since(autostartStarted).Milliseconds(),
			Success:    clientBoolPtr(false), ErrorCode: "daemon_unaddressable",
		})
		return unaddressableDaemonError(squatter, daemonPort)
	}
	args := []string{"daemon",
		"--cdp-host", cdpInfo.Host,
		"--cdp-port", strconv.Itoa(cdpInfo.Port),
	}
	if cdpInfo.OwnedByBorz {
		args = append(args, "--close-owned-browser")
	}
	// The profile's idleTabTimeout rides along so an auto-spawned daemon
	// honours it without a hand-crafted service definition. The env var
	// outranks the profile and the daemon inherits our environment, so the
	// flag is omitted whenever the env is set — the daemon resolves it.
	if target.IdleTabTimeout != nil && config.Env("BORZ_TAB_IDLE_TIMEOUT", "BB_BROWSER_TAB_IDLE_TIMEOUT") == "" {
		args = append(args, "--idle-tab-timeout", strconv.Itoa(*target.IdleTabTimeout))
	}
	if target.MaxTabs != nil && config.Env("BORZ_MAX_TABS", "BB_BROWSER_MAX_TABS") == "" {
		args = append(args, "--max-tabs", strconv.Itoa(*target.MaxTabs))
	}
	if config.Profile() != "" {
		args = append(args, "--profile", config.Profile(), "--port", strconv.Itoa(daemonPort))
	}
	cmd := execCommand(exe, args...)
	setDetached(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		logClientEvent("warn", "daemon_autostart_failed", observability.Fields{
			DurationMS: time.Since(autostartStarted).Milliseconds(), ErrorCode: "daemon_start_failed",
		})
		return fmt.Errorf("failed to start daemon: %w", err)
	}
	cmd.Process.Release()

	// Wait for daemon to become healthy
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		info, err := ReadDaemonJSON()
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
			logClientEvent("info", "daemon_autostarted", observability.Fields{
				DurationMS: time.Since(autostartStarted).Milliseconds(), Success: clientBoolPtr(true),
			})
			return nil
		}
	}

	logClientEvent("warn", "daemon_autostart_failed", observability.Fields{
		DurationMS: time.Since(autostartStarted).Milliseconds(), Success: clientBoolPtr(false), ErrorCode: "daemon_start_failed",
	})
	// A daemon we cannot address — its daemon.json is gone, so we have no
	// token — still holds the port, and every spawn we just tried died on
	// "address already in use". Say so instead of blaming the browser.
	if squatter, ok := probeStartupDaemonPort(daemonPort); ok {
		return unaddressableDaemonError(squatter, daemonPort)
	}
	return fmt.Errorf("borz: Daemon did not start in time.\n\n" +
		"Chrome CDP is reachable, but the daemon process failed to initialize.\n" +
		"Try: borz daemon status")
}

// daemonPortSquatter is an unaddressable borz daemon found on the port we
// wanted: it answers /healthz (the one unauthenticated route) but we hold no
// token for it because daemon.json is gone.
type daemonPortSquatter struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

func unaddressableDaemonError(squatter daemonPortSquatter, port int) error {
	return fmt.Errorf("borz: Daemon metadata is unavailable.\n\n"+
		"A borz daemon (%s) is already listening on 127.0.0.1:%d, but %s is\n"+
		"missing, so borz has no token to talk to it and cannot start a replacement\n"+
		"on the same port. This usually means a daemon from an older borz is still\n"+
		"running.\n\n"+
		"Fix: %s, then re-run this command.",
		squatter.describe(), port, config.DaemonJSONPath(), squatter.stopHint())
}

func (d daemonPortSquatter) describe() string {
	switch {
	case d.Version != "" && d.PID > 0:
		return fmt.Sprintf("version %s, pid %d", d.Version, d.PID)
	case d.PID > 0:
		return fmt.Sprintf("pid %d", d.PID)
	case d.Version != "":
		return "version " + d.Version
	default:
		return "unknown version"
	}
}

func (d daemonPortSquatter) stopHint() string {
	if d.PID > 0 {
		return "run 'borz daemon restart' to replace only the daemon while preserving managed Chrome"
	}
	return "stop that process, then re-run this command"
}

// probeDaemonPort asks a port whether a borz daemon is listening. Only /healthz
// is unauthenticated, and it reveals pid/version to loopback callers exactly so
// this diagnosis is possible.
func probeDaemonPort(port int) (daemonPortSquatter, bool) {
	var found daemonPortSquatter
	httpClient := &http.Client{Timeout: 2 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return found, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return found, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return found, false
	}
	var payload struct {
		OK bool `json:"ok"`
		daemonPortSquatter
	}
	if err := json.Unmarshal(body, &payload); err != nil || !payload.OK {
		return found, false
	}
	return payload.daemonPortSquatter, true
}

func logClientEvent(level, event string, fields observability.Fields) {
	logger, err := observability.Open("client", localVersion)
	if err != nil {
		return
	}
	fields.Surface = clientSurface
	fields.SessionID = clientSessionID
	_ = logger.Log(level, event, fields)
	_ = logger.Close()
}

func clientBoolPtr(v bool) *bool { return &v }

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

// ReadDaemonJSONFor reads another profile's daemon.json without disturbing the
// active profile. Returns nil (no error) when the file is absent or unusable,
// which is the ordinary "that profile has no daemon" case.
func ReadDaemonJSONFor(profileName string) *protocol.DaemonInfo {
	data, err := os.ReadFile(config.DaemonJSONPathFor(profileName))
	if err != nil {
		return nil
	}
	var info protocol.DaemonInfo
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	if info.PID <= 0 || info.Host == "" || info.Port <= 0 || info.Port > 65535 {
		return nil
	}
	return &info
}

// StopDaemonAt shuts down the daemon described by info. Unlike StopDaemon it
// takes the target explicitly and touches no package-level state, so it can
// stop a daemon belonging to a profile other than the active one.
func StopDaemonAt(info *protocol.DaemonInfo) error {
	if info == nil {
		return fmt.Errorf("daemon is not running")
	}
	_, err := httpJSON("POST", "/shutdown", info, nil, 5*time.Second)
	return err
}

// WaitForProcessExit polls IsProcessAlive at 50ms intervals until the process
// exits or the timeout elapses. Returns true if the process is gone.
func WaitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !IsProcessAlive(pid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return !IsProcessAlive(pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// KillDaemon forcibly terminates the daemon process at pid (SIGKILL on Unix,
// TerminateProcess on Windows) and removes a stale daemon.json. Used as a
// fallback when /shutdown returns OK but the daemon does not actually exit —
// e.g. when wedged on a long-running streaming response during `borz update`.
func KillDaemon(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil {
		return err
	}
	WaitForProcessExit(pid, 2*time.Second)
	// Only clear the record if it still names the daemon we just killed. A
	// successor may already have published its own, and deleting that would
	// strand it: alive, holding the port, addressable by nobody.
	if info, err := ReadDaemonJSON(); err == nil && info != nil && info.PID == pid {
		os.Remove(config.DaemonJSONPath())
	}
	daemonReady = false
	cachedInfo = nil
	return nil
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

// GetJSONForProfile calls a GET endpoint for an explicitly named profile
// without changing package-global selection and without auto-starting a local
// daemon. It is used by aggregate diagnostics where observing an offline
// profile must not launch Chrome as a side effect.
func GetJSONForProfile(profileName, path string, timeout time.Duration) (json.RawMessage, error) {
	profileName = profile.Normalize(profileName)
	target, err := profile.ResolveTarget(profileName)
	if err != nil {
		return nil, err
	}
	if target.Kind == profile.TransportRemote {
		return httpJSONEndpoint("GET", target.Remote.URL, target.Remote.Token, path, nil, timeout)
	}
	info := ReadDaemonJSONFor(profileName)
	if info == nil {
		return nil, fmt.Errorf("daemon is not running")
	}
	return httpJSON("GET", path, info, nil, timeout)
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
	Host        string
	Port        int
	OwnedByBorz bool // true only for a browser launched or previously recorded by borz
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

// LaunchManagedBrowser starts the managed browser on the given CDP port and
// waits until its endpoint is reachable. It exists for the daemon-side ensure
// hook (`borz server --ensure-browser`), which main wires up as a callback so
// the daemon package never imports client.
func LaunchManagedBrowser(port int) error {
	_, err := launchManagedBrowserAtPort(port)
	return err
}

func launchManagedBrowser(port int) (*CDPEndpoint, error) {
	executable := browserExecutableFinder()
	if executable == "" {
		return nil, fmt.Errorf("no browser found")
	}
	if _, err := config.EnsureHomeDir(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.ManagedBrowserDir(), 0o755); err != nil {
		return nil, fmt.Errorf("prepare managed browser profile: %w", err)
	}
	browserLock, err := processlock.Acquire(config.BrowserLockPath(), 12*time.Second)
	if err != nil {
		return nil, fmt.Errorf("serialize managed browser launch: %w", err)
	}
	defer browserLock.Release()

	// Another process may have completed the launch while this caller waited
	// for the browser lock. Reuse it only after proving the recorded browser
	// identity still matches the live CDP browser target.
	if port > 0 && canConnect("127.0.0.1", port) {
		if err := verifyManagedEndpoint(port, true); err != nil {
			return nil, err
		}
		return &CDPEndpoint{Host: "127.0.0.1", Port: port, OwnedByBorz: true}, nil
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

	// Wait for browser to become reachable
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if canConnect("127.0.0.1", port) {
			browserID, err := readBrowserID("127.0.0.1", port, 1200*time.Millisecond)
			if err != nil {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			if err := publishManagedBrowserState(port, browserID); err != nil {
				return nil, err
			}
			return &CDPEndpoint{Host: "127.0.0.1", Port: port, OwnedByBorz: true}, nil
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

func daemonPortForTarget(target profile.Target) (int, error) {
	if target.DaemonPort != 0 {
		return target.DaemonPort, nil
	}
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
	target, err := ActiveTarget()
	if err != nil {
		return 0, err
	}
	if target.Kind == profile.TransportRemote {
		return 0, fmt.Errorf("profile %q is remote; it has no local daemon port", profile.Normalize(config.Profile()))
	}
	return daemonPortForTarget(target)
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
		if config.Profile() != "" {
			return nil, fmt.Errorf("BORZ_CDP_URL cannot override named managed profile %q; declare a cdp profile instead", config.Profile())
		}
		if host, port, ok := profile.ParseCDPEndpoint(envURL); ok && canConnect(host, port) {
			return &CDPEndpoint{Host: host, Port: port}, nil
		}
	}

	// Priority 2: profile-scoped managed browser identity (with one-time
	// migration from the legacy cdp-port-only state).
	if port, ok := recordedManagedPort(); ok && canConnect("127.0.0.1", port) {
		if err := verifyManagedEndpoint(port, true); err != nil {
			return nil, err
		}
		return &CDPEndpoint{Host: "127.0.0.1", Port: port, OwnedByBorz: true}, nil
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

	return nil, fmt.Errorf("no CDP endpoint found: %w", err)
}

// CheckCDPEndpoint verifies a CDP endpoint answers /json/version. Used by
// 'borz profile add --cdp' to probe the target before persisting it.
func CheckCDPEndpoint(host string, port int, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	endpoint := fmt.Sprintf("http://%s:%d/json/version", host, port)
	httpClient := &http.Client{Timeout: timeout}
	resp, err := httpClient.Get(endpoint)
	if err != nil {
		return fmt.Errorf("cannot reach CDP endpoint %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CDP endpoint %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return nil
}
