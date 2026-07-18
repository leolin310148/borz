package processlock

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireSerializesAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.lock")
	first, err := Acquire(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("second Acquire error = %v, want timeout", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path, time.Second)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("double release: %v", err)
	}
}
