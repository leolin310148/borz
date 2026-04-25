package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// withCapturedStdout runs fn with os.Stdout swapped for a pipe; returns the
// captured output. Used by tests that exercise code paths writing directly to
// stdout.
func withCapturedStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	w.Close()
	<-done
	return buf.String()
}
