package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
)

func TestManagedTitleLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	t.Setenv("BORZ_NO_TITLE_LABEL", "")
	client.ResetForTests()
	t.Cleanup(client.ResetForTests)
	cfg := `{"version":1,"profiles":{"jump":{"transport":"cdp","cdp":{"url":"http://127.0.0.1:9222"}}}}`
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = config.SetProfile("") })
	setProfile := func(name string) {
		t.Helper()
		if err := config.SetProfile(name); err != nil {
			t.Fatal(err)
		}
	}
	setProfile("teams")
	if got := managedTitleLabel(true); got != "[teams] " {
		t.Fatalf("managed owned browser label = %q", got)
	}
	if got := managedTitleLabel(false); got != "" {
		t.Fatalf("browser not owned by borz must stay unlabeled, got %q", got)
	}
	setProfile("")
	if got := managedTitleLabel(true); got != "[default] " {
		t.Fatalf("default profile label = %q", got)
	}
	setProfile("jump")
	if got := managedTitleLabel(true); got != "" {
		t.Fatalf("cdp profile must stay unlabeled, got %q", got)
	}
	setProfile("teams")
	t.Setenv("BORZ_NO_TITLE_LABEL", "1")
	if got := managedTitleLabel(true); got != "" {
		t.Fatalf("BORZ_NO_TITLE_LABEL=1 must disable, got %q", got)
	}
}
