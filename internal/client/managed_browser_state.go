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
			return fmt.Errorf("managed browser identity mismatch on port %d; refusing to attach to or own a different Chrome instance", port)
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
