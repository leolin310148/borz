package diagnostics

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withBinaryDiagnosticStubs(t *testing.T) {
	t.Helper()
	oldExecutable := diagnosticExecutable
	oldPathEnv := diagnosticPathEnv
	oldVersionAt := diagnosticVersionAt
	t.Cleanup(func() {
		diagnosticExecutable = oldExecutable
		diagnosticPathEnv = oldPathEnv
		diagnosticVersionAt = oldVersionAt
	})
}

func writeDiagnosticBinary(t *testing.T, dir, contents string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "borz"
	if runtime.GOOS == "windows" {
		name = "borz.exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckBinaryPathDetectsVersionDrift(t *testing.T) {
	withBinaryDiagnosticStubs(t)
	root := t.TempDir()
	activeDir := filepath.Join(root, "usr-local-bin")
	staleDir := filepath.Join(root, "user-local-bin")
	active := writeDiagnosticBinary(t, activeDir, "current binary")
	stale := writeDiagnosticBinary(t, staleDir, "stale binary")
	diagnosticExecutable = func() (string, error) { return active, nil }
	diagnosticPathEnv = func() string {
		return strings.Join([]string{activeDir, staleDir}, string(os.PathListSeparator))
	}
	diagnosticVersionAt = func(path string) string {
		if path == stale {
			return "0.25.0-dev.20260728"
		}
		return ""
	}

	check := checkBinaryPath("0.25.0-9-ge297906")
	if check.Status != StatusWarn || !strings.Contains(check.Detail, "conflicts with") {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Binaries) != 2 {
		t.Fatalf("binaries = %+v", check.Binaries)
	}
	if !check.Binaries[0].Current || !check.Binaries[0].MatchesCurrent || check.Binaries[0].Version != "0.25.0-9-ge297906" {
		t.Fatalf("active candidate = %+v", check.Binaries[0])
	}
	if check.Binaries[1].Current || check.Binaries[1].MatchesCurrent || check.Binaries[1].Version != "0.25.0-dev.20260728" {
		t.Fatalf("stale candidate = %+v", check.Binaries[1])
	}
	if check.Binaries[0].SHA256 == check.Binaries[1].SHA256 {
		t.Fatal("different binaries should have different hashes")
	}
}

func TestCheckBinaryPathDeduplicatesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions are environment-dependent on Windows")
	}
	withBinaryDiagnosticStubs(t)
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	aliasDir := filepath.Join(root, "alias")
	active := writeDiagnosticBinary(t, activeDir, "same binary")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(aliasDir, "borz")
	if err := os.Symlink(active, alias); err != nil {
		t.Fatal(err)
	}
	diagnosticExecutable = func() (string, error) { return active, nil }
	diagnosticPathEnv = func() string {
		return strings.Join([]string{activeDir, aliasDir}, string(os.PathListSeparator))
	}

	check := checkBinaryPath("same-version")
	if check.Status != StatusOK || len(check.Binaries) != 1 {
		t.Fatalf("check = %+v", check)
	}
}

func TestCheckBinaryPathWarnsForSeparateMatchingCopies(t *testing.T) {
	withBinaryDiagnosticStubs(t)
	root := t.TempDir()
	activeDir := filepath.Join(root, "active")
	copyDir := filepath.Join(root, "copy")
	active := writeDiagnosticBinary(t, activeDir, "same bytes")
	writeDiagnosticBinary(t, copyDir, "same bytes")
	diagnosticExecutable = func() (string, error) { return active, nil }
	diagnosticPathEnv = func() string {
		return strings.Join([]string{activeDir, copyDir}, string(os.PathListSeparator))
	}

	check := checkBinaryPath("same-version")
	if check.Status != StatusWarn || !strings.Contains(check.Detail, "currently match") || len(check.Binaries) != 2 {
		t.Fatalf("check = %+v", check)
	}
	for _, candidate := range check.Binaries {
		if !candidate.MatchesCurrent {
			t.Fatalf("matching candidate = %+v", candidate)
		}
	}
}

func TestCheckBinaryPathWarnsWhenActiveIsNotOnPATH(t *testing.T) {
	withBinaryDiagnosticStubs(t)
	root := t.TempDir()
	active := writeDiagnosticBinary(t, filepath.Join(root, "active"), "active")
	staleDir := filepath.Join(root, "stale")
	writeDiagnosticBinary(t, staleDir, "stale")
	diagnosticExecutable = func() (string, error) { return active, nil }
	diagnosticPathEnv = func() string { return staleDir }

	check := checkBinaryPath("active-version")
	if check.Status != StatusWarn || !strings.Contains(check.Detail, "not reachable through PATH") {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Binaries) != 2 || !check.Binaries[0].Current {
		t.Fatalf("binaries = %+v", check.Binaries)
	}
}

func TestCheckBinaryPathHandlesResolutionErrorsAndTests(t *testing.T) {
	withBinaryDiagnosticStubs(t)
	diagnosticExecutable = func() (string, error) { return "", errors.New("no executable") }
	if check := checkBinaryPath("v"); check.Status != StatusWarn || !strings.Contains(check.Detail, "no executable") {
		t.Fatalf("executable error check = %+v", check)
	}

	withBinaryDiagnosticStubs(t)
	diagnosticExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "diagnostics.test"), nil }
	if check := checkBinaryPath("v"); check.Status != StatusOK || !strings.Contains(check.Detail, "scan skipped") {
		t.Fatalf("test executable check = %+v", check)
	}
}

func TestCheckBinaryPathHandlesUnreadableCurrentBinary(t *testing.T) {
	withBinaryDiagnosticStubs(t)
	missing := filepath.Join(t.TempDir(), "borz")
	diagnosticExecutable = func() (string, error) { return missing, nil }
	diagnosticPathEnv = func() string { return "" }

	check := checkBinaryPath("v")
	if check.Status != StatusWarn || !strings.Contains(check.Detail, "cannot fingerprint") {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Binaries) != 1 || check.Binaries[0].Error == "" {
		t.Fatalf("binaries = %+v", check.Binaries)
	}
}

func TestVersionFromLDFlags(t *testing.T) {
	for _, test := range []struct {
		flags string
		want  string
	}{
		{"-s -w -X main.version=0.25.0-9-ge297906", "0.25.0-9-ge297906"},
		{"-X=main.version=dev", "dev"},
		{"-X other.value=x", ""},
		{"", ""},
	} {
		if got := versionFromLDFlags(test.flags); got != test.want {
			t.Errorf("versionFromLDFlags(%q) = %q, want %q", test.flags, got, test.want)
		}
	}
	if got := displayBinaryVersion(""); got != "version unknown" {
		t.Fatalf("displayBinaryVersion empty = %q", got)
	}
}
