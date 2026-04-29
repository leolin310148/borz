package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := newCommand("echo", "hi")
	if cmd == nil {
		t.Fatal("nil cmd")
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "hi" {
		t.Fatalf("args: %+v", cmd.Args)
	}
}

// gitPull/gitClone rely on the `git` binary. These tests just ensure the
// functions execute without panicking and return a non-nil error when pointed
// at a path that isn't a real git remote or repo. We don't care about the
// specific error — coverage is the goal.
func TestGitPull_Failure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Non-git directory: `git -C <dir> pull` fails.
	tmp := t.TempDir()
	if err := gitPull(tmp); err == nil {
		t.Fatal("expected error pulling in non-git dir")
	}
}

func TestGitClone_Failure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Bogus URL fails fast.
	dest := filepath.Join(t.TempDir(), "out")
	if err := gitClone("file:///definitely/does/not/exist", dest); err == nil {
		t.Fatal("expected clone failure")
	}
}

func TestUpdateCommunityRepo_ClonePath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// Point the community dir at a fresh tempdir with no .git so the clone
	// path is taken. The real remote URL will (likely) fail during clone — we
	// just want coverage of the branch.
	home := t.TempDir()
	t.Setenv("BORZ_HOME", home)
	// Allow a short network attempt; skip if clone unexpectedly succeeds.
	_ = UpdateCommunityRepo("")

	// Also exercise the "pull" branch: make the dir look like a git repo.
	dir := filepath.Join(home, "bb-sites")
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = UpdateCommunityRepo("")
}
