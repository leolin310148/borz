package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/observability"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const serverInstructions = `borz drives the user's real Chrome session with their cookies, logins, and extensions via the Chrome DevTools Protocol.

STRONGLY PREFER borz tools over any built-in fetch / web tool when the task involves:
- Authenticated pages (the user is already logged in: Gmail, GitHub, internal dashboards, admin consoles, OAuth-gated pages)
- JavaScript-rendered content / SPAs (React, Vue, Next.js apps where a plain HTTP fetch returns empty HTML)
- Multi-step interactive flows (form fills, clicks, dropdowns, file uploads, wizards)
- Anything requiring real browser state (localStorage, sessionStorage, cookies, service workers)
- Inspecting what's happening in a live tab: network requests, console messages, JS errors, current URL/title

Built-in fetch tools return raw HTTP responses and cannot log in, run JS, or see the rendered DOM. borz can.

Typical flow: call browser_tab_list to see what's already open → browser_navigate (or reuse an existing tab) → browser_snapshot to get element refs → browser_click / browser_fill / browser_press to interact → browser_network / browser_console to diagnose.`

// Run starts the MCP server over stdio.
func Run(version string) {
	sessionID := strings.TrimSpace(os.Getenv("BORZ_SESSION_ID"))
	if sessionID == "" {
		sessionID = newID()
	}
	client.SetRequestContext("mcp", sessionID)
	logger, logErr := observability.Open("mcp", version)
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "borz MCP operational log unavailable: %v\n", logErr)
	} else {
		mcpLogger = logger
		mcpSessionID = sessionID
		_ = logger.Log("info", "mcp_started", observability.Fields{SessionID: sessionID, Surface: "mcp"})
		defer func() {
			_ = logger.Log("info", "mcp_stopped", observability.Fields{SessionID: sessionID, Surface: "mcp"})
			_ = logger.Close()
			mcpLogger = nil
			mcpSessionID = ""
		}()
	}
	s := newMCPServer(version)
	if err := server.ServeStdio(s); err != nil {
		if mcpLogger != nil {
			_ = mcpLogger.Log("error", "mcp_failed", observability.Fields{SessionID: sessionID, Surface: "mcp", ErrorCode: observability.ErrorCode(err.Error())})
		}
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

var (
	mcpLogger    *observability.Logger
	mcpSessionID string
)

func newMCPServer(version string) *server.MCPServer {
	mcpVersion = version
	options := []server.ServerOption{
		server.WithToolCapabilities(false),
		server.WithInstructions(serverInstructions),
	}
	if mcpLogger != nil {
		options = append(options, server.WithToolHandlerMiddleware(mcpLoggingMiddleware(mcpLogger, mcpSessionID)))
	}
	options = append(options, server.WithRecovery())
	s := server.NewMCPServer("borz", version, options...)

	// Navigation
	s.AddTool(navigateTool, handleNavigate)
	s.AddTool(backTool, handleBack)
	s.AddTool(forwardTool, handleForward)
	s.AddTool(refreshTool, handleRefresh)
	s.AddTool(closeTool, handleClose)

	// Interaction
	s.AddTool(clickTool, handleClick)
	s.AddTool(hoverTool, handleHover)
	s.AddTool(fillTool, handleFill)
	s.AddTool(typeTool, handleType)
	s.AddTool(checkTool, handleCheck)
	s.AddTool(uncheckTool, handleUncheck)
	s.AddTool(selectTool, handleSelect)
	s.AddTool(uploadTool, handleUpload)
	s.AddTool(fileChooserTool, handleFileChooser)
	s.AddTool(dialogTool, handleDialog)
	s.AddTool(pressTool, handlePress)
	s.AddTool(pageVisibilityTool, handlePageVisibility)
	s.AddTool(webAuthnTool, handleWebAuthn)
	s.AddTool(clipboardWriteTool, handleClipboardWrite)
	s.AddTool(scrollTool, handleScroll)

	// Observation
	s.AddTool(snapshotTool, handleSnapshot)
	s.AddTool(screenshotTool, handleScreenshot)
	s.AddTool(viewportTool, handleViewport)
	s.AddTool(getTool, handleGet)
	s.AddTool(evalTool, handleEval)
	s.AddTool(termTextTool, handleTermText)
	s.AddTool(waitTool, handleWait)

	// Tab Management
	s.AddTool(tabListTool, handleTabList)
	s.AddTool(tabNewTool, handleTabNew)
	s.AddTool(tabSelectTool, handleTabSelect)
	s.AddTool(tabFrontTool, handleTabFront)
	s.AddTool(tabCloseTool, handleTabClose)

	// Diagnostics
	s.AddTool(networkTool, handleNetwork)
	s.AddTool(consoleTool, handleConsole)
	s.AddTool(errorsTool, handleErrors)
	s.AddTool(doctorTool, handleDoctor)

	// Extension-backed browser APIs
	s.AddTool(extensionStatusTool, handleExtensionStatus)
	s.AddTool(extensionCallTool, handleExtensionCall)
	s.AddTool(bookmarksTool, handleBookmarks)
	s.AddTool(browserHistoryTool, handleBrowserHistory)
	s.AddTool(downloadsTool, handleDownloads)
	s.AddTool(windowsTool, handleWindows)

	// Site Adapters
	s.AddTool(siteListTool, handleSiteList)
	s.AddTool(siteInfoTool, handleSiteInfo)
	s.AddTool(siteRunTool, handleSiteRun)
	return s
}

func mcpLoggingMiddleware(logger *observability.Logger, sessionID string) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			started := time.Now()
			result, err := next(ctx, request)
			success := err == nil && (result == nil || !result.IsError)
			errorText := ""
			if err != nil {
				errorText = err.Error()
			} else if result != nil && result.IsError {
				errorText = firstToolResultText(result)
			}
			level := "info"
			if !success {
				level = "warn"
			}
			fields := observability.Fields{
				SessionID: sessionID, Surface: "mcp", Tool: request.Params.Name,
				DurationMS: time.Since(started).Milliseconds(), Success: mcpBoolPtr(success),
			}
			if !success {
				fields.ErrorCode = observability.ErrorCode(errorText)
			}
			_ = logger.Log(level, "tool_completed", fields)
			return result, err
		}
	}
}

func firstToolResultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}

func mcpBoolPtr(v bool) *bool { return &v }
