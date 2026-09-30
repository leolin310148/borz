package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/protocol"
)

func TestSaveFetchBodyDecodesBinaryBase64(t *testing.T) {
	raw := []byte{'P', 'K', 3, 4, 0, 0xff, 0xfe, 0x80, '\n', 0}
	path := filepath.Join(t.TempDir(), "out", "file.docx")
	result := map[string]interface{}{
		"status":     float64(200),
		"bodyBase64": base64.StdEncoding.EncodeToString(raw),
	}
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: result}}
	if err := saveFetchBody(path, resp); err != nil {
		t.Fatalf("saveFetchBody: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("binary output corrupted: got %v want %v", got, raw)
	}
	if _, ok := result["bodyBase64"]; ok {
		t.Fatalf("bodyBase64 should be dropped from the printed result: %+v", result)
	}
	if result["bytes"] != len(raw) || result["output"] != path {
		t.Fatalf("result metadata: %+v", result)
	}
}

func TestSaveFetchBodyRejectsBadBase64(t *testing.T) {
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{Result: map[string]interface{}{"bodyBase64": "%%%"}}}
	if err := saveFetchBody(filepath.Join(t.TempDir(), "x"), resp); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestNavigationURLArgFromStdin(t *testing.T) {
	old := stdinReader
	t.Cleanup(func() { stdinReader = old })
	stdinReader = strings.NewReader("\n  https://example.test/login?token=secret  \nignored\n")
	if got := navigationURLArg("open", nil, []string{"--stdin"}); got != "https://example.test/login?token=secret" {
		t.Fatalf("stdin URL = %q", got)
	}
	if got := navigationURLArg("open", []string{"https://a.test"}, nil); got != "https://a.test" {
		t.Fatalf("positional URL = %q", got)
	}
	if got := navigationURLArg("open", nil, nil); got != "" {
		t.Fatalf("missing URL = %q", got)
	}
}

func TestExtensionErrorMessageNamesProfileAndNextSteps(t *testing.T) {
	msg := extensionErrorMessage("HTTP 503: no extension connected", "teams")
	for _, want := range []string{`profile "teams"`, "borz extension status --all-profiles", "borz --profile teams tab front", "Profile = teams"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
	if got := extensionErrorMessage("HTTP 500: boom", "teams"); got != "HTTP 500: boom" {
		t.Fatalf("unrelated errors must pass through unchanged, got %q", got)
	}
	if !strings.Contains(extensionErrorMessage("no extension connected", ""), `profile "default"`) {
		t.Fatal("empty profile should read as default")
	}
}

func TestRedactResponseURLHidesCredentialQuery(t *testing.T) {
	resp := &protocol.Response{Success: true, Data: &protocol.ResponseData{URL: "https://sso.test/cb?code=abc123&state=x"}}
	if err := redactResponseURL(resp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp.Data.URL, "abc123") {
		t.Fatalf("credential leaked: %s", resp.Data.URL)
	}
}
