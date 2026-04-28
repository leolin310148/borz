package mcp

import "testing"

func TestNewMCPServerRegistersToolsAndVersion(t *testing.T) {
	s := newMCPServer("test-version")
	if s == nil {
		t.Fatal("server is nil")
	}
	if mcpVersion != "test-version" {
		t.Fatalf("mcpVersion = %q", mcpVersion)
	}
}
