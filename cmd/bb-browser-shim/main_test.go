package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunWrapperMissingBorz(t *testing.T) {
	oldLookPath := lookPath
	oldRunBorz := runBorz
	t.Cleanup(func() {
		lookPath = oldLookPath
		runBorz = oldRunBorz
	})
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	runBorz = func(string, []string) error {
		t.Fatal("runBorz should not be called")
		return nil
	}

	var stderr bytes.Buffer
	if code := runWrapper([]string{"--version"}, &stderr); code != 127 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), deprecationNotice) || !strings.Contains(stderr.String(), "could not find") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunWrapperDelegatesArgs(t *testing.T) {
	oldLookPath := lookPath
	oldRunBorz := runBorz
	t.Cleanup(func() {
		lookPath = oldLookPath
		runBorz = oldRunBorz
	})
	lookPath = func(name string) (string, error) {
		if name != "borz" {
			t.Fatalf("lookPath name = %q", name)
		}
		return "/usr/local/bin/borz", nil
	}
	var gotPath string
	var gotArgs []string
	runBorz = func(path string, args []string) error {
		gotPath = path
		gotArgs = append([]string(nil), args...)
		return nil
	}

	var stderr bytes.Buffer
	if code := runWrapper([]string{"open", "https://example.test"}, &stderr); code != 0 {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}
	if gotPath != "/usr/local/bin/borz" || !reflect.DeepEqual(gotArgs, []string{"open", "https://example.test"}) {
		t.Fatalf("delegated path=%q args=%v", gotPath, gotArgs)
	}
}

func TestRunWrapperRejectsSelfDelegation(t *testing.T) {
	oldLookPath := lookPath
	oldExecutablePath := executablePath
	oldRunBorz := runBorz
	t.Cleanup(func() {
		lookPath = oldLookPath
		executablePath = oldExecutablePath
		runBorz = oldRunBorz
	})

	dir := t.TempDir()
	self := filepath.Join(dir, "bb-browser")
	if err := os.WriteFile(self, []byte("shim"), 0o755); err != nil {
		t.Fatal(err)
	}

	lookPath = func(string) (string, error) { return self, nil }
	executablePath = func() (string, error) { return self, nil }
	runBorz = func(string, []string) error {
		t.Fatal("runBorz should not be called")
		return nil
	}

	var stderr bytes.Buffer
	if code := runWrapper(nil, &stderr); code != 126 {
		t.Fatalf("exit code = %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "resolves to this compatibility wrapper") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunWrapperPropagatesErrors(t *testing.T) {
	oldLookPath := lookPath
	oldRunBorz := runBorz
	t.Cleanup(func() {
		lookPath = oldLookPath
		runBorz = oldRunBorz
	})
	lookPath = func(string) (string, error) { return "/bin/borz", nil }

	runBorz = func(string, []string) error { return errors.New("exec failed") }
	var stderr bytes.Buffer
	if code := runWrapper(nil, &stderr); code != 1 {
		t.Fatalf("generic error code = %d", code)
	}
	if !strings.Contains(stderr.String(), "exec failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	cmd := exec.Command("sh", "-c", "exit 42")
	exitErr := cmd.Run()
	runBorz = func(string, []string) error { return exitErr }
	stderr.Reset()
	if code := runWrapper(nil, &stderr); code != 42 {
		t.Fatalf("exit error code = %d", code)
	}
}
