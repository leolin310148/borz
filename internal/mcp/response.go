package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

// checkError returns a tool error result if the command failed.
// Returns nil if the response is successful.
func checkError(resp *protocol.Response, err error) *mcp.CallToolResult {
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Command failed: %s", err.Error()))
	}
	if !resp.Success {
		return mcp.NewToolResultError(resp.Error)
	}
	return nil
}

// textResult returns a simple text result with page context.
func textResult(resp *protocol.Response, message string) *mcp.CallToolResult {
	if resp.Data != nil && resp.Data.URL != "" {
		message += fmt.Sprintf("\nPage: %s", resp.Data.URL)
	}
	if resp.Data != nil && resp.Data.Title != "" {
		message += fmt.Sprintf(" — %s", resp.Data.Title)
	}
	return mcp.NewToolResultText(message)
}

// formatSnapshot formats a snapshot response as text.
func formatSnapshot(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || resp.Data.SnapshotData == nil {
		return mcp.NewToolResultText("(empty snapshot)")
	}
	return mcp.NewToolResultText(resp.Data.SnapshotData.Snapshot)
}

// formatScreenshot returns a screenshot as an image content result.
func formatScreenshot(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || resp.Data.DataURL == "" {
		return mcp.NewToolResultText("Screenshot captured (no image data returned)")
	}
	base64Data := resp.Data.DataURL
	base64Data = strings.TrimPrefix(base64Data, "data:image/png;base64,")
	base64Data = strings.TrimPrefix(base64Data, "data:image/jpeg;base64,")

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{
				Type:     "image",
				Data:     base64Data,
				MIMEType: "image/png",
			},
		},
	}
}

// formatTabList formats a tab list response as readable text.
func formatTabList(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || len(resp.Data.Tabs) == 0 {
		return mcp.NewToolResultText("No tabs open")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Tabs (%d total):\n", len(resp.Data.Tabs))
	for _, tab := range resp.Data.Tabs {
		prefix := "  "
		if tab.Active {
			prefix = "* "
		}
		fmt.Fprintf(&sb, "%s[%d] %s — %s (tab: %v)\n", prefix, tab.Index, tab.URL, tab.Title, tab.TabID)
	}
	return mcp.NewToolResultText(sb.String())
}

// formatEval formats an eval result.
func formatEval(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil {
		return mcp.NewToolResultText("(no result)")
	}
	if resp.Data.Result == nil {
		return mcp.NewToolResultText("undefined")
	}
	out, err := json.MarshalIndent(resp.Data.Result, "", "  ")
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("%v", resp.Data.Result))
	}
	return mcp.NewToolResultText(string(out))
}

// formatGet formats a get attribute response.
func formatGet(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil {
		return mcp.NewToolResultText("")
	}
	if resp.Data.Value != "" {
		return mcp.NewToolResultText(resp.Data.Value)
	}
	if resp.Data.URL != "" {
		return mcp.NewToolResultText(resp.Data.URL)
	}
	if resp.Data.Title != "" {
		return mcp.NewToolResultText(resp.Data.Title)
	}
	if resp.Data.Result != nil {
		out, err := json.MarshalIndent(resp.Data.Result, "", "  ")
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("%v", resp.Data.Result))
		}
		return mcp.NewToolResultText(string(out))
	}
	return mcp.NewToolResultText("")
}

// formatNetwork formats network request data as readable text.
func formatNetwork(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || len(resp.Data.NetworkRequests) == 0 {
		return mcp.NewToolResultText("No network requests captured")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Network requests (%d):\n", len(resp.Data.NetworkRequests))
	for _, req := range resp.Data.NetworkRequests {
		status := "pending"
		if req.Status != nil {
			status = fmt.Sprintf("%d", *req.Status)
		}
		if req.Failed {
			status = "FAILED"
		}
		fmt.Fprintf(&sb, "  [%s] %s %s (%s)\n", status, req.Method, req.URL, req.Type)
		if req.ResponseBody != "" {
			body := req.ResponseBody
			if len(body) > 500 {
				body = body[:500] + "... (truncated)"
			}
			fmt.Fprintf(&sb, "    Body: %s\n", body)
		}
	}
	return mcp.NewToolResultText(sb.String())
}

// formatConsole formats console messages as readable text.
func formatConsole(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || len(resp.Data.ConsoleMessages) == 0 {
		return mcp.NewToolResultText("No console messages captured")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Console messages (%d):\n", len(resp.Data.ConsoleMessages))
	for _, msg := range resp.Data.ConsoleMessages {
		fmt.Fprintf(&sb, "  [%s] %s\n", msg.Type, msg.Text)
	}
	return mcp.NewToolResultText(sb.String())
}

// formatErrors formats JavaScript errors as readable text.
func formatErrors(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || len(resp.Data.JSErrors) == 0 {
		return mcp.NewToolResultText("No JavaScript errors captured")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "JavaScript errors (%d):\n", len(resp.Data.JSErrors))
	for _, e := range resp.Data.JSErrors {
		fmt.Fprintf(&sb, "  %s\n", e.Message)
		if e.URL != "" {
			fmt.Fprintf(&sb, "    at %s", e.URL)
			if e.LineNumber != nil {
				fmt.Fprintf(&sb, ":%d", *e.LineNumber)
			}
			fmt.Fprintln(&sb)
		}
	}
	return mcp.NewToolResultText(sb.String())
}
