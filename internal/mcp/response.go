package mcp

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	if resp == nil {
		return mcp.NewToolResultError("Command failed: no response from daemon")
	}
	if !resp.Success {
		if resp.Error == "" {
			return mcp.NewToolResultError("Command failed without error details")
		}
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

// formatSnapshot formats a snapshot response as text. When the response
// carries a SnapshotDiffData (i.e. the request set diff=true), the diff
// text is preferred over the full snapshot — including a baseline-reset
// header on the first call so callers know the next call is the real
// diff.
func formatSnapshot(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil {
		return mcp.NewToolResultText("(empty snapshot)")
	}
	if dd := resp.Data.SnapshotDiffData; dd != nil {
		if dd.BaselineReset {
			body := "(baseline reset — full snapshot follows)"
			if dd.Diff != "" {
				body += "\n" + dd.Diff
			}
			return mcp.NewToolResultText(body)
		}
		if dd.Diff == "" {
			return mcp.NewToolResultText("(no changes since last snapshot)")
		}
		return mcp.NewToolResultText(dd.Diff)
	}
	if resp.Data.SnapshotData == nil {
		return mcp.NewToolResultText("(empty snapshot)")
	}
	return mcp.NewToolResultText(resp.Data.SnapshotData.Snapshot)
}

// formatScreenshot returns a screenshot as an image content result.
func formatScreenshot(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || resp.Data.DataURL == "" {
		return mcp.NewToolResultText("Screenshot captured (no image data returned)")
	}
	base64Data, mimeType := parseImageDataURL(resp.Data.DataURL)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.ImageContent{
				Type:     "image",
				Data:     base64Data,
				MIMEType: mimeType,
			},
		},
	}
}

func parseImageDataURL(dataURL string) (data string, mimeType string) {
	const defaultMIME = "image/png"
	if !strings.HasPrefix(dataURL, "data:") {
		return dataURL, defaultMIME
	}
	header, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return dataURL, defaultMIME
	}
	meta := strings.TrimPrefix(header, "data:")
	parts := strings.Split(meta, ";")
	if len(parts) == 0 || parts[0] == "" {
		return data, defaultMIME
	}
	return data, parts[0]
}

func formatViewport(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || resp.Data.Viewport == nil {
		return mcp.NewToolResultText("Viewport updated")
	}
	vp := resp.Data.Viewport
	mode := "desktop"
	if vp.Mobile {
		mode = "mobile"
	}
	prefix := "Viewport"
	if vp.Reset {
		prefix = "Viewport reset"
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s: %dx%d @ %sx (%s, touch=%v)",
		prefix,
		vp.Width,
		vp.Height,
		strconv.FormatFloat(vp.DPR, 'f', -1, 64),
		mode,
		vp.Touch,
	))
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
	return mcp.NewToolResultText(formatJSONValue(resp.Data.Result))
}

// formatEvalRaw mirrors CLI --unwrap for tools that need an unquoted scalar.
func formatEvalRaw(resp *protocol.Response) *mcp.CallToolResult {
	if resp.Data == nil || resp.Data.Result == nil {
		return mcp.NewToolResultText("")
	}
	if s, ok := resp.Data.Result.(string); ok {
		return mcp.NewToolResultText(s)
	}
	return mcp.NewToolResultText(formatJSONValue(resp.Data.Result))
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
		return mcp.NewToolResultText(formatJSONValue(resp.Data.Result))
	}
	return mcp.NewToolResultText("")
}

func formatJSONValue(v interface{}) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(out)
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
