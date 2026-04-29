package mcp

import "github.com/mark3labs/mcp-go/mcp"

// tabParam is reused across most tools.
func tabParam() mcp.ToolOption {
	return mcp.WithString("tab", mcp.Description("Target tab ID (short or full target ID)"))
}

// waitForParam / timeoutParam expose the after-action wait-for / timeout
// options. The daemon polls document.querySelector(waitFor) on a 100ms tick
// after the action runs until non-null or timeout elapses (default 10000ms).
func waitForParam() mcp.ToolOption {
	return mcp.WithString("waitFor", mcp.Description("After the action runs, wait until document.querySelector(waitFor) returns a non-null node. Use for DOM changes (modal opened, content loaded) instead of fixed waits."))
}

func timeoutParam() mcp.ToolOption {
	return mcp.WithNumber("timeout", mcp.Description("Cap for waitFor in milliseconds (default 10000). Ignored without waitFor."))
}

// --- Navigation ---

var navigateTool = mcp.NewTool("browser_navigate",
	mcp.WithDescription("Navigate in the user's real Chrome session (keeps their cookies, logins, and extensions). Prefer this over any built-in fetch/web tool for authenticated pages, SPAs / JS-rendered content, OAuth flows, or anything that needs a real browser. Built-in fetch returns raw HTML only — use this when the page requires login, runs JS, or needs interaction. If a tab with the exact same URL already exists, it is reused (focused, not reloaded) to prevent tab blowup in automated workflows; pass new=true to force a fresh tab."),
	mcp.WithString("url", mcp.Required(), mcp.Description("The URL to navigate to")),
	mcp.WithBoolean("new", mcp.Description("Force opening a fresh tab even if one with this exact URL already exists. Default false (reuse existing tab when found).")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var backTool = mcp.NewTool("browser_back",
	mcp.WithDescription("Go back in browser history"),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var forwardTool = mcp.NewTool("browser_forward",
	mcp.WithDescription("Go forward in browser history"),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var refreshTool = mcp.NewTool("browser_refresh",
	mcp.WithDescription("Refresh the current page"),
	tabParam(),
	waitForParam(),
	timeoutParam(),
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
	waitForParam(),
	timeoutParam(),
)

var hoverTool = mcp.NewTool("browser_hover",
	mcp.WithDescription("Hover over an element on the page"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var fillTool = mcp.NewTool("browser_fill",
	mcp.WithDescription("Clear an input field and fill it with new text"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("text", mcp.Required(), mcp.Description("Text to fill into the field")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var typeTool = mcp.NewTool("browser_type",
	mcp.WithDescription("Type text into an element (appends to existing text, does not clear first)"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("text", mcp.Required(), mcp.Description("Text to type")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var checkTool = mcp.NewTool("browser_check",
	mcp.WithDescription("Check a checkbox"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var uncheckTool = mcp.NewTool("browser_uncheck",
	mcp.WithDescription("Uncheck a checkbox"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var selectTool = mcp.NewTool("browser_select",
	mcp.WithDescription("Select an option from a dropdown/select element"),
	mcp.WithString("ref", mcp.Required(), mcp.Description("Element reference from snapshot")),
	mcp.WithString("value", mcp.Required(), mcp.Description("Value to select")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var pressTool = mcp.NewTool("browser_press",
	mcp.WithDescription("Press a keyboard key (e.g. Enter, Tab, ArrowDown, a, A)"),
	mcp.WithString("key", mcp.Required(), mcp.Description("Key to press (e.g. \"Enter\", \"Tab\", \"Escape\", \"ArrowDown\")")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

var scrollTool = mcp.NewTool("browser_scroll",
	mcp.WithDescription("Scroll the page in a given direction"),
	mcp.WithString("direction",
		mcp.Description("Scroll direction"),
		mcp.Enum("up", "down", "left", "right"),
	),
	mcp.WithNumber("pixels", mcp.Description("Number of pixels to scroll (default 300)")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
)

// --- Observation ---

var snapshotTool = mcp.NewTool("browser_snapshot",
	mcp.WithDescription("Read the current page as the user sees it — fully rendered DOM from their real Chrome (post-JavaScript, post-login). Returns an accessibility tree with element refs (e.g. [5] \"Submit\" button) to pass to click, fill, etc. Prefer this over fetching raw HTML: it reflects the actual logged-in, JS-rendered page. Call this first before any interaction."),
	mcp.WithBoolean("interactive", mcp.Description("Only include interactive elements (links, buttons, inputs)")),
	mcp.WithBoolean("compact", mcp.Description("Use compact output format")),
	mcp.WithNumber("maxDepth", mcp.Description("Maximum tree depth to return")),
	mcp.WithString("selector", mcp.Description("Case-insensitive substring match across tag/role/name/xpath/attribute values. Not CSS. Filters elements; pass e.g. \"submit\" to narrow the snapshot. Combine with role for precision.")),
	mcp.WithString("role", mcp.Description("Filter to elements of an exact accessibility role (case-insensitive), e.g. \"button\", \"textbox\", \"link\". AND'd with selector.")),
	mcp.WithBoolean("textOnly", mcp.Description("Reader-mode output: title + URL + visible page text only (nav/header/footer/script/style stripped). No element refs are produced — use for summarization or LLM context, not before interaction.")),
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
	mcp.WithDescription("Run JavaScript inside the user's real Chrome tab and return the result. Has full access to the live page: window, document, localStorage, fetch() with the user's session cookies, framework internals. Use for surgical data extraction, calling page APIs, or triggering app-specific behavior that UI tools can't reach. Top-level `await` is auto-wrapped in an async IIFE — pass noAutoAwait=true to opt out."),
	mcp.WithString("script", mcp.Required(), mcp.Description("JavaScript code to execute")),
	mcp.WithBoolean("noAutoAwait", mcp.Description("Disable auto-wrapping of top-level await. Default false.")),
	tabParam(),
	waitForParam(),
	timeoutParam(),
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

var doctorTool = mcp.NewTool("browser_doctor",
	mcp.WithDescription("Run end-to-end diagnostics on the bb-browser stack (binary → daemon JSON → daemon process → daemon HTTP → CDP attach → tabs). Use when something isn't working and it's unclear which layer is broken; returns the first failing layer with a remediation hint."),
	mcp.WithBoolean("json", mcp.Description("Return structured JSON {ok, checks[]} instead of the human-readable report.")),
)

// --- Extension-backed browser APIs ---

var extensionStatusTool = mcp.NewTool("browser_extension_status",
	mcp.WithDescription("Show whether the optional bb-browser Chrome extension is connected and which Chrome-only APIs it exposes (cookies, bookmarks, history, downloads, windows, tabs/events). Use this when an extension-backed tool reports that no extension is connected."),
)

var extensionCallTool = mcp.NewTool("browser_extension_call",
	mcp.WithDescription("Advanced escape hatch: call a supported Chrome extension RPC method directly. Methods include cookies.getAll, bookmarks.*, history.*, downloads.*, windows.*, tabs.*, tabGroups.*. Prefer the structured tools when possible."),
	mcp.WithString("method", mcp.Required(), mcp.Description("Extension RPC method, e.g. bookmarks.search or downloads.search")),
	mcp.WithObject("params", mcp.Description("Method parameters as a JSON object")),
)

var bookmarksTool = mcp.NewTool("browser_bookmarks",
	mcp.WithDescription("Read or manage Chrome bookmarks through the bb-browser extension. CDP cannot access the browser bookmark store."),
	mcp.WithString("command", mcp.Description("tree, search, create, update, or remove"), mcp.Enum("tree", "search", "create", "update", "remove")),
	mcp.WithString("query", mcp.Description("Search query for command=search")),
	mcp.WithString("id", mcp.Description("Bookmark/folder ID for update/remove")),
	mcp.WithString("url", mcp.Description("Bookmark URL for create/update")),
	mcp.WithString("title", mcp.Description("Bookmark title for create/update")),
	mcp.WithString("parentId", mcp.Description("Parent folder ID for create")),
	mcp.WithBoolean("recursive", mcp.Description("Remove a folder recursively")),
)

var browserHistoryTool = mcp.NewTool("browser_history",
	mcp.WithDescription("Search or delete Chrome browsing history through the bb-browser extension. This is browser-level history, not bb-browser's daemon action history."),
	mcp.WithString("command", mcp.Description("search or deleteUrl"), mcp.Enum("search", "deleteUrl")),
	mcp.WithString("query", mcp.Description("Search query for command=search")),
	mcp.WithString("url", mcp.Description("URL for command=deleteUrl")),
	mcp.WithNumber("limit", mcp.Description("Maximum history results")),
)

var downloadsTool = mcp.NewTool("browser_downloads",
	mcp.WithDescription("Inspect or control Chrome downloads through the bb-browser extension. CDP cannot see the full browser download manager."),
	mcp.WithString("command", mcp.Description("list, search, start, erase, cancel, pause, resume, show, or showFolder"), mcp.Enum("list", "search", "start", "erase", "cancel", "pause", "resume", "show", "showFolder")),
	mcp.WithString("query", mcp.Description("Search/erase query")),
	mcp.WithString("url", mcp.Description("URL for command=start")),
	mcp.WithString("filename", mcp.Description("Suggested filename for command=start")),
	mcp.WithString("state", mcp.Description("Filter by state for list/search")),
	mcp.WithNumber("id", mcp.Description("Download ID for id-based commands")),
	mcp.WithNumber("limit", mcp.Description("Maximum results for list/search")),
	mcp.WithBoolean("saveAs", mcp.Description("Show Chrome Save As dialog for command=start")),
)

var windowsTool = mcp.NewTool("browser_windows",
	mcp.WithDescription("List and control Chrome browser windows through the bb-browser extension. Useful for focusing or creating windows, which CDP cannot reliably manage at the browser UI level."),
	mcp.WithString("command", mcp.Description("list, new, focus, or close"), mcp.Enum("list", "new", "focus", "close")),
	mcp.WithNumber("id", mcp.Description("Window ID for focus/close")),
	mcp.WithString("url", mcp.Description("URL to open for command=new")),
	mcp.WithBoolean("focused", mcp.Description("Focus the new window for command=new")),
)

// --- Site Adapters ---

var siteListTool = mcp.NewTool("browser_site_list",
	mcp.WithDescription("List available site adapters — JavaScript plugins that automate interactions with specific websites (e.g. twitter/search). Returns adapter names, descriptions, domains, and argument schemas. Adapters are resolved on the bb-browser daemon's filesystem."),
)

var siteInfoTool = mcp.NewTool("browser_site_info",
	mcp.WithDescription("Get detailed metadata for a site adapter, including its argument schema and example usage."),
	mcp.WithString("name", mcp.Required(), mcp.Description("Adapter name, e.g. \"twitter/search\"")),
)

var siteRunTool = mcp.NewTool("browser_site_run",
	mcp.WithDescription("Run a site adapter in the user's real Chrome tab. The adapter's JavaScript runs with the user's session (cookies, logins). Call browser_site_list first to discover adapters and their required arguments."),
	mcp.WithString("name", mcp.Required(), mcp.Description("Adapter name, e.g. \"twitter/search\"")),
	mcp.WithObject("args", mcp.Description("Adapter arguments as a JSON object (e.g. {\"query\": \"AI news\"})")),
	tabParam(),
)
