package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/client"
)

// fakeBrowserEndpoint answers /json/version like a Chrome CDP endpoint does.
func fakeBrowserEndpoint(t *testing.T, browserID string) int {
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
		t.Fatalf("parse port from %q: %v", srv.URL, err)
	}
	return port
}

func TestBrowserCLIStatusAndAdopt(t *testing.T) {
	setupProfileHome(t)
	port := fakeBrowserEndpoint(t, "chrome-abc")
	portFlag := strconv.Itoa(port)

	// Nothing recorded yet: status says so without pretending there is a problem.
	out := runProfileCLI(t, "browser", "status", "--port", portFlag)
	if !strings.Contains(out, "none recorded yet") || !strings.Contains(out, "chrome-abc") {
		t.Fatalf("status before adopt = %q", out)
	}

	out = runProfileCLI(t, "browser", "adopt", "--port", portFlag)
	if !strings.Contains(out, "Adopted the browser") || !strings.Contains(out, "chrome-abc") {
		t.Fatalf("adopt output = %q", out)
	}

	out = runProfileCLI(t, "browser", "status", "--port", portFlag)
	if !strings.Contains(out, "OK — this is borz's own browser") {
		t.Fatalf("status after adopt = %q", out)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(runProfileCLI(t, "browser", "status", "--port", portFlag, "--json")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["identityMatches"] != true || payload["liveBrowser"] != "chrome-abc" {
		t.Fatalf("status payload = %+v", payload)
	}

	// A different Chrome on the same port is the mismatch that wedges commands;
	// status must name it and point at the fix.
	other := fakeBrowserEndpoint(t, "chrome-xyz")
	out = runProfileCLI(t, "browser", "status", "--port", strconv.Itoa(other))
	if !strings.Contains(out, "MISMATCH") || !strings.Contains(out, "borz browser adopt") {
		t.Fatalf("mismatch status = %q", out)
	}
}

func TestBrowserCLIErrors(t *testing.T) {
	setupProfileHome(t)

	errOut := captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("browser", "adopt", "--port", "1") })
	})
	if !strings.Contains(errOut, "no CDP endpoint answered") {
		t.Fatalf("adopt with no endpoint stderr = %q", errOut)
	}

	errOut = captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("browser", "status", "--port", "abc") })
	})
	if !strings.Contains(errOut, "--port must be a TCP port") {
		t.Fatalf("bad port stderr = %q", errOut)
	}

	errOut = captureStderr(t, func() {
		expectExit(t, 1, func() { runMainArgsForExit("browser", "nope") })
	})
	if !strings.Contains(errOut, "nope") {
		t.Fatalf("unknown subcommand stderr = %q", errOut)
	}
}

func TestManagedBrowserPortDefaultsToRecorded(t *testing.T) {
	setupProfileHome(t)
	port := fakeBrowserEndpoint(t, "chrome-default")

	runProfileCLI(t, "browser", "adopt", "--port", strconv.Itoa(port))
	if got := client.ManagedBrowserPort(); got != port {
		t.Fatalf("ManagedBrowserPort() = %d, want the adopted %d", got, port)
	}
	// With no --port, status uses the recorded port rather than guessing.
	out := runProfileCLI(t, "browser", "status")
	if !strings.Contains(out, strconv.Itoa(port)) {
		t.Fatalf("status without --port = %q", out)
	}
}
