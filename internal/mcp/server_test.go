package mcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/observability"
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

func TestMCPLoggingMiddlewareRecordsOutcomeWithoutArguments(t *testing.T) {
	t.Setenv(config.HomeEnv, t.TempDir())
	logger, err := observability.Open("mcp", "test")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := mcpLoggingMiddleware(logger, "session-1")(func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultError("ref 9 not found for super-secret input"), nil
	})
	_, err = wrapped(context.Background(), mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
		Name: "browser_fill", Arguments: map[string]any{"text": "super-secret"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = logger.Close()
	entries, err := observability.ReadEntries(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Tool != "browser_fill" || entries[0].ErrorCode != "stale_ref" {
		t.Fatalf("entries = %+v", entries)
	}
	raw, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret") {
		t.Fatalf("argument leaked: %s", raw)
	}
}
