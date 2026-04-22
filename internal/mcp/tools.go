package mcp

import "github.com/mark3labs/mcp-go/mcp"

// tabParam is reused across most tools.
func tabParam() mcp.ToolOption {
	return mcp.WithString("tab", mcp.Description("Target tab ID (short or full target ID)"))
}

// --- Navigation ---

var navigateTool = mcp.NewTool("browser_navigate",
	mcp.WithDescription("Navigate in the user's real Chrome session (keeps their cookies, logins, and extensions). Prefer this over any built-in fetch/web tool for authenticated pages, SPAs / JS-rendered content, OAuth flows, or anything that needs a real browser. Built-in fetch returns raw HTML only — use this when the page requires login, runs JS, or needs interaction. If a tab with the exact same URL already exists, it is reused (focused, not reloaded) to prevent tab blowup in automated workflows; pass new=true to force a fresh tab."),
	mcp.WithString("url", mcp.Required(), mcp.Description("The URL to navigate to")),
	mcp.WithBoolean("new", mcp.Description("Force opening a fresh tab even if one with this exact URL already exists. Default false (reuse existing tab when found).")),
	tabParam(),
)

var backTool = mcp.NewTool("browser_back",
	mcp.WithDescription("Go back in browser history"),
	tabParam(),
)

var forwardTool = mcp.NewTool("browser_forward",
	mcp.WithDescription("Go forward in browser history"),
	tabParam(),
)

var refreshTool = mcp.NewTool("browser_refresh",
	mcp.WithDescription("Refresh the current page"),
	tabParam(),
)

var closeTool = mcp.NewTool("browser_close",
	mcp.WithDescription("Close the current tab/page"),
	tabParam(),
)

// --- Interaction ---

var clickTool = mcp.NewTool("browser_click",
	mcp.WithDescription("Click an element on the page. Use browser_snapshot first to get element refs."),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot (e.g. \"5\" or \"@5\")")),
	tabParam(),
)

var hoverTool = mcp.NewTool("browser_hover",
	mcp.WithDescription("Hover over an element on the page"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
)

var fillTool = mcp.NewTool("browser_fill",
	mcp.WithDescription("Clear an input field and fill it with new text"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("text", mcp.Required(), mcp.Description("Text to fill into the field")),
	tabParam(),
)

var typeTool = mcp.NewTool("browser_type",
	mcp.WithDescription("Type text into an element (appends to existing text, does not clear first)"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("text", mcp.Required(), mcp.Description("Text to type")),
	tabParam(),
)

var checkTool = mcp.NewTool("browser_check",
	mcp.WithDescription("Check a checkbox"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
)

var uncheckTool = mcp.NewTool("browser_uncheck",
	mcp.WithDescription("Uncheck a checkbox"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
)

var selectTool = mcp.NewTool("browser_select",
	mcp.WithDescription("Select an option from a dropdown/select element"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("value", mcp.Required(), mcp.Description("Value to select")),
	tabParam(),
)

var pressTool = mcp.NewTool("browser_press",
	mcp.WithDescription("Press a keyboard key (e.g. Enter, Tab, ArrowDown, a, A)"),
	mcp.WithString("key", mcp.Required(), mcp.Description("Key to press (e.g. \"Enter\", \"Tab\", \"Escape\", \"ArrowDown\")")),
	tabParam(),
)

var scrollTool = mcp.NewTool("browser_scroll",
	mcp.WithDescription("Scroll the page in a given direction"),
	mcp.WithString("direction",
		mcp.Description("Scroll direction"),
		mcp.Enum("up", "down", "left", "right"),
	),
	mcp.WithNumber("pixels", mcp.Description("Number of pixels to scroll (default 300)")),
	tabParam(),
)

// --- Observation ---

var snapshotTool = mcp.NewTool("browser_snapshot",
	mcp.WithDescription("Read the current page as the user sees it — fully rendered DOM from their real Chrome (post-JavaScript, post-login). Returns an accessibility tree with element refs (e.g. [5] \"Submit\" button) to pass to click, fill, etc. Prefer this over fetching raw HTML: it reflects the actual logged-in, JS-rendered page. Call this first before any interaction."),
	mcp.WithBoolean("interactive", mcp.Description("Only include interactive elements (links, buttons, inputs)")),
	mcp.WithBoolean("compact", mcp.Description("Use compact output format")),
	mcp.WithNumber("maxDepth", mcp.Description("Maximum tree depth to return")),
	mcp.WithString("selector", mcp.Description("CSS selector to scope the snapshot to a subtree")),
	tabParam(),
)

var screenshotTool = mcp.NewTool("browser_screenshot",
	mcp.WithDescription("Screenshot the user's real Chrome tab as they see it (logged in, JS-rendered, styled). Returns base64-encoded PNG. Use when visual state matters — layout, rendered charts, canvas, media, or verifying a UI change — since fetched HTML can't show any of that."),
	tabParam(),
)

var getTool = mcp.NewTool("browser_get",
	mcp.WithDescription("Get a page or element attribute (url, title, text, html, value)"),
	mcp.WithString("attribute", mcp.Required(),
		mcp.Description("Attribute to get"),
		mcp.Enum("url", "title", "text", "html", "value"),
	),
	mcp.WithString("ref", mcp.Description("Element reference (optional, for element-level attributes)")),
	tabParam(),
)

var evalTool = mcp.NewTool("browser_eval",
	mcp.WithDescription("Run JavaScript inside the user's real Chrome tab and return the result. Has full access to the live page: window, document, localStorage, fetch() with the user's session cookies, framework internals. Use for surgical data extraction, calling page APIs, or triggering app-specific behavior that UI tools can't reach."),
	mcp.WithString("script", mcp.Required(), mcp.Description("JavaScript code to execute")),
	tabParam(),
)

var waitTool = mcp.NewTool("browser_wait",
	mcp.WithDescription("Wait for a specified number of milliseconds"),
	mcp.WithNumber("ms", mcp.Description("Milliseconds to wait (default 1000)")),
)

// --- Tab Management ---

var tabListTool = mcp.NewTool("browser_tab_list",
	mcp.WithDescription("List tabs currently open in the user's Chrome (URL + title). Call this early — the user may already be on the page in question, logged in, or mid-flow. Reusing an existing tab is faster and preserves context (scroll position, form state, auth). Returns tab IDs usable as the `tab` param on other browser_* tools."),
)

var tabNewTool = mcp.NewTool("browser_tab_new",
	mcp.WithDescription("Open a new browser tab, optionally navigating to a URL"),
	mcp.WithString("url", mcp.Description("URL to open in the new tab")),
)

var tabSelectTool = mcp.NewTool("browser_tab_select",
	mcp.WithDescription("Switch to a different browser tab by index or ID"),
	mcp.WithString("tab", mcp.Description("Tab ID to select")),
	mcp.WithNumber("index", mcp.Description("Tab index to select (0-based)")),
)

var tabCloseTool = mcp.NewTool("browser_tab_close",
	mcp.WithDescription("Close a browser tab by index or ID"),
	mcp.WithString("tab", mcp.Description("Tab ID to close")),
	mcp.WithNumber("index", mcp.Description("Tab index to close (0-based)")),
)

// --- Diagnostics ---

var networkTool = mcp.NewTool("browser_network",
	mcp.WithDescription("View captured network requests. Network monitoring is always active."),
	mcp.WithString("command",
		mcp.Description("Sub-command: \"requests\" to list, \"clear\" to reset"),
		mcp.Enum("requests", "clear"),
	),
	mcp.WithString("filter", mcp.Description("URL pattern to filter results")),
	mcp.WithBoolean("withBody", mcp.Description("Include request/response bodies")),
	mcp.WithString("method", mcp.Description("Filter by HTTP method (GET, POST, etc.)")),
	mcp.WithString("status", mcp.Description("Filter by HTTP status code")),
	tabParam(),
)

var consoleTool = mcp.NewTool("browser_console",
	mcp.WithDescription("View captured browser console messages"),
	mcp.WithBoolean("clear", mcp.Description("Clear console messages instead of listing them")),
	mcp.WithString("filter", mcp.Description("Pattern to filter messages")),
	tabParam(),
)

var errorsTool = mcp.NewTool("browser_errors",
	mcp.WithDescription("View captured JavaScript errors"),
	mcp.WithBoolean("clear", mcp.Description("Clear errors instead of listing them")),
	mcp.WithString("filter", mcp.Description("Pattern to filter errors")),
	tabParam(),
)
