package mcp

import (
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestNewMCPServerRegistersToolsAndVersion(t *testing.T) {
	s := newMCPServer("test-version")
	if s == nil {
		t.Fatal("server is nil")
	}
	if mcpVersion != "test-version" {
		t.Fatalf("mcpVersion = %q", mcpVersion)
	}
}

func TestMCPDurationSchemasRejectNegativeValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool mcpgo.Tool
		prop string
	}{
		{"navigate timeout", navigateTool, "timeout"},
		{"eval timeout", evalTool, "timeout"},
		{"wait ms", waitTool, "ms"},
		{"site run timeout", siteRunTool, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prop, ok := tc.tool.InputSchema.Properties[tc.prop].(map[string]interface{})
			if !ok {
				t.Fatalf("%s property schema missing or wrong type: %#v", tc.prop, tc.tool.InputSchema.Properties[tc.prop])
			}
			if got := prop["minimum"]; got != float64(0) {
				t.Fatalf("%s minimum = %#v, want 0", tc.prop, got)
			}
		})
	}
}
