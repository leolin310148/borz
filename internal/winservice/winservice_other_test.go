//go:build !windows

package winservice

import (
	"context"
	"strings"
	"testing"
)

func TestUnsupportedPlatformOperations(t *testing.T) {
	if Supported() {
		t.Fatal("Supported should be false on non-Windows platforms")
	}
	ops := []struct {
		name string
		fn   func() error
	}{
		{"install", func() error { return Install(Config{Name: DefaultName}) }},
		{"uninstall", func() error { return Uninstall(DefaultName) }},
		{"start", func() error { return Start(DefaultName) }},
		{"stop", func() error { return Stop(DefaultName) }},
		{"run", func() error { return Run(DefaultName, func(context.Context) error { return nil }) }},
		{"unsupported", unsupported},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			if err := op.fn(); err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if status, err := Status(DefaultName); status != "" || err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
		t.Fatalf("Status = (%q, %v)", status, err)
	}
}
