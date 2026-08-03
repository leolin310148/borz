package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

const managedBrowserStateVersion = 1

type managedBrowserState struct {
	Version     int    `json:"version"`
	Port        int    `json:"port"`
	BrowserID   string `json:"browserId"`
	UserDataDir string `json:"userDataDir"`
}

type cdpVersionInfo struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// ReadCDPBrowserID returns the browser-level CDP identity answering on a port,
// i.e. the id embedded in its webSocketDebuggerUrl. Two Chromes never share
// one, so it is what distinguishes borz's own managed browser from any other
// Chrome that happens to be listening on a remembered port.
func ReadCDPBrowserID(host string, port int, timeout time.Duration) (string, error) {
	return readCDPBrowserID(host, port, timeout)
}

func readCDPBrowserID(host string, port int, timeout time.Duration) (string, error) {
	endpoint := fmt.Sprintf("http://%s:%d/json/version", host, port)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(endpoint)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var version cdpVersionInfo
	if err := json.Unmarshal(body, &version); err != nil {
		return "", fmt.Errorf("decode %s: %w", endpoint, err)
	}
	wsURL := strings.TrimSpace(version.WebSocketDebuggerURL)
	parsed, err := url.Parse(wsURL)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" || parsed.Path == "" {
		return "", fmt.Errorf("%s did not return a valid webSocketDebuggerUrl", endpoint)
	}
	const prefix = "/devtools/browser/"
	if !strings.HasPrefix(parsed.Path, prefix) || strings.TrimPrefix(parsed.Path, prefix) == "" {
		return "", fmt.Errorf("%s returned an unexpected browser WebSocket path", endpoint)
	}
	return strings.TrimPrefix(parsed.Path, prefix), nil
}

func readManagedBrowserState() (*managedBrowserState, error) {
	data, err := os.ReadFile(config.ManagedStateFile())
	if err != nil {
		return nil, err
	}
	var state managedBrowserState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("invalid managed browser state: %w", err)
	}
	if state.Version != managedBrowserStateVersion || state.Port <= 0 || state.Port > 65535 ||
		strings.TrimSpace(state.BrowserID) == "" || filepath.Clean(state.UserDataDir) != filepath.Clean(config.ManagedUserDataDir()) {
		return nil, fmt.Errorf("invalid managed browser state")
	}
	return &state, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".borz-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
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

func publishManagedBrowserState(port int, browserID string) error {
	state := managedBrowserState{
		Version:     managedBrowserStateVersion,
		Port:        port,
		BrowserID:   browserID,
		UserDataDir: config.ManagedUserDataDir(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(config.ManagedStateFile(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write managed browser identity: %w", err)
	}
	if err := writeAtomic(config.ManagedPortFile(), []byte(fmt.Sprintf("%d\n", port)), 0o600); err != nil {
		return fmt.Errorf("write managed browser port: %w", err)
	}
	return nil
}

// verifyManagedEndpoint proves that port still identifies the browser borz
// recorded for this profile. With allowLegacy, a pre-browser.json installation
// is imported once from its existing profile-scoped cdp-port file.
func verifyManagedEndpoint(port int, allowLegacy bool) error {
	browserID, err := readBrowserID("127.0.0.1", port, 1200*time.Millisecond)
	if err != nil {
		return err
	}
	state, stateErr := readManagedBrowserState()
	if stateErr == nil {
		if state.Port != port || state.BrowserID != browserID {
			// Deliberately not self-healing: from here a stale record and a
			// foreign Chrome squatting on the port look identical, and
			// adopting the wrong one means driving someone else's session.
			// 'browser adopt' is the explicit, one-command way out.
			return fmt.Errorf("managed browser identity mismatch on port %d; refusing to attach to or own a different Chrome instance"+
				" (if that Chrome is borz's own — e.g. launched by an older borz — record it with 'borz browser adopt')", port)
		}
		return nil
	}
	if !allowLegacy || !os.IsNotExist(stateErr) {
		return stateErr
	}
	data, err := os.ReadFile(config.ManagedPortFile())
	if err != nil || strings.TrimSpace(string(data)) != fmt.Sprintf("%d", port) {
		return fmt.Errorf("CDP port %d is reachable but is not recorded as this profile's managed browser", port)
	}
	return publishManagedBrowserState(port, browserID)
}

// ManagedBrowserPort is the port borz looks for this profile's managed browser
// on: the recorded one, else the default CDP port.
func ManagedBrowserPort() int {
	if port, ok := recordedManagedPort(); ok {
		return port
	}
	return config.DefaultCDPPort
}

// ManagedBrowserIdentity reports the recorded managed-browser identity and the
// id the browser now answering that port reports. Either half may be empty: no
// record yet, or nothing listening.
func ManagedBrowserIdentity(port int) (recordedID string, liveID string, recordedPort int) {
	if state, err := readManagedBrowserState(); err == nil {
		recordedID, recordedPort = state.BrowserID, state.Port
	}
	if id, err := readBrowserID("127.0.0.1", port, 2*time.Second); err == nil {
		liveID = id
	}
	return recordedID, liveID, recordedPort
}

// AdoptManagedBrowser records the Chrome currently answering on port as this
// profile's managed browser, replacing any stale identity. This is the
// sanctioned recovery from an identity mismatch — e.g. a browser launched by a
// borz old enough not to record identities — and is deliberately explicit:
// borz will not silently take ownership of a browser it cannot prove is its own.
func AdoptManagedBrowser(port int) (int, string, error) {
	if port <= 0 {
		port = ManagedBrowserPort()
	}
	browserID, err := readBrowserID("127.0.0.1", port, 2*time.Second)
	if err != nil {
		return 0, "", fmt.Errorf("no CDP endpoint answered on 127.0.0.1:%d: %w", port, err)
	}
	if err := publishManagedBrowserState(port, browserID); err != nil {
		return 0, "", err
	}
	return port, browserID, nil
}

func recordedManagedPort() (int, bool) {
	if state, err := readManagedBrowserState(); err == nil {
		return state.Port, true
	}
	data, err := os.ReadFile(config.ManagedPortFile())
	if err != nil {
		return 0, false
	}
	port, err := strconvAtoiPort(strings.TrimSpace(string(data)))
	return port, err == nil
}

func strconvAtoiPort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid TCP port")
	}
	return port, nil
}
