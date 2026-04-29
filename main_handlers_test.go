package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	w.Close()
	return <-done
}

func TestPrintJSON(t *testing.T) {
	out := captureStdout(t, func() {
		printJSON(map[string]string{"key": "value"})
	})
	if !strings.Contains(out, `"key": "value"`) {
		t.Fatalf("printJSON output: %q", out)
	}
}

func TestPrintHelp(t *testing.T) {
	out := captureStdout(t, func() {
		printHelp()
	})
	if !strings.Contains(out, "borz") {
		t.Fatalf("printHelp should mention borz: %q", out[:min(200, len(out))])
	}
	// Spot-check a few sections.
	for _, want := range []string{"Navigation:", "Interaction:", "Observation:"} {
		if !strings.Contains(out, want) {
			t.Errorf("printHelp missing section %q", want)
		}
	}
}

func TestHandleSite_List_JSON(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"list"}, true, "")
	})
	// JSON output should look like a JSON array.
	out = strings.TrimSpace(out)
	if !(strings.HasPrefix(out, "[") || strings.HasPrefix(out, "null")) {
		t.Fatalf("expected JSON array or null, got: %q", out[:min(100, len(out))])
	}
}

func TestHandleSite_List_DefaultSub(t *testing.T) {
	// Empty args -> defaults to "list".
	out := captureStdout(t, func() {
		handleSite([]string{}, true, "")
	})
	if len(out) == 0 {
		t.Fatal("expected some output for default list")
	}
}

func TestHandleSite_Search_JSON(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"search", "twitter"}, true, "")
	})
	out = strings.TrimSpace(out)
	if !(strings.HasPrefix(out, "[") || strings.HasPrefix(out, "null")) {
		t.Fatalf("search JSON: %q", out[:min(100, len(out))])
	}
}

func TestHandleSite_Search_Text(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"search", "anything"}, false, "")
	})
	if !strings.Contains(out, "results") {
		t.Fatalf("search text output missing 'results': %q", out)
	}
}

func TestHandleSite_List_Text(t *testing.T) {
	out := captureStdout(t, func() {
		handleSite([]string{"list"}, false, "")
	})
	if !strings.Contains(out, "Total:") {
		t.Fatalf("list text output missing 'Total:': %q", out)
	}
}
