package main

import (
	"fmt"
	"sort"
	"strings"
)

// cmdHelp is the structured help for a single command.
type cmdHelp struct {
	Summary  string   // one-line description
	Usage    string   // e.g. "bb-browser open <url> [--new]"
	Flags    []string // aligned "  --foo <v>   description" lines
	Examples []string
	Notes    string // additional context (ref format, side effects, etc.)
}

// refNote is the standard paragraph about the <ref> argument, reused by
// every interaction command. Refs come from the accessibility snapshot.
const refNote = `<ref> is an element handle from 'bb-browser snapshot'. Snapshots render
elements as 'button [ref=5]'; pass "5" (or "@5") as <ref>. Refs are
regenerated on every snapshot — take a fresh snapshot after navigation
or any DOM change before interacting.`

// globalFlagsNote is the short summary of global flags shown in per-command help.
const globalFlagsNote = `Global flags (available on every command):
  --tab <id>              Target a specific tab (from 'bb-browser tab')
  --json                  Emit the raw JSON response instead of pretty output
  --jq <expr>             Filter JSON output with a jq expression (implies --json)
  --unwrap                For 'eval'/site adapters: print resp.data.result raw
                          (strings unquoted, other shapes as JSON)
  --since <seq|last_action>  Only include events newer than this (network/console/errors)`

// commandHelp indexes per-command help. Canonical commands are the keys; any
// subcommand shortcuts ("tab.new", "site.run", ...) are also listed so callers
// can look them up via 'bb-browser help <command> <sub>'.
var commandHelp = map[string]cmdHelp{
	// --- Navigation ---
	"open": {
		Summary: "Open a URL (reuses a tab with the same URL unless --new).",
		Usage:   "bb-browser open <url> [--new] [--tab <id>] [--wait-for <selector>] [--timeout <ms>]",
		Flags: []string{
			"  --new                   Force a new tab even if the URL is already open",
			"  --tab <id>              Navigate an existing tab instead of opening a new one",
			"  --wait-for <selector>   Block until document.querySelector(<selector>) is non-null",
			"  --timeout <ms>          Cap --wait-for (default 10000ms)",
		},
		Examples: []string{
			"  bb-browser open https://github.com",
			"  bb-browser open https://github.com --new",
			"  bb-browser open https://example.com/spa --wait-for '.article-content'",
			"  bb-browser open https://slow.example --wait-for '#root' --timeout 30000",
		},
	},
	"back":    {Summary: "Go back in the current tab's history.", Usage: "bb-browser back [--tab <id>]"},
	"forward": {Summary: "Go forward in the current tab's history.", Usage: "bb-browser forward [--tab <id>]"},
	"refresh": {Summary: "Reload the current page.", Usage: "bb-browser refresh [--tab <id>]"},
	"close": {
		Summary: "Close the current tab (or the tab named by --tab).",
		Usage:   "bb-browser close [--tab <id>]",
		Notes:   "To close a tab by index/ID use 'bb-browser tab close <n>'.",
	},

	// --- Interaction ---
	"click": {
		Summary:  "Click an element by ref.",
		Usage:    "bb-browser click <ref> [--tab <id>]",
		Examples: []string{"  bb-browser click 5"},
		Notes:    refNote,
	},
	"hover": {
		Summary:  "Hover an element by ref.",
		Usage:    "bb-browser hover <ref> [--tab <id>]",
		Examples: []string{"  bb-browser hover 12"},
		Notes:    refNote,
	},
	"fill": {
		Summary: "Clear an input/textarea and fill it with <text>.",
		Usage:   "bb-browser fill <ref> <text> [--tab <id>]",
		Examples: []string{
			"  bb-browser fill 3 'hello world'",
			"  bb-browser fill 3 multi word text (remaining args are joined)",
		},
		Notes: refNote + "\nUse 'type' to append without clearing.",
	},
	"type": {
		Summary:  "Append <text> to an input/textarea without clearing it first.",
		Usage:    "bb-browser type <ref> <text> [--tab <id>]",
		Examples: []string{"  bb-browser type 3 ' and more'"},
		Notes:    refNote + "\nUse 'fill' to clear the field before writing.",
	},
	"check": {
		Summary:  "Check a checkbox or radio by ref.",
		Usage:    "bb-browser check <ref> [--tab <id>]",
		Examples: []string{"  bb-browser check 7"},
		Notes:    refNote,
	},
	"uncheck": {
		Summary:  "Uncheck a checkbox by ref.",
		Usage:    "bb-browser uncheck <ref> [--tab <id>]",
		Examples: []string{"  bb-browser uncheck 7"},
		Notes:    refNote,
	},
	"select": {
		Summary:  "Select an <option> in a <select> element by value.",
		Usage:    "bb-browser select <ref> <value> [--tab <id>]",
		Examples: []string{"  bb-browser select 9 'us-east-1'"},
		Notes:    refNote,
	},
	"press": {
		Summary: "Dispatch a single key press to the active element.",
		Usage:   "bb-browser press <key> [--tab <id>]",
		Examples: []string{
			"  bb-browser press Enter",
			"  bb-browser press Escape",
			"  bb-browser press ArrowDown",
		},
		Notes: "Key names follow KeyboardEvent.key (e.g. 'Enter', 'Tab', 'ArrowLeft', 'a').",
	},
	"scroll": {
		Summary: "Scroll the page by pixels in a direction.",
		Usage:   "bb-browser scroll [direction] [pixels] [--tab <id>]",
		Flags: []string{
			"  direction    up|down|left|right (default: down)",
			"  pixels       integer pixel distance (default: 300)",
		},
		Examples: []string{
			"  bb-browser scroll",
			"  bb-browser scroll down 800",
			"  bb-browser scroll up 200",
		},
	},
	"eval": {
		Summary: "Run JavaScript in the page context and return the JSON result.",
		Usage:   "bb-browser eval <script...> [--file <path>] [--unwrap] [--no-auto-await] [--tab <id>]",
		Flags: []string{
			"  --file <path>      Read the script from a file instead of inline args",
			"  --unwrap           Print the result raw (strings unquoted, otherwise JSON)",
			"  --no-auto-await    Disable the auto-wrap of top-level `await` in an async IIFE",
		},
		Examples: []string{
			"  bb-browser eval 'document.title'",
			"  bb-browser eval --unwrap 'document.title'",
			"  bb-browser eval 'await fetch(\"/api/me\").then(r=>r.json())'",
			"  bb-browser eval --file ./extract.js",
		},
		Notes: "All remaining args are joined with spaces and evaluated as one expression.\n" +
			"By default, scripts that contain a top-level `await` are auto-wrapped in\n" +
			"`(async () => { return (<script>) })()` so the resolved value is returned\n" +
			"instead of `[object Promise]`. Use --no-auto-await to disable.\n" +
			"For authenticated HTTP calls prefer 'bb-browser fetch'.",
	},
	"wait": {
		Summary:  "Sleep for <ms> milliseconds (default 1000) without releasing the daemon.",
		Usage:    "bb-browser wait [ms] [--tab <id>]",
		Examples: []string{"  bb-browser wait 500"},
	},

	// --- Observation ---
	"snapshot": {
		Summary: "Emit the accessibility tree of the page with [ref=N] handles.",
		Usage:   "bb-browser snapshot [-i] [-c] [-d N] [-s <selector>] [--tab <id>]",
		Flags: []string{
			"  -i, --interactive   Include only clickable/fillable elements (much shorter)",
			"  -c, --compact       Collapse whitespace and redundant nesting",
			"  -d, --depth N       Limit tree depth to N levels",
			"  -s, --selector CSS  Snapshot only the subtree matching a CSS selector",
		},
		Examples: []string{
			"  bb-browser snapshot -i -c",
			"  bb-browser snapshot -d 4 -s '#app'",
		},
		Notes: "Always snapshot before calling interaction commands — refs are regenerated " +
			"on every snapshot and go stale across navigations or DOM updates.",
	},
	"screenshot": {
		Summary: "Capture a PNG of the current page.",
		Usage:   "bb-browser screenshot [path] [--tab <id>]",
		Examples: []string{
			"  bb-browser screenshot",
			"  bb-browser screenshot ./out.png",
		},
		Notes: "Without [path] the image is returned as a base64 data URL in the JSON payload.",
	},
	"get": {
		Summary: "Read a single attribute — page-level or from a ref.",
		Usage:   "bb-browser get <attribute> [ref] [--tab <id>]",
		Flags: []string{
			"  Page-level: url, title",
			"  Element:    text, value, href, html, <any DOM attribute> (requires <ref>)",
		},
		Examples: []string{
			"  bb-browser get url",
			"  bb-browser get title",
			"  bb-browser get text 5",
			"  bb-browser get href 12",
		},
		Notes: refNote,
	},
	"network": {
		Summary: "List or clear network requests captured for the current tab.",
		Usage:   "bb-browser network [requests|clear] [flags]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code>      Only requests whose response status matches <code>",
			"  --with-body          Include response bodies (heavier payload)",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
		},
		Examples: []string{
			"  bb-browser network",
			"  bb-browser network requests --filter /api/ --method POST",
			"  bb-browser network requests --since last_action",
			"  bb-browser network clear",
		},
	},
	"console": {
		Summary: "Read or clear captured console messages.",
		Usage:   "bb-browser console [--clear] [--filter <substr>] [--since <seq|last_action>]",
		Flags: []string{
			"  --clear               Drop all captured console messages for this tab",
			"  --filter <substr>     Only messages whose text contains <substr>",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
		},
		Examples: []string{
			"  bb-browser console",
			"  bb-browser console --filter error --since last_action",
		},
	},
	"errors": {
		Summary: "Read or clear captured uncaught JS errors.",
		Usage:   "bb-browser errors [--clear] [--filter <substr>] [--since <seq|last_action>]",
		Examples: []string{
			"  bb-browser errors",
			"  bb-browser errors --since last_action",
		},
	},
	"trace": {
		Summary: "Record the user's manual interactions as a replayable trace.",
		Usage:   "bb-browser trace [start|stop|status] [--tab <id>]",
		Flags: []string{
			"  start     Begin recording clicks, fills, presses, scrolls, navigations",
			"  stop      Stop recording and return the event list",
			"  status    Report whether recording is active and how many events captured (default)",
		},
	},
	"history": {
		Summary: "List the daemon's recent action history (ring buffer).",
		Usage:   "bb-browser history",
	},

	// --- Tabs / frames / dialogs ---
	"tab": {
		Summary: "List, create, switch between, or close Chrome tabs.",
		Usage:   "bb-browser tab [subcommand]",
		Flags: []string{
			"  (no subcommand)       List all tabs (default)",
			"  list                  Same as no subcommand",
			"  new [url]             Open a new tab (default url: about:blank)",
			"  <n>                   Switch to the tab at index <n>",
			"  select --id <id>      Switch to the tab with the given short id",
			"  select <n>            Switch to the tab at index <n>",
			"  close [n|--id <id>]   Close a tab by index or short id (default: active)",
		},
		Examples: []string{
			"  bb-browser tab",
			"  bb-browser tab new https://github.com",
			"  bb-browser tab 2",
			"  bb-browser tab select --id abc123",
			"  bb-browser tab close 3",
		},
	},
	"frame": {
		Summary: "Switch the interaction context to a child iframe, or back to the main frame.",
		Usage:   "bb-browser frame [main|<selector>]",
		Examples: []string{
			"  bb-browser frame 'iframe#checkout'",
			"  bb-browser frame main",
		},
	},
	"dialog": {
		Summary: "Pre-arm a handler for the next native alert/confirm/prompt/beforeunload.",
		Usage:   "bb-browser dialog [accept|dismiss] [prompt-text]",
		Examples: []string{
			"  bb-browser dialog accept",
			"  bb-browser dialog accept 'Leo'",
			"  bb-browser dialog dismiss",
		},
		Notes: "Run this BEFORE the click/navigation that triggers the dialog. " +
			"Default action is 'accept'. If accepting a prompt, pass the response text as the second arg.",
	},

	// --- Site adapters ---
	"site": {
		Summary: "Run or inspect platform-specific scrapers (twitter/search, hackernews/top, ...).",
		Usage:   "bb-browser site [subcommand]",
		Flags: []string{
			"  list                  List all adapters grouped by platform (default)",
			"  search <query>        Fuzzy-search adapters by name/description",
			"  info <name>           Show an adapter's args, domain, and example",
			"  update                Pull the latest community adapter pack",
			"  run <name> [args...]  Run an adapter (equivalent to 'bb-browser <name> ...')",
		},
		Examples: []string{
			"  bb-browser site list",
			"  bb-browser site info hackernews/top",
			"  bb-browser hackernews/top",
			"  bb-browser twitter/search 'claude code'",
		},
		Notes: "Any '<platform>/<adapter>' invocation is forwarded to 'site run' — " +
			"'bb-browser hackernews/top' and 'bb-browser site run hackernews/top' are equivalent. " +
			"Run 'bb-browser site info <name>' to see required args for each adapter.",
	},

	// --- Utility / infra ---
	"fetch": {
		Summary: "Issue an authenticated HTTP request from inside the page (inherits cookies).",
		Usage:   "bb-browser fetch <url> [--method <M>] [--tab <id>]",
		Flags: []string{
			"  --method <M>   HTTP method (default: GET)",
		},
		Examples: []string{
			"  bb-browser fetch https://api.github.com/user",
			"  bb-browser fetch https://example.com/api/x --method POST",
		},
		Notes: "Runs as fetch(url, {credentials:'include'}) in the tab, so session cookies, " +
			"auth headers, and CORS policy all apply. Body is returned as parsed JSON when the " +
			"response content-type is application/json, else as raw text.",
	},
	"status": {
		Summary: "Print the daemon status as JSON (uptime, tabs, CDP connection).",
		Usage:   "bb-browser status",
	},
	"daemon": {
		Summary: "Start or control the local daemon (loopback only).",
		Usage:   "bb-browser daemon [status|shutdown|stop] [--host H --port P --cdp-host H --cdp-port P]",
		Flags: []string{
			"  (no subcommand)        Start the daemon in the foreground",
			"  status                 Show JSON status (or 'not running')",
			"  shutdown|stop          Ask the running daemon to exit",
			"  --host <h>             Bind address (default 127.0.0.1)",
			"  --port <p>             Listen port (default 19824)",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (default 30, 0=disable; env BB_BROWSER_TAB_IDLE_TIMEOUT)",
		},
		Notes: "For a remote-accessible server with auth, use 'bb-browser server' instead.",
	},
	"server": {
		Summary: "Start the REST server (exposes /v1/* routes; requires a token when non-loopback).",
		Usage:   "bb-browser server [status|shutdown|stop] [--host H --port P --token T]",
		Flags: []string{
			"  (no subcommand)        Start the server in the foreground",
			"  status                 Show JSON status",
			"  shutdown|stop          Ask the running server to exit",
			"  --host <h>             Bind address (default 0.0.0.0)",
			"  --port <p>             Listen port (default 19824; env BB_BROWSER_SERVER_PORT)",
			"  --token <t>            Bearer token required for non-loopback binds",
			"                         (env BB_BROWSER_TOKEN)",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (default 30, 0=disable; env BB_BROWSER_TAB_IDLE_TIMEOUT)",
		},
		Examples: []string{
			"  bb-browser server --host 127.0.0.1",
			"  bb-browser server --host 0.0.0.0 --token \"$BB_BROWSER_TOKEN\"",
			"  bb-browser server shutdown",
		},
		Notes: "Clients authenticate with 'Authorization: Bearer <token>'. " +
			"Swagger UI is served at /docs and the OpenAPI spec at /openapi.json.",
	},
	"mcp": {
		Summary: "Speak MCP over stdio — intended to be spawned by an MCP-aware client.",
		Usage:   "bb-browser mcp",
		Notes:   "Humans rarely run this directly; configure it in your MCP client instead.",
	},
	"update": {
		Summary: "Download the latest release from GitHub and replace the running binary.",
		Usage:   "bb-browser update [--check] [--force]",
		Flags: []string{
			"  --check   Only report current vs latest version; do not download",
			"  --force   Reinstall even if already on the latest version",
		},
		Examples: []string{
			"  bb-browser update --check",
			"  bb-browser update",
		},
		Notes: "The binary replaces itself atomically via rename. " +
			"Verifies a SHA-256 checksum from the GitHub release assets.",
	},
	"help": {
		Summary: "Show help for bb-browser as a whole or a specific command.",
		Usage:   "bb-browser help [command [subcommand]]",
		Examples: []string{
			"  bb-browser help",
			"  bb-browser help snapshot",
			"  bb-browser help tab new",
			"  bb-browser snapshot --help",
			"  bb-browser tab new --help",
		},
	},
	"version": {
		Summary: "Print the version of this bb-browser binary.",
		Usage:   "bb-browser version",
	},

	// --- Subcommand pages: tab.* ---
	"tab.list": {
		Summary:  "List every open tab with title, URL, 1-based index, and short id.",
		Usage:    "bb-browser tab [list]",
		Examples: []string{"  bb-browser tab", "  bb-browser tab list"},
		Notes: "The active tab is marked with '*'. The short id shown in the last column is " +
			"what you pass to '--tab' or to 'tab select --id'.",
	},
	"tab.new": {
		Summary:  "Open a new tab, optionally pointed at a URL (default about:blank).",
		Usage:    "bb-browser tab new [url]",
		Examples: []string{"  bb-browser tab new", "  bb-browser tab new https://github.com"},
		Notes: "Unlike 'bb-browser open', this always creates a fresh tab and never reuses an " +
			"existing one. Use 'open --new' if you want the same force-new behaviour from the " +
			"navigation flow.",
	},
	"tab.select": {
		Summary: "Switch the active tab by index or short id.",
		Usage:   "bb-browser tab select <n|--id <short-id>>",
		Flags: []string{
			"  <n>               1-based index as shown by 'bb-browser tab'",
			"  --id <short-id>   Short tab id (also shown by 'bb-browser tab')",
		},
		Examples: []string{
			"  bb-browser tab 2            # shorthand, equivalent to 'tab select 2'",
			"  bb-browser tab select 2",
			"  bb-browser tab select --id abc123",
		},
	},
	"tab.close": {
		Summary: "Close a tab by index or short id (default: the currently active tab).",
		Usage:   "bb-browser tab close [n|--id <short-id>]",
		Examples: []string{
			"  bb-browser tab close",
			"  bb-browser tab close 3",
			"  bb-browser tab close --id abc123",
		},
	},

	// --- Subcommand pages: site.* ---
	"site.list": {
		Summary:  "List every available site adapter, grouped by platform.",
		Usage:    "bb-browser site [list]",
		Examples: []string{"  bb-browser site", "  bb-browser site list"},
		Notes: "Entries tagged [local] come from your workspace; the rest are from the " +
			"community pack. Use 'site update' to refresh the community pack.",
	},
	"site.search": {
		Summary:  "Fuzzy-search adapters by name, description, or domain.",
		Usage:    "bb-browser site search <query>",
		Examples: []string{"  bb-browser site search hacker", "  bb-browser site search 'linux forum'"},
	},
	"site.info": {
		Summary: "Print an adapter's description, domain, source, example, and args.",
		Usage:   "bb-browser site info <name>",
		Examples: []string{
			"  bb-browser site info hackernews/top",
			"  bb-browser hackernews/top --help   # shortcut that forwards here",
		},
		Notes: "'Args' in the output lists the positional arguments the adapter accepts. " +
			"'(required)' marks mandatory ones.",
	},
	"site.update": {
		Summary: "Pull the latest community adapter pack from GitHub.",
		Usage:   "bb-browser site update",
		Notes: "Community adapters are cached under the user's config dir. Local adapters " +
			"you've placed in the workspace are not affected.",
	},
	"site.run": {
		Summary:  "Run an adapter by name — equivalent to calling 'bb-browser <platform>/<name> ...' directly.",
		Usage:    "bb-browser site run <name> [args...] [--tab <id>]",
		Examples: []string{"  bb-browser site run hackernews/top 10", "  bb-browser hackernews/top 10"},
		Notes: "Use 'bb-browser site info <name>' to discover the args an adapter expects " +
			"before running it.",
	},

	// --- Subcommand pages: daemon.* ---
	"daemon.status": {
		Summary: "Print the daemon's JSON status, or 'Daemon is not running'.",
		Usage:   "bb-browser daemon status",
		Notes:   "Identical payload to the top-level 'bb-browser status'.",
	},
	"daemon.shutdown": {
		Summary:  "Ask the running daemon to exit cleanly.",
		Usage:    "bb-browser daemon shutdown",
		Examples: []string{"  bb-browser daemon shutdown", "  bb-browser daemon stop   # alias"},
	},
	"daemon.stop": {
		Summary:  "Alias for 'daemon shutdown'. Asks the running daemon to exit cleanly.",
		Usage:    "bb-browser daemon stop",
		Examples: []string{"  bb-browser daemon stop"},
	},

	// --- Subcommand pages: server.* ---
	"server.status": {
		Summary: "Print the server's JSON status, or 'Server is not running'.",
		Usage:   "bb-browser server status",
	},
	"server.shutdown": {
		Summary:  "Ask the running server to exit cleanly.",
		Usage:    "bb-browser server shutdown",
		Examples: []string{"  bb-browser server shutdown", "  bb-browser server stop   # alias"},
	},
	"server.stop": {
		Summary:  "Alias for 'server shutdown'. Asks the running server to exit cleanly.",
		Usage:    "bb-browser server stop",
		Examples: []string{"  bb-browser server stop"},
	},

	// --- Subcommand pages: trace.* ---
	"trace.start": {
		Summary: "Begin recording the user's manual clicks, fills, presses, scrolls, and navigations.",
		Usage:   "bb-browser trace start [--tab <id>]",
		Notes: "Nothing is returned until you call 'trace stop'. Use 'trace status' to confirm " +
			"recording is active.",
	},
	"trace.stop": {
		Summary: "Stop recording and return the captured event list as JSON.",
		Usage:   "bb-browser trace stop [--tab <id>]",
		Notes: "Output is intended for replay: each event has a type (click/fill/press/...), " +
			"timestamp, URL, and any type-specific fields (ref, text, pixels).",
	},
	"trace.status": {
		Summary: "Report whether recording is active and how many events are captured so far.",
		Usage:   "bb-browser trace status [--tab <id>]",
	},

	// --- Subcommand pages: network.* ---
	"network.requests": {
		Summary: "List network requests captured for the current tab.",
		Usage:   "bb-browser network requests [--filter S] [--method M] [--status C] [--with-body] [--since <seq|last_action>] [--tab <id>]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code>      Only requests whose response status matches <code>",
			"  --with-body          Include response bodies (heavier payload)",
			"  --since <seq|last_action>   Only requests newer than this checkpoint",
		},
		Examples: []string{
			"  bb-browser network requests",
			"  bb-browser network requests --filter /api/ --method POST",
			"  bb-browser network requests --since last_action --with-body",
		},
	},
	"network.clear": {
		Summary: "Drop all captured network requests for the current tab.",
		Usage:   "bb-browser network clear [--tab <id>]",
		Notes:   "Pair with 'network requests --since last_action' if you just want a fresh window.",
	},

	// --- Subcommand pages: dialog.* ---
	"dialog.accept": {
		Summary: "Pre-arm the next native dialog to be accepted (OK / Leave / prompt submitted).",
		Usage:   "bb-browser dialog accept [prompt-text] [--tab <id>]",
		Examples: []string{
			"  bb-browser dialog accept",
			"  bb-browser dialog accept 'Leo'       # prompt response text",
		},
		Notes: "Run BEFORE the click/navigation that triggers the dialog. For a prompt(), pass " +
			"the response as the second arg; for alert()/confirm() it is ignored.",
	},
	"dialog.dismiss": {
		Summary: "Pre-arm the next native dialog to be dismissed (Cancel / Stay on page).",
		Usage:   "bb-browser dialog dismiss [--tab <id>]",
		Notes:   "Run BEFORE the click/navigation that triggers the dialog.",
	},

	// --- Subcommand pages: frame.* ---
	"frame.main": {
		Summary:  "Switch the interaction context back to the page's top-level frame.",
		Usage:    "bb-browser frame main [--tab <id>]",
		Examples: []string{"  bb-browser frame main"},
	},
}

// helpAliases maps synonyms to canonical commandHelp keys.
var helpAliases = map[string]string{
	"--help":    "help",
	"-h":        "help",
	"--version": "version",
	"-v":        "version",
}

// printCommandHelp renders the help for a single command to stdout. If the
// command is unknown it falls back to the top-level help and returns false.
func printCommandHelp(name string) bool {
	if alias, ok := helpAliases[name]; ok {
		name = alias
	}
	h, ok := commandHelp[name]
	if !ok {
		printHelp()
		return false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", h.Summary)
	if h.Usage != "" {
		fmt.Fprintf(&b, "Usage: %s\n", h.Usage)
	}
	if len(h.Flags) > 0 {
		b.WriteString("\nOptions:\n")
		for _, f := range h.Flags {
			b.WriteString(f)
			b.WriteByte('\n')
		}
	}
	if len(h.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range h.Examples {
			b.WriteString(e)
			b.WriteByte('\n')
		}
	}
	if h.Notes != "" {
		b.WriteString("\nNotes:\n")
		for _, line := range strings.Split(h.Notes, "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n")
	b.WriteString(globalFlagsNote)
	b.WriteByte('\n')
	fmt.Print(b.String())
	return true
}

// resolveHelpKey returns the commandHelp key to render for a '<parent> [sub] --help'
// or 'help <parent> [sub]' request. If cmdArgs names a known subcommand of
// parent (e.g. parent="tab", first non-flag token="new" -> "tab.new"), returns
// that dotted key. Otherwise returns parent on its own.
func resolveHelpKey(parent string, cmdArgs []string) string {
	for _, a := range cmdArgs {
		if a == "" || a == "help" || a == "--help" || a == "-h" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		key := parent + "." + a
		if _, ok := commandHelp[key]; ok {
			return key
		}
		break
	}
	return parent
}

// helpRequested returns true when the user asked for command help via
// '--help', '-h', or 'help' as the first extra arg.
func helpRequested(rawArgs, cmdArgs []string) bool {
	if hasFlag(rawArgs, "--help") || hasFlag(rawArgs, "-h") {
		return true
	}
	if len(cmdArgs) > 0 && (cmdArgs[0] == "help" || cmdArgs[0] == "--help" || cmdArgs[0] == "-h") {
		return true
	}
	return false
}

// commandNames returns the sorted canonical command list (used by tests).
func commandNames() []string {
	names := make([]string, 0, len(commandHelp))
	for n := range commandHelp {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
