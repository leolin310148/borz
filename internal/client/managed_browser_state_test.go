package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/config"
)

// fakeCDPServer answers /json/version with browserID, on 127.0.0.1.
func fakeCDPServer(t *testing.T, browserID string) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1/devtools/browser/%s"}`, browserID)
	}))
	t.Cleanup(srv.Close)
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatalf("parse test server port from %q: %v", srv.URL, err)
	}
	return srv, port
}

func TestAdoptManagedBrowserRecordsLiveBrowser(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())
	_, port := fakeCDPServer(t, "live-browser-id")

	// A stale record for the same port: exactly the state that makes every
	// command fail with an identity mismatch.
	if err := publishManagedBrowserState(port, "old-browser-id"); err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedEndpoint(port, true); err == nil {
		t.Fatal("stale record should not verify")
	} else if !strings.Contains(err.Error(), "browser adopt") {
		t.Fatalf("mismatch error should point at the recovery command: %v", err)
	}

	adoptedPort, browserID, err := AdoptManagedBrowser(port)
	if err != nil {
		t.Fatalf("AdoptManagedBrowser: %v", err)
	}
	if adoptedPort != port || browserID != "live-browser-id" {
		t.Fatalf("adopted %d/%q, want %d/live-browser-id", adoptedPort, browserID, port)
	}
	if err := verifyManagedEndpoint(port, false); err != nil {
		t.Fatalf("adopted browser should verify: %v", err)
	}

	// The record on disk names the live browser and this profile's user-data.
	state, err := readManagedBrowserState()
	if err != nil {
		t.Fatal(err)
	}
	if state.BrowserID != "live-browser-id" || state.Port != port ||
		state.UserDataDir != config.ManagedUserDataDir() {
		t.Fatalf("state = %+v", state)
	}
}

func TestAdoptManagedBrowserWithoutEndpointFails(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())

	// Port 1 is not a borz browser; adopt must refuse rather than record a
	// browser that does not exist.
	if _, _, err := AdoptManagedBrowser(1); err == nil {
		t.Fatal("adopt should fail when nothing answers")
	} else if !strings.Contains(err.Error(), "no CDP endpoint answered") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(config.ManagedStateFile()); !os.IsNotExist(err) {
		t.Fatalf("failed adopt should not write state: %v", err)
	}
}

func TestManagedBrowserIdentityAndPort(t *testing.T) {
	resetState()
	t.Cleanup(resetState)
	t.Setenv("BORZ_HOME", t.TempDir())

	// Nothing recorded yet: the default CDP port is what borz would look at.
	if got := ManagedBrowserPort(); got != config.DefaultCDPPort {
		t.Fatalf("ManagedBrowserPort() = %d, want %d", got, config.DefaultCDPPort)
	}

	_, port := fakeCDPServer(t, "live-id")
	if err := publishManagedBrowserState(port, "recorded-id"); err != nil {
		t.Fatal(err)
	}
	if got := ManagedBrowserPort(); got != port {
		t.Fatalf("ManagedBrowserPort() = %d, want the recorded %d", got, port)
	}
	recorded, live, recordedPort := ManagedBrowserIdentity(port)
	if recorded != "recorded-id" || live != "live-id" || recordedPort != port {
		t.Fatalf("identity = %q/%q/%d", recorded, live, recordedPort)
	}

	// Nothing listening: the live half is empty, the record still reads.
	recorded, live, _ = ManagedBrowserIdentity(1)
	if recorded != "recorded-id" || live != "" {
		t.Fatalf("identity with no browser = %q/%q", recorded, live)
	}
}

func TestProbeDaemonPortIdentifiesSquattingDaemon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "pid": 4242, "version": "0.24.0"})
	}))
	defer srv.Close()
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	found, ok := probeDaemonPort(port)
	if !ok {
		t.Fatal("probeDaemonPort should recognise a borz daemon")
	}
	if found.PID != 4242 || found.Version != "0.24.0" {
		t.Fatalf("found = %+v", found)
	}
	if got := found.describe(); got != "version 0.24.0, pid 4242" {
		t.Fatalf("describe() = %q", got)
	}
	if got := found.stopHint(); !strings.Contains(got, "kill 4242") {
		t.Fatalf("stopHint() = %q", got)
	}

	// A daemon too old to report identity is still worth naming as the cause.
	bare := daemonPortSquatter{}
	if got := bare.describe(); got != "unknown version" {
		t.Fatalf("bare describe() = %q", got)
	}
	if got := bare.stopHint(); got != "stop that process" {
		t.Fatalf("bare stopHint() = %q", got)
	}

	// Nothing listening, and a non-borz listener, both read as "not a daemon".
	if _, ok := probeDaemonPort(1); ok {
		t.Fatal("probeDaemonPort should not find a daemon on a dead port")
	}
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"hello":"world"}`)
	}))
	defer foreign.Close()
	foreignPort, _ := strconv.Atoi(foreign.URL[strings.LastIndex(foreign.URL, ":")+1:])
	if _, ok := probeDaemonPort(foreignPort); ok {
		t.Fatal("a non-borz server must not be reported as a daemon")
	}
}
