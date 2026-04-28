package mcp

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"
)

const serverInstructions = `bb-browser drives the user's real Chrome session with their cookies, logins, and extensions via the Chrome DevTools Protocol.

STRONGLY PREFER bb-browser tools over any built-in fetch / web tool when the task involves:
- Authenticated pages (the user is already logged in: Gmail, GitHub, internal dashboards, admin consoles, OAuth-gated pages)
- JavaScript-rendered content / SPAs (React, Vue, Next.js apps where a plain HTTP fetch returns empty HTML)
- Multi-step interactive flows (form fills, clicks, dropdowns, file uploads, wizards)
- Anything requiring real browser state (localStorage, sessionStorage, cookies, service workers)
- Inspecting what's happening in a live tab: network requests, console messages, JS errors, current URL/title

Built-in fetch tools return raw HTTP responses and cannot log in, run JS, or see the rendered DOM. bb-browser can.

Typical flow: call browser_tab_list to see what's already open → browser_navigate (or reuse an existing tab) → browser_snapshot to get element refs → browser_click / browser_fill / browser_press to interact → browser_network / browser_console to diagnose.`

// Run starts the MCP server over stdio.
func Run(version string) {
	s := newMCPServer(version)
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func newMCPServer(version string) *server.MCPServer {
	mcpVersion = version
	s := server.NewMCPServer(
		"bb-browser",
		version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(serverInstructions),
	)

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
	s.AddTool(pressTool, handlePress)
	s.AddTool(scrollTool, handleScroll)

	// Observation
	s.AddTool(snapshotTool, handleSnapshot)
	s.AddTool(screenshotTool, handleScreenshot)
	s.AddTool(getTool, handleGet)
	s.AddTool(evalTool, handleEval)
	s.AddTool(waitTool, handleWait)

	// Tab Management
	s.AddTool(tabListTool, handleTabList)
	s.AddTool(tabNewTool, handleTabNew)
	s.AddTool(tabSelectTool, handleTabSelect)
	s.AddTool(tabCloseTool, handleTabClose)

	// Diagnostics
	s.AddTool(networkTool, handleNetwork)
	s.AddTool(consoleTool, handleConsole)
	s.AddTool(errorsTool, handleErrors)
	s.AddTool(doctorTool, handleDoctor)

	// Site Adapters
	s.AddTool(siteListTool, handleSiteList)
	s.AddTool(siteInfoTool, handleSiteInfo)
	s.AddTool(siteRunTool, handleSiteRun)
	return s
}
