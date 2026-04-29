package main

import (
	"fmt"
	"sort"
	"strings"
)

// cmdHelp is the structured help for a single command.
type cmdHelp struct {
	Summary  string   // one-line description
	Usage    string   // e.g. "borz open <url> [--new]"
	Flags    []string // aligned "  --foo <v>   description" lines
	Examples []string
	Notes    string // additional context (ref format, side effects, etc.)
}

// refNote is the standard paragraph about the <ref> argument, reused by
// every interaction command. Refs come from the accessibility snapshot.
const refNote = `<ref> is an element handle from 'borz snapshot'. Snapshots render
elements as 'button [ref=5]'; pass "5" (or "@5") as <ref>. Refs are
regenerated on every snapshot — take a fresh snapshot after navigation
or any DOM change before interacting.`

// globalFlagsNote is the short summary of global flags shown in per-command help.
const globalFlagsNote = `Global flags (available on every command):
  --remote                Route browser commands/status to configured server
  --tab <id>              Target a specific tab (from 'borz tab')
  --json                  Emit the raw JSON response instead of pretty output
  --jq <expr>             Filter JSON output with a jq expression (implies --json)
  --unwrap                For 'eval'/site adapters: print resp.data.result raw
                          (strings unquoted, other shapes as JSON)
  --since <seq|last_action>  Only include events newer than this (network/console/errors)

Wait-for flags (open, back, forward, refresh, click, hover, fill, type,
check, uncheck, select, press, scroll, eval):
  --wait-for <selector>   Block until document.querySelector(<selector>) is non-null
  --timeout <ms>          Cap --wait-for (default 10000ms)`

// commandHelp indexes per-command help. Canonical commands are the keys; any
// subcommand shortcuts ("tab.new", "site.run", ...) are also listed so callers
// can look them up via 'borz help <command> <sub>'.
var commandHelp = map[string]cmdHelp{
	// --- Navigation ---
	"open": {
		Summary: "Open a URL (reuses a tab with the same URL unless --new).",
		Usage:   "borz open <url> [--new] [--tab <id>] [--wait-for <selector>] [--timeout <ms>]",
		Flags: []string{
			"  --new                   Force a new tab even if the URL is already open",
			"  --tab <id>              Navigate an existing tab instead of opening a new one",
			"  --wait-for <selector>   Block until document.querySelector(<selector>) is non-null",
			"  --timeout <ms>          Cap --wait-for (default 10000ms)",
		},
		Examples: []string{
			"  borz open https://github.com",
			"  borz open https://github.com --new",
			"  borz open https://example.com/spa --wait-for '.article-content'",
			"  borz open https://slow.example --wait-for '#root' --timeout 30000",
		},
	},
	"back":    {Summary: "Go back in the current tab's history.", Usage: "borz back [--tab <id>]"},
	"forward": {Summary: "Go forward in the current tab's history.", Usage: "borz forward [--tab <id>]"},
	"refresh": {Summary: "Reload the current page.", Usage: "borz refresh [--tab <id>]"},
	"close": {
		Summary: "Close the current tab (or the tab named by --tab).",
		Usage:   "borz close [--tab <id>]",
		Notes:   "To close a tab by index/ID use 'borz tab close <n>'.",
	},

	// --- Interaction ---
	"click": {
		Summary:  "Click an element by ref.",
		Usage:    "borz click <ref> [--tab <id>]",
		Examples: []string{"  borz click 5"},
		Notes:    refNote,
	},
	"hover": {
		Summary:  "Hover an element by ref.",
		Usage:    "borz hover <ref> [--tab <id>]",
		Examples: []string{"  borz hover 12"},
		Notes:    refNote,
	},
	"fill": {
		Summary: "Clear an input/textarea and fill it with <text>.",
		Usage:   "borz fill <ref> <text> [--tab <id>]",
		Examples: []string{
			"  borz fill 3 'hello world'",
			"  borz fill 3 multi word text (remaining args are joined)",
		},
		Notes: refNote + "\nUse 'type' to append without clearing.",
	},
	"type": {
		Summary:  "Append <text> to an input/textarea without clearing it first.",
		Usage:    "borz type <ref> <text> [--tab <id>]",
		Examples: []string{"  borz type 3 ' and more'"},
		Notes:    refNote + "\nUse 'fill' to clear the field before writing.",
	},
	"check": {
		Summary:  "Check a checkbox or radio by ref.",
		Usage:    "borz check <ref> [--tab <id>]",
		Examples: []string{"  borz check 7"},
		Notes:    refNote,
	},
	"uncheck": {
		Summary:  "Uncheck a checkbox by ref.",
		Usage:    "borz uncheck <ref> [--tab <id>]",
		Examples: []string{"  borz uncheck 7"},
		Notes:    refNote,
	},
	"select": {
		Summary:  "Select an <option> in a <select> element by value.",
		Usage:    "borz select <ref> <value> [--tab <id>]",
		Examples: []string{"  borz select 9 'us-east-1'"},
		Notes:    refNote,
	},
	"press": {
		Summary: "Dispatch a single key press to the active element.",
		Usage:   "borz press <key> [--tab <id>]",
		Examples: []string{
			"  borz press Enter",
			"  borz press Escape",
			"  borz press ArrowDown",
		},
		Notes: "Key names follow KeyboardEvent.key (e.g. 'Enter', 'Tab', 'ArrowLeft', 'a').",
	},
	"scroll": {
		Summary: "Scroll the page by pixels in a direction.",
		Usage:   "borz scroll [direction] [pixels] [--tab <id>]",
		Flags: []string{
			"  direction    up|down|left|right (default: down)",
			"  pixels       integer pixel distance (default: 300)",
		},
		Examples: []string{
			"  borz scroll",
			"  borz scroll down 800",
			"  borz scroll up 200",
		},
	},
	"eval": {
		Summary: "Run JavaScript in the page context and return the JSON result.",
		Usage:   "borz eval <script...> [--file <path>] [--unwrap] [--no-auto-await] [--json-arg name=value]... [--tab <id>]",
		Flags: []string{
			"  --file <path>            Read the script from a file instead of inline args",
			"  --unwrap                 Print the result raw (strings unquoted, otherwise JSON)",
			"  --no-auto-await          Disable the auto-wrap of top-level `await` in an async IIFE",
			"  --json-arg name=value    Inject a JSON value as a top-level `const` (repeatable)",
		},
		Examples: []string{
			"  borz eval 'document.title'",
			"  borz eval --unwrap 'document.title'",
			"  borz eval 'await fetch(\"/api/me\").then(r=>r.json())'",
			"  borz eval --file ./extract.js",
			"  borz eval --file ./greet.js --json-arg user='{\"id\":7}' --json-arg n=3",
		},
		Notes: "All remaining args are joined with spaces and evaluated as one expression.\n" +
			"By default, scripts that contain a top-level `await` are auto-wrapped in\n" +
			"`(async () => { return (<script>) })()` so the resolved value is returned\n" +
			"instead of `[object Promise]`. Use --no-auto-await to disable.\n" +
			"--json-arg may be repeated; each value is parsed as JSON and prepended as\n" +
			"`const NAME = VALUE;` so --file scripts can read CLI inputs without templating.\n" +
			"For authenticated HTTP calls prefer 'borz fetch'.",
	},
	"wait": {
		Summary:  "Sleep for <ms> milliseconds (default 1000) without releasing the daemon.",
		Usage:    "borz wait [ms] [--tab <id>]",
		Examples: []string{"  borz wait 500"},
	},

	// --- Observation ---
	"snapshot": {
		Summary: "Emit the accessibility tree of the page with [ref=N] handles.",
		Usage:   "borz snapshot [-i] [-c] [-d N] [-s <selector>] [--text-only] [--tab <id>]",
		Flags: []string{
			"  -i, --interactive   Include only clickable/fillable elements (much shorter)",
			"  -c, --compact       Collapse whitespace and redundant nesting",
			"  -d, --depth N       Limit tree depth to N levels",
			"  -s, --selector CSS  Snapshot only the subtree matching a CSS selector",
			"  --text-only         Reader-mode plain text (no refs, no tree); good for LLM context",
		},
		Examples: []string{
			"  borz snapshot -i -c",
			"  borz snapshot -d 4 -s '#app'",
			"  borz snapshot --text-only",
		},
		Notes: "Always snapshot before calling interaction commands — refs are regenerated " +
			"on every snapshot and go stale across navigations or DOM updates.\n" +
			"--text-only strips nav/header/footer/script/style and returns the visible text " +
			"(plus title and URL); refs are NOT produced, so follow with a normal snapshot " +
			"before any click/fill/etc.",
	},
	"screenshot": {
		Summary: "Capture a PNG of the current page.",
		Usage:   "borz screenshot [path] [--tab <id>]",
		Examples: []string{
			"  borz screenshot",
			"  borz screenshot ./out.png",
		},
		Notes: "With [path], the CLI writes the PNG on the machine running the CLI, including when --remote is used. Without [path] the image is returned as a base64 data URL in the JSON payload.",
	},
	"get": {
		Summary: "Read a single attribute — page-level or from a ref.",
		Usage:   "borz get <attribute> [ref] [--tab <id>]",
		Flags: []string{
			"  Page-level: url, title",
			"  Element:    text, value, href, html, <any DOM attribute> (requires <ref>)",
		},
		Examples: []string{
			"  borz get url",
			"  borz get title",
			"  borz get text 5",
			"  borz get href 12",
		},
		Notes: refNote,
	},
	"network": {
		Summary: "List, clear, or live-tail network requests captured for the current tab.",
		Usage:   "borz network [requests|clear] [--tail] [--interval <ms>] [flags]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code>      Only requests whose response status matches <code>",
			"  --with-body          Include response bodies (heavier payload)",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
			"  --tail               Stream new requests as they arrive (Ctrl+C to stop)",
			"  --interval <ms>      Polling interval in --tail mode (default 500)",
		},
		Examples: []string{
			"  borz network",
			"  borz network requests --filter /api/ --method POST",
			"  borz network requests --since last_action",
			"  borz network requests --tail --filter /api/",
			"  borz network --tail --json | jq -c 'select(.status>=400)'",
			"  borz network clear",
		},
		Notes: "--tail polls the daemon every --interval ms, advancing the cursor so each\n" +
			"request is printed at most once. Combine with --json for JSONL output suitable\n" +
			"for piping into jq -c, or with --filter/--method/--status to narrow the stream.",
	},
	"console": {
		Summary: "Read or clear captured console messages.",
		Usage:   "borz console [--clear] [--filter <substr>] [--since <seq|last_action>]",
		Flags: []string{
			"  --clear               Drop all captured console messages for this tab",
			"  --filter <substr>     Only messages whose text contains <substr>",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
		},
		Examples: []string{
			"  borz console",
			"  borz console --filter error --since last_action",
		},
	},
	"errors": {
		Summary: "Read or clear captured uncaught JS errors.",
		Usage:   "borz errors [--clear] [--filter <substr>] [--since <seq|last_action>]",
		Examples: []string{
			"  borz errors",
			"  borz errors --since last_action",
		},
	},
	"trace": {
		Summary: "Record the user's manual interactions as a replayable trace.",
		Usage:   "borz trace [start|stop|status] [--tab <id>]",
		Flags: []string{
			"  start     Begin recording clicks, fills, presses, scrolls, navigations",
			"  stop      Stop recording and return the event list",
			"  status    Report whether recording is active and how many events captured (default)",
		},
	},
	"history": {
		Summary: "List the daemon's recent action history (ring buffer).",
		Usage:   "borz history",
	},

	// --- Tabs / frames / dialogs ---
	"tab": {
		Summary: "List, create, switch between, or close Chrome tabs.",
		Usage:   "borz tab [subcommand]",
		Flags: []string{
			"  (no subcommand)       List all tabs (default)",
			"  list                  Same as no subcommand",
			"  new [url]             Open a new tab (default url: about:blank)",
			"  <n>                   Switch to the tab at index <n>",
			"  select --id <id>      Switch to the tab with the given short id",
			"  select <n>            Switch to the tab at index <n>",
			"  close [n|--id <id>]   Close a tab by index or short id (default: active)",
			"  events [--tail]       Browser-level tab events (created/removed/updated/activated)",
		},
		Examples: []string{
			"  borz tab",
			"  borz tab new https://github.com",
			"  borz tab 2",
			"  borz tab select --id abc123",
			"  borz tab close 3",
			"  borz tab events --tail",
		},
		Notes: "'events' requires the borz Chrome extension to be installed and connected. " +
			"It surfaces browser-level events (Chrome tab/window lifecycle) that CDP cannot observe.",
	},
	"cookies": {
		Summary: "Read cookies the browser has stored, across every domain.",
		Usage:   "borz cookies [all] [domain-filter]",
		Flags: []string{
			"  all [domain]          Dump cookies for every domain, optionally filtered",
		},
		Examples: []string{
			"  borz cookies all",
			"  borz cookies all github.com",
			"  borz cookies all --json",
		},
		Notes: "Requires the borz Chrome extension. CDP can only return cookies " +
			"scoped to the active page; the extension exposes cookies across all domains.",
	},
	"bookmarks": {
		Summary: "Read and manage Chrome bookmarks through the borz extension.",
		Usage:   "borz bookmarks [tree|search|create|update|remove]",
		Flags: []string{
			"  tree                         Print the full bookmark tree (default)",
			"  search <query>               Search bookmarks by title or URL",
			"  create <url> <title>         Create a bookmark",
			"  update <id> [--title T] [--url U]",
			"  remove <id> [--recursive]    Remove a bookmark or folder",
		},
		Examples: []string{
			"  borz bookmarks tree",
			"  borz bookmarks search github",
			"  borz bookmarks create https://example.com Example --parent 1",
		},
		Notes: "Requires the Chrome extension. This uses chrome.bookmarks, a browser-level API CDP cannot access.",
	},
	"browser-history": {
		Summary: "Search or delete Chrome browsing history through the extension.",
		Usage:   "borz browser-history [search|delete-url]",
		Flags: []string{
			"  search [query] [--limit N]   Search browser history (default)",
			"  delete-url <url>             Delete one URL from browser history",
		},
		Examples: []string{
			"  borz browser-history search github --limit 20",
			"  borz browser-history delete-url https://example.com",
		},
		Notes: "Named browser-history to avoid changing the existing 'history' command, which shows borz daemon action history.",
	},
	"downloads": {
		Summary: "Inspect and control Chrome downloads through the extension.",
		Usage:   "borz downloads [list|search|start|erase|cancel|pause|resume|show|show-folder]",
		Flags: []string{
			"  list [--limit N] [--state S]     List downloads (default)",
			"  search <query> [--limit N]       Search downloads",
			"  start <url> [--filename P] [--save-as]",
			"  erase [--id N|query]             Erase download records",
			"  cancel|pause|resume|show <id>    Control a download by ID",
			"  show-folder                      Open the default download folder",
		},
		Examples: []string{
			"  borz downloads list --limit 20",
			"  borz downloads search report",
			"  borz downloads start https://example.com/file.zip --filename file.zip",
		},
		Notes: "Requires the Chrome extension. It uses chrome.downloads, which exposes browser download manager state outside CDP.",
	},
	"window": {
		Summary: "List and control Chrome browser windows through the extension.",
		Usage:   "borz window [list|new|focus|close]",
		Flags: []string{
			"  list                  List Chrome windows and tab counts (default)",
			"  new [url] [--focused] Create a browser window",
			"  focus <id>            Focus a browser window",
			"  close <id>            Close a browser window",
		},
		Examples: []string{
			"  borz window list",
			"  borz window new https://example.com --focused",
			"  borz window focus 123",
		},
		Notes: "Requires the Chrome extension. The plural alias 'windows' is also accepted.",
	},
	"windows": {
		Summary: "Alias for 'window'.",
		Usage:   "borz windows [list|new|focus|close]",
		Notes:   "Use 'borz help window' for the full command reference.",
	},
	"frame": {
		Summary: "Switch the interaction context to a child iframe, or back to the main frame.",
		Usage:   "borz frame [main|<selector>]",
		Examples: []string{
			"  borz frame 'iframe#checkout'",
			"  borz frame main",
		},
	},
	"dialog": {
		Summary: "Pre-arm a handler for the next native alert/confirm/prompt/beforeunload.",
		Usage:   "borz dialog [accept|dismiss] [prompt-text]",
		Examples: []string{
			"  borz dialog accept",
			"  borz dialog accept 'Leo'",
			"  borz dialog dismiss",
		},
		Notes: "Run this BEFORE the click/navigation that triggers the dialog. " +
			"Default action is 'accept'. If accepting a prompt, pass the response text as the second arg.",
	},

	// --- Site adapters ---
	"site": {
		Summary: "Run or inspect platform-specific scrapers (twitter/search, hackernews/top, ...).",
		Usage:   "borz site [subcommand]",
		Flags: []string{
			"  list                  List all adapters grouped by platform (default)",
			"  search <query>        Fuzzy-search adapters by name/description",
			"  info <name>           Show an adapter's args, domain, and example",
			"  update [--ref <ref>]  Pull the community adapter pack, optionally pinned",
			"  new <name>            Scaffold a local adapter template",
			"  lint <name|path>      Validate adapter metadata and wrapper buildability",
			"  trust <name>          Trust the current SHA256 of a community adapter",
			"  run <name> [args...]  Run an adapter (equivalent to 'borz <name> ...')",
		},
		Examples: []string{
			"  borz site list",
			"  borz site info hackernews/top",
			"  borz hackernews/top",
			"  borz twitter/search 'claude code'",
		},
		Notes: "Any '<platform>/<adapter>' invocation is forwarded to 'site run' — " +
			"'borz hackernews/top' and 'borz site run hackernews/top' are equivalent. " +
			"Run 'borz site info <name>' to see required args, read-only status, source, and hash.",
	},

	// --- Utility / infra ---
	"fetch": {
		Summary: "Issue an authenticated HTTP request from inside the page (inherits cookies).",
		Usage:   "borz fetch <url> [--method <M>] [--tab <id>]",
		Flags: []string{
			"  --method <M>   HTTP method (default: GET)",
		},
		Examples: []string{
			"  borz fetch https://api.github.com/user",
			"  borz fetch https://example.com/api/x --method POST",
		},
		Notes: "Runs as fetch(url, {credentials:'include'}) in the tab, so session cookies, " +
			"auth headers, and CORS policy all apply. Body is returned as parsed JSON when the " +
			"response content-type is application/json, else as raw text.",
	},
	"status": {
		Summary: "Print the daemon status as JSON (uptime, tabs, CDP connection).",
		Usage:   "borz status",
	},
	"doctor": {
		Summary: "Run end-to-end diagnostics on the CLI/daemon/browser stack.",
		Usage:   "borz doctor [--json]",
		Notes: "Checks: home directory, daemon.json, daemon process & HTTP, CDP connection,\n" +
			"open tabs, and direct CDP discovery. Exits non-zero if any check fails;\n" +
			"warnings (e.g. daemon not started) do not fail. Use --json for machine output.",
	},
	"daemon": {
		Summary: "Start or control the local daemon (loopback only).",
		Usage:   "borz daemon [status|shutdown|stop] [--host H --port P --cdp-host H --cdp-port P]",
		Flags: []string{
			"  (no subcommand)        Start the daemon in the foreground",
			"  status                 Show JSON status (or 'not running')",
			"  shutdown|stop          Ask the running daemon to exit",
			"  --host <h>             Bind address (default 127.0.0.1)",
			"  --port <p>             Listen port (default 19824)",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (default 30, 0=disable; env BORZ_TAB_IDLE_TIMEOUT)",
		},
		Notes: "For a remote-accessible server with auth, use 'borz server' instead.",
	},
	"server": {
		Summary: "Start the REST server (exposes /v1/* routes; requires a token when non-loopback).",
		Usage:   "borz server [status|shutdown|stop] [--host H --port P --token T]",
		Flags: []string{
			"  (no subcommand)        Start the server in the foreground",
			"  status                 Show JSON status",
			"  shutdown|stop          Ask the running server to exit",
			"  --host <h>             Bind address (default 0.0.0.0)",
			"  --port <p>             Listen port (default 19824; env BORZ_SERVER_PORT)",
			"  --token <t>            Bearer token required for non-loopback binds",
			"                         (env BORZ_TOKEN)",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (default 30, 0=disable; env BORZ_TAB_IDLE_TIMEOUT)",
		},
		Examples: []string{
			"  borz server --host 127.0.0.1",
			"  borz server --host 0.0.0.0 --token \"$BORZ_TOKEN\"",
			"  borz server shutdown",
		},
		Notes: "Clients authenticate with 'Authorization: Bearer <token>'. " +
			"Swagger UI is served at /docs and the OpenAPI spec at /openapi.yaml.",
	},
	"service": {
		Summary: "Install or control borz as a Windows service.",
		Usage:   "borz service [install|uninstall|start|stop|status] [--name N] [server flags]",
		Flags: []string{
			"  install                Register a Windows service that runs 'borz server'",
			"  uninstall|remove       Delete the registered service",
			"  start|stop|status      Control or inspect the service",
			"  --name <n>             Service name (default borz)",
			"  --display-name <text>  Display name for install",
			"  --description <text>   Service description for install",
			"  --host <h>             Server bind address (default 127.0.0.1 for service)",
			"  --port <p>             Listen port (default 19824)",
			"  --token <t>            Bearer token; required with non-loopback --host",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
		},
		Examples: []string{
			"  borz service install",
			"  borz service start",
			"  borz service install --host 0.0.0.0 --token \"$env:BORZ_TOKEN\"",
			"  borz service status",
			"  borz service uninstall",
		},
		Notes: "Windows service management requires an elevated shell. The service entry " +
			"runs the REST server in the foreground under the Windows Service Control Manager. " +
			"Non-Windows platforms should use launchd, systemd, or a process manager instead.",
	},
	"client": {
		Summary: "Configure the remote borz server used by the global --remote flag.",
		Usage:   "borz client [setup|status|enable|disable]",
		Flags: []string{
			"  setup <url>            Store the remote server URL",
			"  setup --url <url>      Same as positional URL",
			"  BORZ_SERVER_URL        Env fallback when setup URL is omitted",
			"  --token <t>            Bearer token for the remote server",
			"  BORZ_TOKEN             Env fallback when --token is omitted",
			"  --no-check             Store/toggle config without probing /status",
			"  status                 Show current remote-client config",
			"  enable|disable         Legacy config toggle; normal commands still need --remote",
		},
		Examples: []string{
			"  borz client setup http://server:19824 --token \"$BORZ_TOKEN\"",
			"  borz --remote open https://example.com",
			"  alias borz='borz --remote'  # remote by default in this shell",
		},
		Notes: "Commands that talk to the browser use the local daemon unless --remote is " +
			"passed for that invocation. The token is stored in " +
			"~/.borz/client.json with 0600 permissions and is never printed by status. " +
			"BORZ_SERVER_URL and BORZ_TOKEN are read only by 'client setup' when the " +
			"matching CLI argument is omitted.",
	},
	"mcp": {
		Summary: "Speak MCP over stdio — intended to be spawned by an MCP-aware client.",
		Usage:   "borz mcp",
		Notes:   "Humans rarely run this directly; configure it in your MCP client instead.",
	},
	"extension": {
		Summary: "Download, locate, or inspect the borz Chrome extension.",
		Usage:   "borz extension [download|update|path|status|call]",
		Flags: []string{
			"  download              Download the latest extension zip and extract it (default)",
			"  update                Alias for 'download' — overwrites the current install",
			"  path                  Print the local install directory and exit",
			"  status                Query the connected extension capabilities",
			"  call <method> [json]  Raw extension RPC escape hatch",
		},
		Examples: []string{
			"  borz extension download",
			"  borz extension path",
			"  borz extension status --json",
			"  borz extension call bookmarks.search '{\"query\":\"github\"}'",
		},
		Notes: "Extracts to ~/.borz/extension (override with $BORZ_HOME). " +
			"After download, load it in Chrome via chrome://extensions → enable Developer " +
			"mode → 'Load unpacked' → select the printed directory. The extension provides " +
			"capabilities CDP cannot: cross-domain cookies, bookmarks, history, downloads, " +
			"windows, tab groups, and browser-level events.",
	},
	"extension.download": {
		Summary: "Download the latest extension zip and extract it (replacing any prior install).",
		Usage:   "borz extension download",
		Notes: "Downloads borz-extension.zip from the latest GitHub release, verifies " +
			"its SHA-256 from checksums.txt, then nukes ~/.borz/extension and extracts " +
			"the new contents. After it finishes, follow the printed steps to load it via " +
			"chrome://extensions → 'Load unpacked'.",
	},
	"extension.update": {
		Summary: "Alias for 'extension download'. Overwrites the current install with the latest release.",
		Usage:   "borz extension update",
	},
	"extension.install": {
		Summary: "Alias for 'extension download'.",
		Usage:   "borz extension install",
	},
	"extension.path": {
		Summary: "Print the local extension install directory.",
		Usage:   "borz extension path",
		Notes:   "Useful for scripting or for pasting the path into chrome://extensions.",
	},
	"extension.status": {
		Summary: "Show the connected extension's capabilities.",
		Usage:   "borz extension status [--json]",
		Notes:   "Requires the extension service worker to be connected to /v1/ext/ws.",
	},
	"extension.capabilities": {
		Summary: "Alias for 'extension status'.",
		Usage:   "borz extension capabilities [--json]",
	},
	"extension.call": {
		Summary: "Call a supported extension RPC method directly.",
		Usage:   "borz extension call <method> [json-params]",
		Examples: []string{
			"  borz extension call bookmarks.search '{\"query\":\"github\"}'",
			"  borz extension call downloads.search '{\"q\":\"report\",\"limit\":10}'",
		},
		Notes: "Use 'extension status --json' to inspect supportedMethods. This is the CLI escape hatch for extension APIs not promoted to a first-class command.",
	},
	"update": {
		Summary: "Download the latest release from GitHub and replace the running binary.",
		Usage:   "borz update [--check] [--force]",
		Flags: []string{
			"  --check   Only report current vs latest version; do not download",
			"  --force   Reinstall even if already on the latest version",
		},
		Examples: []string{
			"  borz update --check",
			"  borz update",
		},
		Notes: "The binary replaces itself atomically via rename. " +
			"Verifies a SHA-256 checksum from the GitHub release assets.",
	},
	"help": {
		Summary: "Show help for borz as a whole or a specific command.",
		Usage:   "borz help [command [subcommand]]",
		Examples: []string{
			"  borz help",
			"  borz help snapshot",
			"  borz help tab new",
			"  borz snapshot --help",
			"  borz tab new --help",
			"  borz help --all | less",
		},
		Notes: "Unknown commands and known commands with misspelled subcommands print " +
			"nearest-match hints, for example 'borz extension statu' suggests " +
			"'borz extension status'.",
	},
	"version": {
		Summary: "Print the version of this borz binary.",
		Usage:   "borz version",
	},

	// --- Subcommand pages: tab.* ---
	"tab.list": {
		Summary:  "List every open tab with title, URL, 1-based index, and short id.",
		Usage:    "borz tab [list]",
		Examples: []string{"  borz tab", "  borz tab list"},
		Notes: "The active tab is marked with '*'. The short id shown in the last column is " +
			"what you pass to '--tab' or to 'tab select --id'.",
	},
	"tab.new": {
		Summary:  "Open a new tab, optionally pointed at a URL (default about:blank).",
		Usage:    "borz tab new [url]",
		Examples: []string{"  borz tab new", "  borz tab new https://github.com"},
		Notes: "Unlike 'borz open', this always creates a fresh tab and never reuses an " +
			"existing one. Use 'open --new' if you want the same force-new behaviour from the " +
			"navigation flow.",
	},
	"tab.select": {
		Summary: "Switch the active tab by index or short id.",
		Usage:   "borz tab select <n|--id <short-id>>",
		Flags: []string{
			"  <n>               1-based index as shown by 'borz tab'",
			"  --id <short-id>   Short tab id (also shown by 'borz tab')",
		},
		Examples: []string{
			"  borz tab 2            # shorthand, equivalent to 'tab select 2'",
			"  borz tab select 2",
			"  borz tab select --id abc123",
		},
	},
	"tab.close": {
		Summary: "Close a tab by index or short id (default: the currently active tab).",
		Usage:   "borz tab close [n|--id <short-id>]",
		Examples: []string{
			"  borz tab close",
			"  borz tab close 3",
			"  borz tab close --id abc123",
		},
	},
	"tab.events": {
		Summary: "Stream or list browser-level tab/window events from the Chrome extension.",
		Usage:   "borz tab events [--tail] [--since <seq|last_action>] [--json]",
		Flags: []string{
			"  --tail                  Keep streaming new events until Ctrl+C",
			"  --since <seq|last_action>  Only include newer events",
		},
		Examples: []string{
			"  borz tab events",
			"  borz tab events --tail",
			"  borz tab events --since last_action --json",
		},
		Notes: "Requires the borz Chrome extension to be installed and connected.",
	},

	// --- Subcommand pages: site.* ---
	"site.list": {
		Summary:  "List every available site adapter, grouped by platform.",
		Usage:    "borz site [list]",
		Examples: []string{"  borz site", "  borz site list"},
		Notes: "Entries tagged [local] come from your workspace; the rest are from the " +
			"community pack. Use 'site update' to refresh the community pack.",
	},
	"site.search": {
		Summary:  "Fuzzy-search adapters by name, description, or domain.",
		Usage:    "borz site search <query>",
		Examples: []string{"  borz site search hacker", "  borz site search 'linux forum'"},
	},
	"site.info": {
		Summary: "Print an adapter's description, domain, source, example, and args.",
		Usage:   "borz site info <name>",
		Examples: []string{
			"  borz site info hackernews/top",
			"  borz hackernews/top --help   # shortcut that forwards here",
		},
		Notes: "'Args' in the output lists the positional arguments the adapter accepts. " +
			"'(required)' marks mandatory ones.",
	},
	"site.update": {
		Summary: "Pull the latest community adapter pack from GitHub.",
		Usage:   "borz site update [--ref <tag|sha>]",
		Flags: []string{
			"  --ref <tag|sha>   Fetch and checkout a specific community repo ref; writes community.lock",
		},
		Notes: "Community adapters are cached under the user's config dir. Local adapters " +
			"you've placed in the workspace are not affected. Updates refuse to run when the community repo has local changes.",
	},
	"site.new": {
		Summary:  "Create a local site adapter template.",
		Usage:    "borz site new <platform/name>",
		Examples: []string{"  borz site new github/search"},
		Notes:    "Creates the file under the local sites directory and refuses to overwrite an existing adapter.",
	},
	"site.lint": {
		Summary:  "Validate an adapter's metadata and generated execution wrapper.",
		Usage:    "borz site lint <name-or-path>",
		Examples: []string{"  borz site lint github/search", "  borz site lint ./sites/github/search.js"},
		Notes:    "Checks required metadata, required/default consistency, required args, and adapter readability.",
	},
	"site.trust": {
		Summary:  "Trust the current SHA256 hash of a community adapter.",
		Usage:    "borz site trust <name>",
		Examples: []string{"  borz site trust twitter/search"},
		Notes:    "Community adapters are arbitrary JavaScript. Trust records the current hash in sites-trust.json; hash changes require re-trust or --force.",
	},
	"site.run": {
		Summary:  "Run an adapter by name — equivalent to calling 'borz <platform>/<name> ...' directly.",
		Usage:    "borz site run <name> [args...] [--tab <id>] [--timeout <ms>] [--force]",
		Examples: []string{"  borz site run hackernews/top 10", "  borz hackernews/top 10"},
		Notes: "Use 'borz site info <name>' to discover the args an adapter expects " +
			"before running it. The daemon blocks domain mismatches by default; --force bypasses that guard and one-off community trust checks.",
	},

	// --- Subcommand pages: daemon.* ---
	"daemon.status": {
		Summary: "Print the daemon's JSON status, or 'Daemon is not running'.",
		Usage:   "borz daemon status",
		Notes:   "Identical payload to the top-level 'borz status'.",
	},
	"daemon.shutdown": {
		Summary:  "Ask the running daemon to exit cleanly.",
		Usage:    "borz daemon shutdown",
		Examples: []string{"  borz daemon shutdown", "  borz daemon stop   # alias"},
	},
	"daemon.stop": {
		Summary:  "Alias for 'daemon shutdown'. Asks the running daemon to exit cleanly.",
		Usage:    "borz daemon stop",
		Examples: []string{"  borz daemon stop"},
	},

	// --- Subcommand pages: server.* ---
	"server.status": {
		Summary: "Print the server's JSON status, or 'Server is not running'.",
		Usage:   "borz server status",
	},
	"server.shutdown": {
		Summary:  "Ask the running server to exit cleanly.",
		Usage:    "borz server shutdown",
		Examples: []string{"  borz server shutdown", "  borz server stop   # alias"},
	},
	"server.stop": {
		Summary:  "Alias for 'server shutdown'. Asks the running server to exit cleanly.",
		Usage:    "borz server stop",
		Examples: []string{"  borz server stop"},
	},
	"service.install": {
		Summary: "Register borz as a Windows service that runs the REST server.",
		Usage:   "borz service install [--name N] [--host H --port P --token T]",
		Notes:   "Defaults to a loopback-only service on 127.0.0.1:19824. Use an elevated PowerShell or Command Prompt.",
	},
	"service.uninstall": {
		Summary:  "Delete the borz Windows service registration.",
		Usage:    "borz service uninstall [--name N]",
		Examples: []string{"  borz service uninstall", "  borz service remove --name borz"},
	},
	"service.remove": {
		Summary: "Alias for 'service uninstall'.",
		Usage:   "borz service remove [--name N]",
	},
	"service.start": {
		Summary: "Start the borz Windows service.",
		Usage:   "borz service start [--name N]",
	},
	"service.stop": {
		Summary: "Stop the borz Windows service.",
		Usage:   "borz service stop [--name N]",
	},
	"service.status": {
		Summary: "Print the borz Windows service state.",
		Usage:   "borz service status [--name N]",
	},

	// --- Subcommand pages: client.* ---
	"client.setup": {
		Summary: "Store the remote server URL and optional bearer token.",
		Usage:   "borz client setup <server-url> [--token <token>] [--no-check]",
		Examples: []string{
			"  borz client setup http://127.0.0.1:19824",
			"  BORZ_SERVER_URL=http://127.0.0.1:19824 BORZ_TOKEN=secret borz client setup",
			"  borz client setup https://browser.example.com --token \"$BORZ_TOKEN\"",
		},
		Notes: "The setup command probes the server's authenticated /status endpoint before " +
			"saving unless --no-check is set. If the URL has no scheme, http:// is assumed. " +
			"Use --remote on individual browser commands to route them to this server. " +
			"When <server-url> or --token is omitted, setup falls back to BORZ_SERVER_URL " +
			"and BORZ_TOKEN respectively.",
	},
	"client.enable": {
		Summary: "Set the legacy remote-client enabled field in client.json.",
		Usage:   "borz client enable [--no-check]",
		Notes:   "The configured server is checked unless --no-check is set. Browser actions still use local by default; pass --remote to route one invocation to the configured server.",
	},
	"client.disable": {
		Summary:  "Clear the legacy remote-client enabled field in client.json.",
		Usage:    "borz client disable",
		Examples: []string{"  borz client disable"},
	},
	"client.status": {
		Summary: "Show the remote client config used by --remote.",
		Usage:   "borz client status [--json]",
	},

	// --- Subcommand pages: trace.* ---
	"trace.start": {
		Summary: "Begin recording the user's manual clicks, fills, presses, scrolls, and navigations.",
		Usage:   "borz trace start [--tab <id>]",
		Notes: "Nothing is returned until you call 'trace stop'. Use 'trace status' to confirm " +
			"recording is active.",
	},
	"trace.stop": {
		Summary: "Stop recording and return the captured event list as JSON.",
		Usage:   "borz trace stop [--tab <id>]",
		Notes: "Output is intended for replay: each event has a type (click/fill/press/...), " +
			"timestamp, URL, and any type-specific fields (ref, text, pixels).",
	},
	"trace.status": {
		Summary: "Report whether recording is active and how many events are captured so far.",
		Usage:   "borz trace status [--tab <id>]",
	},

	// --- Subcommand pages: network.* ---
	"network.requests": {
		Summary: "List network requests captured for the current tab.",
		Usage:   "borz network requests [--filter S] [--method M] [--status C] [--with-body] [--since <seq|last_action>] [--tab <id>]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code>      Only requests whose response status matches <code>",
			"  --with-body          Include response bodies (heavier payload)",
			"  --since <seq|last_action>   Only requests newer than this checkpoint",
		},
		Examples: []string{
			"  borz network requests",
			"  borz network requests --filter /api/ --method POST",
			"  borz network requests --since last_action --with-body",
		},
	},
	"network.clear": {
		Summary: "Drop all captured network requests for the current tab.",
		Usage:   "borz network clear [--tab <id>]",
		Notes:   "Pair with 'network requests --since last_action' if you just want a fresh window.",
	},

	// --- Subcommand pages: dialog.* ---
	"dialog.accept": {
		Summary: "Pre-arm the next native dialog to be accepted (OK / Leave / prompt submitted).",
		Usage:   "borz dialog accept [prompt-text] [--tab <id>]",
		Examples: []string{
			"  borz dialog accept",
			"  borz dialog accept 'Leo'       # prompt response text",
		},
		Notes: "Run BEFORE the click/navigation that triggers the dialog. For a prompt(), pass " +
			"the response as the second arg; for alert()/confirm() it is ignored.",
	},
	"dialog.dismiss": {
		Summary: "Pre-arm the next native dialog to be dismissed (Cancel / Stay on page).",
		Usage:   "borz dialog dismiss [--tab <id>]",
		Notes:   "Run BEFORE the click/navigation that triggers the dialog.",
	},

	// --- Subcommand pages: frame.* ---
	"frame.main": {
		Summary:  "Switch the interaction context back to the page's top-level frame.",
		Usage:    "borz frame main [--tab <id>]",
		Examples: []string{"  borz frame main"},
	},

	// --- Subcommand pages: extension-backed browser APIs ---
	"bookmarks.tree": {
		Summary:  "Print Chrome's full bookmark tree.",
		Usage:    "borz bookmarks tree [--json]",
		Examples: []string{"  borz bookmarks tree", "  borz bookmarks tree --json"},
	},
	"bookmarks.search": {
		Summary:  "Search Chrome bookmarks by title or URL.",
		Usage:    "borz bookmarks search <query> [--json]",
		Examples: []string{"  borz bookmarks search github"},
	},
	"bookmarks.create": {
		Summary: "Create a Chrome bookmark.",
		Usage:   "borz bookmarks create <url> <title> [--parent <id>]",
		Flags:   []string{"  --parent <id>   Parent bookmark folder ID"},
	},
	"bookmarks.update": {
		Summary: "Update a Chrome bookmark title and/or URL.",
		Usage:   "borz bookmarks update <id> [--title <title>] [--url <url>]",
		Flags: []string{
			"  --title <title>   New bookmark title",
			"  --url <url>       New bookmark URL",
		},
	},
	"bookmarks.remove": {
		Summary: "Remove a Chrome bookmark or bookmark folder.",
		Usage:   "borz bookmarks remove <id> [--recursive]",
		Flags:   []string{"  --recursive      Remove a folder and all children"},
	},
	"browser-history.search": {
		Summary: "Search Chrome browsing history.",
		Usage:   "borz browser-history search [query] [--limit N] [--json]",
		Flags:   []string{"  --limit N        Maximum results returned by Chrome"},
	},
	"browser-history.delete-url": {
		Summary: "Delete one URL from Chrome browsing history.",
		Usage:   "borz browser-history delete-url <url>",
	},
	"downloads.list": {
		Summary: "List Chrome downloads.",
		Usage:   "borz downloads list [--limit N] [--state complete|interrupted|in_progress] [--json]",
		Flags: []string{
			"  --limit N       Maximum results",
			"  --state S       Filter by download state",
		},
	},
	"downloads.search": {
		Summary: "Search Chrome downloads.",
		Usage:   "borz downloads search <query> [--limit N] [--json]",
		Flags:   []string{"  --limit N       Maximum results"},
	},
	"downloads.start": {
		Summary: "Start a Chrome-managed download.",
		Usage:   "borz downloads start <url> [--filename <path>] [--save-as]",
		Flags: []string{
			"  --filename <path>   Suggested download filename",
			"  --save-as           Ask Chrome to show the Save As dialog",
		},
	},
	"downloads.erase": {
		Summary: "Erase Chrome download history records.",
		Usage:   "borz downloads erase [--id N|query]",
		Flags:   []string{"  --id N          Erase one download record by ID"},
	},
	"downloads.cancel":      {Summary: "Cancel a Chrome download by ID.", Usage: "borz downloads cancel <id>"},
	"downloads.pause":       {Summary: "Pause a Chrome download by ID.", Usage: "borz downloads pause <id>"},
	"downloads.resume":      {Summary: "Resume a Chrome download by ID.", Usage: "borz downloads resume <id>"},
	"downloads.show":        {Summary: "Show one downloaded file in the platform file manager.", Usage: "borz downloads show <id>"},
	"downloads.show-folder": {Summary: "Open Chrome's default download folder.", Usage: "borz downloads show-folder"},
	"window.list": {
		Summary:  "List Chrome browser windows.",
		Usage:    "borz window list [--json]",
		Examples: []string{"  borz window list"},
	},
	"window.new": {
		Summary: "Create a Chrome browser window.",
		Usage:   "borz window new [url] [--focused]",
		Flags:   []string{"  --focused       Focus the new window immediately"},
	},
	"window.focus": {Summary: "Focus a Chrome browser window.", Usage: "borz window focus <id>"},
	"window.close": {Summary: "Close a Chrome browser window.", Usage: "borz window close <id>"},
	"windows.list": {Summary: "Alias for 'window list'.", Usage: "borz windows list [--json]"},
	"windows.new":  {Summary: "Alias for 'window new'.", Usage: "borz windows new [url] [--focused]"},
	"windows.focus": {
		Summary: "Alias for 'window focus'.",
		Usage:   "borz windows focus <id>",
	},
	"windows.close": {
		Summary: "Alias for 'window close'.",
		Usage:   "borz windows close <id>",
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

// topLevelCommandNames returns sorted top-level commands (no dotted
// subcommand pages like "tab.new").
func topLevelCommandNames() []string {
	out := make([]string, 0, len(commandHelp))
	for n := range commandHelp {
		if strings.Contains(n, ".") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// subcommandNames returns sorted first-level subcommands for a parent command.
func subcommandNames(parent string) []string {
	prefix := parent + "."
	seen := map[string]bool{}
	for n := range commandHelp {
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		sub := strings.TrimPrefix(n, prefix)
		if sub == "" || strings.Contains(sub, ".") || seen[sub] {
			continue
		}
		seen[sub] = true
	}
	out := make([]string, 0, len(seen))
	for sub := range seen {
		out = append(out, sub)
	}
	sort.Strings(out)
	return out
}

// printAllHelp dumps every registered command's help block in one go.
// Intended for piping into a pager or feeding to an LLM.
func printAllHelp() {
	fmt.Println("# borz — full command reference")
	fmt.Println()
	for _, name := range commandNames() {
		fmt.Printf("## %s\n\n", name)
		printCommandHelp(name)
	}
}

// suggestCommands returns up to maxN top-level command names closest to input.
func suggestCommands(input string, maxN int) []string {
	return suggestNames(input, topLevelCommandNames(), maxN)
}

// suggestSubcommands returns up to maxN subcommands for parent closest to input.
func suggestSubcommands(parent, input string, maxN int) []string {
	return suggestNames(input, subcommandNames(parent), maxN)
}

// suggestNames returns close Levenshtein matches. Returns nil if nothing's
// close enough — better silent than to scream "did you mean tab?" for "xyzzy".
func suggestNames(input string, candidates []string, maxN int) []string {
	type scored struct {
		name string
		dist int
	}
	var hits []scored
	input = strings.ToLower(input)
	for _, n := range candidates {
		d := levenshtein(strings.ToLower(input), n)
		// Tighten the cap for short commands (e.g. "tab" -> "tap" should match
		// at d=1 but "tab" -> "scroll" should not via d=4).
		threshold := 3
		if len(n) <= 4 {
			threshold = 2
		}
		if d <= threshold {
			hits = append(hits, scored{n, d})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})
	if len(hits) > maxN {
		hits = hits[:maxN]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// formatCommandSuggestions renders command names as runnable command lines.
func formatCommandSuggestions(parent string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if parent == "" {
			out = append(out, "borz "+name)
		} else {
			out = append(out, "borz "+parent+" "+name)
		}
	}
	return out
}

// unknownSubcommandHint builds a consistent, subcommand-aware hint message for
// fatal errors in dispatch handlers.
func unknownSubcommandHint(parent, sub string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Unknown %s subcommand: %s", parent, sub)
	if suggestions := suggestSubcommands(parent, sub, 3); len(suggestions) > 0 {
		fmt.Fprintf(&b, "\nDid you mean: %s?", strings.Join(formatCommandSuggestions(parent, suggestions), ", "))
	}
	if available := subcommandNames(parent); len(available) > 0 {
		fmt.Fprintf(&b, "\nAvailable subcommands: %s", strings.Join(available, ", "))
	}
	fmt.Fprintf(&b, "\nRun 'borz help %s' for usage.", parent)
	return b.String()
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
