package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

func TestHandleDaemonTokenConfiguredAndCopy(t *testing.T) {
	setupProfileHome(t)
	runProfileCLI(t, "profile", "add", "clean", "--managed", "--daemon-token", "stable-secret")
	if err := config.SetProfile("clean"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })

	out := withCapturedStdout(t, func() { handleDaemonToken(nil) })
	if strings.TrimSpace(out) != "stable-secret" {
		t.Fatalf("token output = %q", out)
	}
	out = withCapturedStdout(t, func() { handleDaemonToken([]string{"--json"}) })
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["profile"] != "clean" || payload["token"] != "stable-secret" {
		t.Fatalf("token JSON = %+v", payload)
	}

	oldCopy := copyDaemonTokenToClipboard
	var copied string
	copyDaemonTokenToClipboard = func(secret string) error {
		copied = secret
		return nil
	}
	t.Cleanup(func() { copyDaemonTokenToClipboard = oldCopy })
	out = withCapturedStdout(t, func() { handleDaemonToken([]string{"--copy"}) })
	if copied != "stable-secret" || !strings.Contains(out, "copied to the clipboard") {
		t.Fatalf("copy = %q, output = %q", copied, out)
	}
}

func TestHandleDaemonTokenPrefersRunningDaemon(t *testing.T) {
	setupProfileHome(t)
	runProfileCLI(t, "profile", "add", "clean", "--managed", "--daemon-token", "next-stable-secret")
	if err := config.SetProfile("clean"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.SetProfile("") })

	info, err := json.Marshal(protocol.DaemonInfo{
		PID:   os.Getpid(),
		Host:  "127.0.0.1",
		Port:  19827,
		Token: "currently-live-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(config.DaemonJSONPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.DaemonJSONPath(), info, 0o600); err != nil {
		t.Fatal(err)
	}

	out := withCapturedStdout(t, func() { handleDaemonToken(nil) })
	if strings.TrimSpace(out) != "currently-live-secret" {
		t.Fatalf("token output = %q", out)
	}
}
