package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/profile"
)

// e2e browser isolation.
//
// Without BORZ_CDP_URL, client.DiscoverCDPPort falls back to the default CDP
// port (19825) — the developer's own default borz Chrome — so e2e runs used to
// open fixture tabs in it. TestMain now launches a throwaway Chrome in a temp
// directory for the whole package and points BORZ_CDP_URL at it; every e2e
// lookup goes through e2eDiscoverCDP, which fails instead of falling back to
// any other browser. Setting BORZ_CDP_URL yourself still selects that browser.

// e2eIsolatedCDPURL is the endpoint e2e tests must use; empty outside e2e.
var e2eIsolatedCDPURL string

func TestMain(m *testing.M) {
	cleanup, err := setupE2EBrowser()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func e2eRequested() bool {
	if os.Getenv("BORZ_E2E_HELPER") == "1" || os.Getenv("GITHUB_ACTIONS") == "true" {
		return false
	}
	return os.Getenv(e2eEnabledEnv) == "1" || os.Getenv(e2eLegacyEnabledEnv) == "1"
}

func setupE2EBrowser() (func(), error) {
	noop := func() {}
	if !e2eRequested() {
		return noop, nil
	}
	if explicit := config.Env("BORZ_CDP_URL", "BB_BROWSER_CDP_URL"); explicit != "" {
		e2eIsolatedCDPURL = explicit
		return noop, nil
	}

	home, err := os.MkdirTemp("", "borz-e2e-browser-")
	if err != nil {
		return nil, fmt.Errorf("create isolated browser home: %w", err)
	}
	removeHome := func() { _ = os.RemoveAll(home) }
	port, err := e2eFreePort()
	if err != nil {
		removeHome()
		return nil, err
	}

	prevHome, hadHome := os.LookupEnv("BORZ_HOME")
	os.Setenv("BORZ_HOME", home)
	client.ResetForTests()
	launchErr := client.LaunchManagedBrowser(port)
	if hadHome {
		os.Setenv("BORZ_HOME", prevHome)
	} else {
		os.Unsetenv("BORZ_HOME")
	}
	client.ResetForTests()
	if launchErr != nil {
		removeHome()
		return nil, fmt.Errorf("launch isolated e2e Chrome: %w", launchErr)
	}

	e2eIsolatedCDPURL = "http://127.0.0.1:" + strconv.Itoa(port)
	os.Setenv("BORZ_CDP_URL", e2eIsolatedCDPURL)
	return func() {
		os.Unsetenv("BORZ_CDP_URL")
		e2eCloseBrowserAt(port)
		removeHome()
	}, nil
}

// e2eDiscoverCDP returns the isolated e2e browser endpoint, failing rather
// than attaching to whatever Chrome happens to listen on the default port.
func e2eDiscoverCDP(t *testing.T) *client.CDPEndpoint {
	t.Helper()
	ep, err := client.DiscoverCDPPort()
	if err != nil {
		t.Fatalf("discover Chrome CDP endpoint: %v", err)
	}
	if e2eIsolatedCDPURL == "" {
		t.Fatalf("e2e browser was not set up (TestMain did not run with %s=1)", e2eEnabledEnv)
	}
	_, wantPort, ok := profile.ParseCDPEndpoint(e2eIsolatedCDPURL)
	if !ok || ep.Port != wantPort {
		t.Fatalf("e2e resolved Chrome %s:%d, want the isolated e2e browser %s; refusing to touch another browser",
			ep.Host, ep.Port, e2eIsolatedCDPURL)
	}
	return ep
}

func e2eFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pick isolated browser port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// e2eCloseBrowserAt closes a Chrome through CDP Browser.close and waits for
// its endpoint to go away. Best-effort.
func e2eCloseBrowserAt(port int) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return
	}
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&version)
	resp.Body.Close()
	if version.WebSocketDebuggerURL == "" {
		return
	}
	conn, _, err := websocket.DefaultDialer.Dial(version.WebSocketDebuggerURL, nil)
	if err != nil {
		return
	}
	_ = conn.WriteJSON(map[string]any{"id": 1, "method": "Browser.close"})
	conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(100 * time.Millisecond)
	}
}
