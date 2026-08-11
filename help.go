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
  --profile <name>        Select a profile: its transport (managed browser,
                          CDP endpoint, or remote server) comes from
                          ~/.borz/profiles.json; undeclared names = managed
  --remote                (deprecated) Alias for --profile remote; errors when
                          combined with an explicit --profile
  --tab <id>              Target a specific tab (from 'borz tab')
  --json                  Emit the raw JSON response instead of pretty output
  --jq <expr>             Filter JSON output with a jq expression (implies --json)
  --unwrap                For 'eval'/site adapters: print resp.data.result raw
                          (strings unquoted, other shapes as JSON)
  --since <seq|last_action>  Only include events newer than this (network/console/errors)

Wait-for flags (open, back, forward, refresh, click, hover, fill, type,
check, uncheck, select, press, scroll, eval):
  --wait-for <selector>   Block until document.querySelector(<selector>) is non-null
  --timeout <ms>          Cap --wait-for (default 10000ms)

Delay flags (global — available on any command):
  --pre-delay <ms>        Sleep this many ms inside the daemon before the action
  --post-delay <ms>       Sleep this many ms after a successful action
                          Both are capped by the 30s daemon command timeout.
                          Prefer --wait-for over --post-delay when a DOM
                          selector is available.`

const waitForUsageSuffix = " [--wait-for <selector>] [--timeout <ms>]"

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
	"back":    {Summary: "Go back in the current tab's history.", Usage: "borz back [--tab <id>]" + waitForUsageSuffix},
	"forward": {Summary: "Go forward in the current tab's history.", Usage: "borz forward [--tab <id>]" + waitForUsageSuffix},
	"refresh": {Summary: "Reload the current page.", Usage: "borz refresh [--tab <id>]" + waitForUsageSuffix},
	"close": {
		Summary: "Close the current tab (or the tab named by --tab).",
		Usage:   "borz close [--tab <id>]",
		Notes:   "To close a tab by index/ID use 'borz tab close <n>'.",
	},

	// --- Interaction ---
	"click": {
		Summary:  "Click an element by ref.",
		Usage:    "borz click <ref> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz click 5"},
		Notes:    refNote,
	},
	"hover": {
		Summary:  "Hover an element by ref.",
		Usage:    "borz hover <ref> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz hover 12"},
		Notes:    refNote,
	},
	"fill": {
		Summary: "Clear an input/textarea and fill it with <text>.",
		Usage:   "borz fill <ref> <text> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{
			"  borz fill 3 'hello world'",
			"  borz fill 3 multi word text (remaining args are joined)",
		},
		Notes: refNote + "\nUse 'type' to append without clearing.",
	},
	"type": {
		Summary:  "Append <text> to an input/textarea without clearing it first.",
		Usage:    "borz type <ref> <text> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz type 3 ' and more'"},
		Notes:    refNote + "\nUse 'fill' to clear the field before writing.",
	},
	"check": {
		Summary:  "Check a checkbox or radio by ref.",
		Usage:    "borz check <ref> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz check 7"},
		Notes:    refNote,
	},
	"uncheck": {
		Summary:  "Uncheck a checkbox by ref.",
		Usage:    "borz uncheck <ref> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz uncheck 7"},
		Notes:    refNote,
	},
	"select": {
		Summary:  "Select an <option> in a <select> element by value.",
		Usage:    "borz select <ref> <value> [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{"  borz select 9 'us-east-1'"},
		Notes:    refNote,
	},
	"upload": {
		Summary: "Attach files to an <input type=file> or its associated label by ref.",
		Usage:   "borz upload <ref> <file> [file...] [--tab <id>]" + waitForUsageSuffix,
		Examples: []string{
			"  borz upload 5 ./photo.jpg",
			"  borz upload 5 ./a.pdf ./b.pdf  # multi-file input",
		},
		Notes: refNote + "\nThe ref may point directly at the file input or at its associated <label>;\n" +
			"borz resolves label.control / a nested file input automatically.\n" +
			"Wraps CDP DOM.setFileInputFiles. Paths are resolved on the daemon's\n" +
			"filesystem (where Chrome runs) — for `--remote`, the files must live on\n" +
			"the daemon host, not on the client.",
	},
	"press": {
		Summary: "Dispatch a single key press to the active element.",
		Usage:   "borz press <key> [--modifiers <csv>] [--commands <csv>] [--tab <id>]" + waitForUsageSuffix,
		Flags: []string{
			"  --modifiers <csv>   Comma-separated combination of alt, ctrl, meta, shift",
			"  --commands <csv>    CDP editing commands sent with keyDown (e.g. selectAll,copy)",
		},
		Examples: []string{
			"  borz press Enter",
			"  borz press Escape",
			"  borz press Tab --modifiers shift",
			"  borz press a --modifiers meta        # select all (auto-mapped to selectAll)",
			"  borz press a --commands selectAll    # explicit editing command",
		},
		Notes: "Key names follow KeyboardEvent.key (e.g. 'Enter', 'Tab', 'ArrowLeft', 'a').\n" +
			"Editing shortcuts (select-all, copy, paste, undo) are handled in the browser\n" +
			"process, which synthesized CDP key events bypass — notably on macOS. Use\n" +
			"--modifiers meta for those combos: well-known meta combos (a/c/x/v/z/y) are\n" +
			"auto-translated to CDP editing commands so they work in editable content on\n" +
			"every OS. Pass --commands to set the commands explicitly.",
	},
	"clipboard-write": {
		Summary: "Write text to the browser clipboard, optionally pasting it into a terminal.",
		Usage:   "borz clipboard-write <text> | --file <path> [--paste] [--tab <id>]",
		Flags: []string{
			"  --file <path>   Read the clipboard text from a file instead of args",
			"  --paste         After writing, fire Ctrl+Shift+V to paste into the focused terminal",
			"  --tab <id>      Target a specific tab",
		},
		Examples: []string{
			"  borz clipboard-write 'ls -la'",
			"  borz clipboard-write --file ./payload.b64 --paste --tab 2",
		},
		Notes: "Sets the clipboard via navigator.clipboard.writeText (the tab is brought\n" +
			"to the foreground first). With --paste it then sends Ctrl+Shift+V so an\n" +
			"xterm.js / SSH terminal receives the whole string in one atomic paste —\n" +
			"no per-character streaming, no Enter race. The terminal must have focus;\n" +
			"the daemon best-effort focuses the xterm textarea (incl. same-origin\n" +
			"iframes) first.",
	},
	"term-text": {
		Summary: "Read an xterm.js terminal buffer as plain text (incl. scrollback).",
		Usage:   "borz term-text [--tab <id>]",
		Examples: []string{
			"  borz term-text",
			"  borz term-text --tab 2 --json",
		},
		Notes: "Replaces screenshot/OCR of canvas-rendered terminals (e.g. JumpServer\n" +
			"Luna SSH). Walks the page and same-origin iframes for a live Terminal\n" +
			"instance and reads buffer.active. The flat text prints to stdout; with\n" +
			"--json, data.result also carries {found, source, lines, cols, rows, note}.\n" +
			"source is xterm-buffer (full, incl. scrollback), xterm-accessibility or\n" +
			"xterm-rows (visible only), or none (canvas/WebGL or cross-origin iframe).",
	},
	"scroll": {
		Summary: "Scroll the page by pixels in a direction.",
		Usage:   "borz scroll [direction] [pixels] [--tab <id>]" + waitForUsageSuffix,
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
		Usage:   "borz eval <script...> [--file <path>] [--unwrap] [--no-auto-await] [--json-arg name=value]... [--tab <id>]" + waitForUsageSuffix,
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
			"CLI/MCP lexical declarations are scoped to one eval call, so const/let/class\n" +
			"names and --json-arg names can be reused safely; top-level return is supported.\n" +
			"--json-arg may be repeated; each value is parsed as JSON and prepended as\n" +
			"a scoped `const NAME = VALUE;` so --file scripts can read CLI inputs without templating.\n" +
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
		Usage:   "borz snapshot [-i] [-c] [-d N] [-s <selector>] [--role <role>] [--text-only] [--diff] [--tab <id>]",
		Flags: []string{
			"  -i, --interactive   Include only clickable/fillable elements (much shorter)",
			"  -c, --compact       Collapse whitespace and redundant nesting",
			"  -d, --depth N       Limit tree depth to N levels",
			"  -s, --selector <s>  Keep nodes whose tag/role/name/xpath/attributes contain <s>",
			"  --role <role>       Keep only nodes with this exact accessibility role",
			"  --text-only         Reader-mode plain text (no refs, no tree); good for LLM context",
			"  --diff              Print only what changed since the previous snapshot of this tab (+/-/~)",
		},
		Examples: []string{
			"  borz snapshot -i -c",
			"  borz snapshot -d 4 -s '#app'",
			"  borz snapshot --role button",
			"  borz snapshot --text-only",
			"  borz snapshot --diff",
		},
		Notes: "Snapshot before calling interaction commands — tree refs are regenerated " +
			"on every snapshot and go stale across navigations or DOM updates.\n" +
			"--text-only strips nav/header/footer/script/style and returns the visible text " +
			"(plus title and URL). It produces no new refs but preserves the latest tree refs; " +
			"they remain actionable unless the page navigated or its DOM changed.\n" +
			"--diff returns a `+` / `-` / `~` listing of nodes added, removed, or whose tracked " +
			"attributes (aria-pressed, aria-disabled, etc.) or accessible name changed since the " +
			"last snapshot of this tab. The very first call (or the first after the URL changes) " +
			"is a baseline reset: everything is reported as added. Refs in the diff are CURRENT " +
			"refs — safe to act on. Not supported with --text-only.",
	},
	"screenshot": {
		Summary: "Capture a PNG of the current page.",
		Usage:   "borz screenshot [path] [--annotate <ref>=<text>]... [--tab <id>]",
		Flags: []string{
			"  --annotate <ref>=<text>  Frame a snapshot ref and add a text callout (repeatable)",
		},
		Examples: []string{
			"  borz screenshot",
			"  borz screenshot ./out.png",
			"  borz snapshot -i -c",
			"  borz screenshot ./guide.png --annotate '@12=Click here to save'",
		},
		Notes: "Snapshot ref highlights are automatically hidden while the image is captured. --annotate uses refs from the latest snapshot and renders temporary red frames and plain-text callouts only for the capture; call snapshot again after navigation or DOM changes. Annotated elements must be visible in the current viewport. With [path], the CLI writes the PNG on the machine running the CLI, including when --remote is used. Without [path] the image is returned as a base64 data URL in the JSON payload.",
	},
	"viewport": {
		Summary: "Inspect or emulate a tab viewport for responsive testing.",
		Usage:   "borz viewport [status|current|mobile|tablet|desktop|reset|<width>x<height>] [--dpr N] [--mobile] [--touch|--no-touch] [--tab <id>]",
		Flags: []string{
			"  --viewport <preset|WxH>  Apply a viewport when used with open/tab new",
			"  --width <px>             Custom width (overrides preset)",
			"  --height <px>            Custom height (overrides preset)",
			"  --dpr <n>                Device scale factor (default 1)",
			"  --mobile                 Enable mobile device metrics",
			"  --touch / --no-touch     Enable or disable touch emulation",
			"  --reset                  Clear CDP viewport and touch emulation",
		},
		Examples: []string{
			"  borz viewport",
			"  borz viewport status",
			"  borz viewport mobile",
			"  borz viewport 390x844 --dpr 3 --mobile",
			"  borz open http://localhost:3000 --viewport mobile",
			"  borz viewport reset",
		},
		Notes: "Run without arguments, or with `status`/`current`, to inspect the current tab viewport. Viewport emulation is per tab and persists across navigations in that tab until reset. It changes layout, so refs from previous snapshots should be treated as stale.",
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
		Usage:   "borz network [requests|clear] [--tail] [--interval <duration|ms>] [--limit <n>] [flags]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code|class>  Only requests whose response status matches (404, 5xx)",
			"  --with-body          Include response bodies (heavier payload)",
			"  --limit <n>          Return at most the newest n requests",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
			"  --tail               Stream new requests as they arrive (Ctrl+C to stop)",
			"  --interval <duration|ms>  Polling interval in --tail mode (default 500ms)",
		},
		Examples: []string{
			"  borz network",
			"  borz network requests --filter /api/ --method POST",
			"  borz network requests --since last_action",
			"  borz network requests --tail --filter /api/",
			"  borz network --tail --json | jq -c 'select(.status>=400)'",
			"  borz network clear",
		},
		Notes: "--tail polls the daemon every --interval, advancing the cursor so each\n" +
			"request is printed at most once. Combine with --json for JSONL output suitable\n" +
			"for piping into jq -c, or with --filter/--method/--status to narrow the stream.",
	},
	"console": {
		Summary: "Read, clear, or live-tail captured console messages.",
		Usage:   "borz console [--clear] [--filter <substr>] [--since <seq|last_action>] [--tail] [--interval <duration|ms>]",
		Flags: []string{
			"  --clear               Drop all captured console messages for this tab",
			"  --filter <substr>     Only messages whose text contains <substr>",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
			"  --tail                Stream new messages as they arrive (Ctrl+C to stop)",
			"  --interval <duration|ms>  Polling interval in --tail mode (default 500ms)",
		},
		Examples: []string{
			"  borz console",
			"  borz console --filter error --since last_action",
			"  borz console --tail --json",
		},
	},
	"errors": {
		Summary: "Read, clear, or live-tail captured uncaught JS errors.",
		Usage:   "borz errors [--clear] [--filter <substr>] [--since <seq|last_action>] [--tail] [--interval <duration|ms>]",
		Flags: []string{
			"  --clear               Drop all captured JS errors for this tab",
			"  --filter <substr>     Only errors whose text/URL contains <substr>",
			"  --since <seq|last_action>   Only events newer than this checkpoint",
			"  --tail                Stream new errors as they arrive (Ctrl+C to stop)",
			"  --interval <duration|ms>  Polling interval in --tail mode (default 500ms)",
		},
		Examples: []string{
			"  borz errors",
			"  borz errors --since last_action",
			"  borz errors --tail --filter TypeError",
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
	"record": {
		Summary: "Capture a browser flow into a .borzrec bundle and render it to video/image.",
		Usage:   "borz record [start|stop|pause|resume|list|info|verify|render|redact|export|edit|play]",
		Flags: []string{
			"  start --url <url> [--mode cdp|client] [--out <path.borzrec>] [--fps N]",
			"  stop [id] [--recover]        Finalize the active recording",
			"  pause|resume [id]            Temporarily pause or resume capture",
			"  render <bundle> --preset share [--out demo.mp4]",
			"  redact <bundle> --selector <css> | --rect x,y,w,h",
		},
		Examples: []string{
			"  borz record start --url https://example.com --out demo.borzrec",
			"  borz record stop",
			"  borz record render demo.borzrec --preset share --out demo.mp4",
			"  borz record verify demo.borzrec",
		},
		Notes: "CDP mode records the controlled Chromium tab. Client mode requires the borz Chrome extension. Rendering requires ffmpeg on PATH.",
	},
	"history": {
		Summary: "List the daemon's recent action history (ring buffer).",
		Usage:   "borz history",
	},

	// --- Tabs / frames / dialogs ---
	"tab": {
		Summary: "List, create, switch between, foreground, or close Chrome tabs.",
		Usage:   "borz tab [subcommand]",
		Flags: []string{
			"  (no subcommand)       List all tabs (default)",
			"  list                  Same as no subcommand",
			"  new [url]             Open a new tab (default url: about:blank)",
			"  <n>                   Switch to the tab at index <n>",
			"  select --id <id>      Switch to the tab with the given short id",
			"  select <n>            Switch to the tab at index <n>",
			"  front [n|--id <id>]   Bring a tab to the real OS foreground (default: active)",
			"  close [n|--id <id>]   Close a tab by index or short id (default: active)",
			"  events [--tail]       Browser-level tab events (created/removed/updated/activated)",
		},
		Examples: []string{
			"  borz tab",
			"  borz tab new https://github.com",
			"  borz tab 2",
			"  borz tab select --id abc123",
			"  borz tab front",
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
	"cookies.all": {
		Summary: "Dump cookies across every domain visible to the extension.",
		Usage:   "borz cookies all [domain-filter]",
		Examples: []string{
			"  borz cookies all",
			"  borz cookies all github.com",
		},
		Notes: "Requires the borz Chrome extension. The optional filter matches cookie domains.",
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
		Summary: "Handle or pre-arm native alert/confirm/prompt/beforeunload dialogs.",
		Usage:   "borz dialog [accept|dismiss|disarm|status] [prompt-text] [--tab <id>]",
		Flags: []string{
			"  accept [text]   Answer an open dialog with OK, else arm the next one",
			"  dismiss         Answer an open dialog with Cancel, else arm the next one",
			"  disarm          Drop a previously armed handler",
			"  status          Show the open dialog, the armed handler, and recent history",
		},
		Examples: []string{
			"  borz dialog accept",
			"  borz dialog accept 'Leo'",
			"  borz dialog dismiss",
			"  borz dialog status",
		},
		Notes: "Arming BEFORE the click/navigation that triggers the dialog is the reliable " +
			"pattern; arming is one-shot. If a dialog is already open, accept/dismiss answers " +
			"that one instead of arming. An unanswered dialog blocks the page, so other commands " +
			"on that tab fail fast with a dialog-blocked error naming the dialog text — run " +
			"'borz dialog status' to see what it asks. Default action is 'accept'.",
	},
	"filechooser": {
		Summary: "Pre-arm a handler for the next native file-picker dialog.",
		Usage:   "borz filechooser [accept <file>...|cancel|disarm|status] [--tab <id>]",
		Flags: []string{
			"  accept <file>...   Arm: fulfill the next file picker with these files",
			"  cancel             Arm: suppress the next file picker without selecting files",
			"  disarm             Drop the armed handler and stop intercepting",
			"  status             Report whether a handler is armed (default)",
		},
		Examples: []string{
			"  borz filechooser accept ./report.pdf",
			"  borz filechooser accept ./a.png ./b.png   # multi-select picker",
			"  borz filechooser cancel",
			"  borz filechooser status",
		},
		Notes: "Run this BEFORE clicking the element that opens the picker — arming is\n" +
			"one-shot, like 'borz dialog'. Wraps CDP Page.setInterceptFileChooserDialog +\n" +
			"Page.fileChooserOpened + DOM.setFileInputFiles: the OS dialog never opens.\n" +
			"Use this for sites that create the file input dynamically or open the file\n" +
			"dialog directly; when a stable <input type=file> exists in the DOM, prefer\n" +
			"'borz upload <ref> <file>...'. Paths are resolved on the daemon's filesystem\n" +
			"(where Chrome runs) — for remote daemons the files must live on that host.",
	},

	// --- WebAuthn virtual authenticators ---
	"webauthn": {
		Summary: "Control a target tab's Chrome WebAuthn virtual authenticators.",
		Usage:   "borz webauthn <enable|disable|add|credentials|remove|set-user-verified|set-automatic-presence> [options] [--tab <id>]",
		Flags: []string{
			"  enable                                     Enable CDP WebAuthn for the tab",
			"  add [options]                               Add a typed virtual authenticator",
			"  credentials <authenticator-id>              Observe registered credentials",
			"  set-user-verified <id> <true|false>         Change user-verification result",
			"  set-automatic-presence <id> <true|false>    Change automatic presence",
			"  remove <authenticator-id>                   Remove the authenticator",
			"  disable                                    Disable WebAuthn and clear virtual state",
		},
		Examples: []string{
			"  borz webauthn enable --tab ab1c",
			"  borz webauthn add --tab ab1c --json",
			"  borz webauthn credentials AUTHENTICATOR_ID --tab ab1c --json",
			"  borz webauthn set-user-verified AUTHENTICATOR_ID false --tab ab1c",
			"  borz webauthn remove AUTHENTICATOR_ID --tab ab1c",
			"  borz webauthn disable --tab ab1c",
		},
		Notes: "Virtual authenticators are scoped to the selected tab's CDP session and are\n" +
			"intended for test automation only. Run enable before add. The add defaults are\n" +
			"Passkey-ready: CTAP2, internal transport, resident key, user-verification\n" +
			"capability, isUserVerified=true, and automatic presence=true. The response to\n" +
			"add contains data.result.authenticatorId; keep it for subsequent commands.\n" +
			"Removing the authenticator and then disabling WebAuthn is the clean lifecycle.",
	},
	"webauthn.enable": {
		Summary:  "Enable Chrome's WebAuthn CDP domain for one tab.",
		Usage:    "borz webauthn enable [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn enable --tab ab1c --json"},
		Notes:    "Enable is target-session scoped. Run it before adding a virtual authenticator.",
	},
	"webauthn.disable": {
		Summary:  "Disable Chrome's WebAuthn CDP domain for one tab.",
		Usage:    "borz webauthn disable [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn disable --tab ab1c"},
		Notes:    "Disabling clears virtual-authenticator state for that target session. Remove known authenticators first when you want explicit lifecycle evidence.",
	},
	"webauthn.add": {
		Summary: "Add a typed Chrome WebAuthn virtual authenticator.",
		Usage:   "borz webauthn add [--protocol ctap2|u2f] [--transport internal|usb|nfc|ble] [boolean options] [--tab <id>] [--json]",
		Flags: []string{
			"  --protocol <ctap2|u2f>                 Authenticator protocol (default ctap2)",
			"  --transport <internal|usb|nfc|ble>     Authenticator transport (default internal)",
			"  --has-resident-key <true|false>        Resident/discoverable credentials (default true)",
			"  --has-user-verification <true|false>   UV capability (default true)",
			"  --is-user-verified <true|false>         Initial UV result (default true)",
			"  --automatic-presence <true|false>      Auto-satisfy presence requests (default true)",
		},
		Examples: []string{
			"  borz webauthn add --tab ab1c --json",
			"  borz webauthn add --automatic-presence=false --is-user-verified=false --tab ab1c --json",
			"  borz webauthn add --protocol u2f --transport usb --has-resident-key=false --has-user-verification=false --is-user-verified=false",
		},
		Notes: "Run 'webauthn enable' first. Safe Passkey defaults make a CTAP2 internal\n" +
			"authenticator with resident keys, user verification, verified-user state,\n" +
			"and automatic presence. Every boolean option requires an explicit true/false\n" +
			"value. JSON returns the authenticator ID at data.result.authenticatorId.",
	},
	"webauthn.credentials": {
		Summary:  "List credentials stored in a virtual authenticator.",
		Usage:    "borz webauthn credentials <authenticator-id> [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn credentials AUTHENTICATOR_ID --tab ab1c --json"},
		Notes:    "Returns Chrome's structured virtual credentials at data.result.credentials. The alias 'list-credentials' is also accepted.",
	},
	"webauthn.remove": {
		Summary:  "Remove a virtual authenticator from one tab's CDP session.",
		Usage:    "borz webauthn remove <authenticator-id> [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn remove AUTHENTICATOR_ID --tab ab1c --json"},
		Notes:    "The alias 'remove-authenticator' is also accepted. Follow with 'webauthn disable' when the test is finished.",
	},
	"webauthn.set-user-verified": {
		Summary:  "Control whether a virtual authenticator reports a verified user.",
		Usage:    "borz webauthn set-user-verified <authenticator-id> <true|false> [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn set-user-verified AUTHENTICATOR_ID false --tab ab1c"},
		Notes:    "Use false to test a failed/not-completed user-verification path, then true before the successful ceremony.",
	},
	"webauthn.set-automatic-presence": {
		Summary:  "Control automatic user-presence simulation for a virtual authenticator.",
		Usage:    "borz webauthn set-automatic-presence <authenticator-id> <true|false> [--tab <id>] [--json]",
		Examples: []string{"  borz webauthn set-automatic-presence AUTHENTICATOR_ID false --tab ab1c"},
		Notes:    "Disable automatic presence when a test needs the ceremony to remain pending; re-enable it to let Chrome complete the request.",
	},

	// --- Page-level emulation ---
	"page": {
		Summary: "Tab-level page emulation controls (visibility).",
		Usage:   "borz page visibility [visible|hidden|reset] [--tab <id>]",
		Notes:   "See 'borz help page visibility'.",
	},
	"page.visibility": {
		Summary: "Override what the page believes about its own visibility.",
		Usage:   "borz page visibility [visible|hidden|reset] [--tab <id>]",
		Flags: []string{
			"  (no argument)   Report document.visibilityState and any active override",
			"  visible         Make the page report 'visible' and fire visibilitychange",
			"  hidden          Make the page report 'hidden' (test background behavior)",
			"  reset           Remove the override and return to native visibility",
		},
		Examples: []string{
			"  borz page visibility",
			"  borz page visibility visible",
			"  borz page visibility reset",
		},
		Notes: "Backgrounded/minimized Chrome windows report visibilityState 'hidden' and\n" +
			"many apps gate work on it (uploads, polling, media). CDP has no native\n" +
			"visibility override, so this overrides document.visibilityState /\n" +
			"document.hidden in JS, fires a synthetic visibilitychange event, emulates\n" +
			"focus, and sets the web lifecycle state to active. The override persists\n" +
			"across navigations in the tab until reset. It cannot un-throttle\n" +
			"requestAnimationFrame / compositor rendering — when real rendering matters,\n" +
			"use 'borz tab front' to actually unhide the window.",
	},

	// --- Site adapters ---
	"site": {
		Summary: "Run or inspect platform-specific scrapers (twitter/search, hackernews/top, ...).",
		Usage:   "borz site [subcommand] [--scope client|server]",
		Flags: []string{
			"  --scope <client|server>  Adapter catalog (default client)",
			"  list                     List all adapters grouped by platform (default)",
			"  search <query>           Fuzzy-search adapters by name/description",
			"  info <name>              Show an adapter's args, domain, and example",
			"  update [--ref <ref>]     Pull the community adapter pack, optionally pinned",
			"  new <name>               Scaffold a local adapter template",
			"  lint <name|path>         Validate adapter metadata and wrapper buildability",
			"  trust <name>             Trust the current SHA256 of a community adapter",
			"  run <name> [args...]     Run an adapter (equivalent to 'borz <name> ...')",
		},
		Examples: []string{
			"  borz site list",
			"  borz --profile mini site list --scope server",
			"  borz site info hackernews/top",
			"  borz hackernews/top",
			"  borz --profile mini hackernews/top --scope client",
			"  borz twitter/search 'claude code'",
		},
		Notes: "Any '<platform>/<adapter>' invocation is forwarded to 'site run' — " +
			"'borz hackernews/top' and 'borz site run hackernews/top' are equivalent. " +
			"Client scope reads adapters and trust from the CLI host, then sends the built " +
			"JavaScript to the selected daemon; server scope uses adapters and trust on that " +
			"daemon. update/new/lint/trust are client-only. Run 'borz site info <name>' to " +
			"see required args, read-only status, source, and hash.",
	},

	// --- Utility / infra ---
	"fetch": {
		Summary: "Issue an authenticated HTTP request from inside the page (inherits cookies).",
		Usage:   "borz fetch <url> [--method <M>] [--header 'Name: value'] [--body <data>] [--tab <id>]",
		Flags: []string{
			"  --method <M>          HTTP method (default: GET)",
			"  --header <N: V>       Request header; repeat for multiple headers",
			"  --body <data>         Raw request body (including an empty body via --body=)",
		},
		Examples: []string{
			"  borz fetch https://api.github.com/user",
			"  borz fetch https://example.com/api/x --method POST --header 'Content-Type: application/json' --body '{\"ok\":true}'",
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
		Notes: "Checks: active executable and every distinct borz on PATH (canonical path,\n" +
			"embedded version, SHA-256, and drift), home directory, daemon.json,\n" +
			"daemon process & HTTP, CDP connection,\n" +
			"open tabs, and direct CDP discovery. Exits non-zero if any check fails;\n" +
			"warnings (e.g. duplicate binaries or daemon not started) do not fail.\n" +
			"PATH candidates are inspected without executing them. Use --json for machine output.",
	},
	"logs": {
		Summary: "Inspect privacy-safe local operational logs and failure statistics.",
		Usage:   "borz logs [path|tail|stats] [--lines N] [--since 7d] [--json]",
		Notes: "Logs are JSONL files under ~/.borz/logs/<profile>. They contain tool/action\n" +
			"metadata, latency, outcome, and error codes, but never field text, eval scripts,\n" +
			"clipboard data, cookies, headers, URL queries, snapshots, or response bodies.\n" +
			"Files rotate at 10 MiB with five backups per component.",
	},
	"logs.path": {
		Summary: "Print the active profile's operational log directory.",
		Usage:   "borz logs path [--json]",
	},
	"logs.tail": {
		Summary: "Show the newest operational log events.",
		Usage:   "borz logs tail [--lines N] [--json]",
		Flags:   []string{"  --lines <n>   Number of events to show (default 50)"},
	},
	"logs.stats": {
		Summary: "Aggregate per-operation failures, latency, error codes, and bursts.",
		Usage:   "borz logs stats [--since 7d] [--json]",
		Flags:   []string{"  --since <duration|time>   Lookback such as 24h/7d, or RFC3339 (default 7d)"},
		Notes: "Shows per-action/tool counts, failures, p50/p95/max latency, and the ten\n" +
			"largest bursts with at least five identical operations in one second.",
	},
	"feedback": {
		Summary: "Record usage feedback (friction, bugs, ideas) to a local file for later review.",
		Usage:   "borz feedback <message> [--category <c>] [--command <cmd>] | list [--limit N] | path",
		Flags: []string{
			"  <message>             Free-form feedback text (bare words are joined)",
			"  --category <c>        One of: ux, bug, feature, docs, perf",
			"  --command <cmd>       The borz command the feedback is about (e.g. snapshot)",
			"  list [--limit N]      Show recorded feedback, oldest first (default last 50)",
			"  path                  Print the feedback file path",
		},
		Examples: []string{
			"  borz feedback \"snapshot output is too verbose on long pages\" --category ux --command snapshot",
			"  borz feedback --category feature \"want a --quiet flag on open\"",
			"  borz feedback list --limit 10",
		},
		Notes: "Intended for agents: when a borz command is confusing, missing a flag, or " +
			"produces unhelpful output, record it here so a maintainer can review and improve " +
			"the tool later. Entries append to ~/.borz/feedback.jsonl (JSONL; one entry per " +
			"line with time, version, profile, session, category, command, message). Purely " +
			"local — nothing is uploaded. Use 'add' explicitly when the message itself starts " +
			"with a subcommand word: borz feedback add \"list output is misaligned\".",
	},
	"feedback.add": {
		Summary: "Append one feedback entry to ~/.borz/feedback.jsonl.",
		Usage:   "borz feedback [add] <message> [--category <c>] [--command <cmd>]",
		Notes:   "'add' is optional; any message not starting with list/path/add works without it.",
	},
	"feedback.list": {
		Summary: "Show recorded feedback entries, oldest first.",
		Usage:   "borz feedback list [--limit N] [--json]",
		Flags:   []string{"  --limit <n>   Number of newest entries to show (default 50)"},
	},
	"feedback.path": {
		Summary: "Print the feedback file path.",
		Usage:   "borz feedback path [--json]",
	},
	"daemon": {
		Summary: "Start or control the local daemon (loopback only).",
		Usage:   "borz daemon [status|token|restart|shutdown|stop] [--profile N] [--host H --port P --cdp-host H --cdp-port P]",
		Flags: []string{
			"  (no subcommand)        Start the daemon in the foreground",
			"  status                 Show JSON status (or 'not running')",
			"  token [--copy]         Print the token accepted by the running daemon",
			"  restart                Replace only the daemon; preserve managed Chrome",
			"  shutdown|stop          Ask the running daemon to exit",
			"  --profile <n>          Use a named local browser profile",
			"  --host <h>             Bind address (default 127.0.0.1)",
			"  --port <p>             Listen port (default 19824)",
			"  --cdp-host <h>         Chrome DevTools host (default 127.0.0.1)",
			"  --cdp-port <p>         Chrome DevTools port (default 19825)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (flag > env BORZ_TAB_IDLE_TIMEOUT > profile idleTabTimeout > 0; default disabled)",
			"  --max-tabs <n>          Keep at most <n> page tabs; close oldest non-current tabs",
			"                         (flag > env BORZ_MAX_TABS > profile maxTabs > 30; 0=unlimited)",
		},
		Notes: "Restart verifies the local daemon PID through its loopback health endpoint, then replaces only that process so the managed Chrome, tabs, and browser session survive. It can recover a stale daemon whose daemon.json is missing when the selected profile has a fixed daemon port (the undeclared default uses 19824). Shutdown is graceful and closes an auto-started Chrome instance owned by borz. External CDP-profile browsers are never closed. For a remote-accessible server with auth, use 'borz server' instead.",
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
			"  --ensure-browser       Launch the managed browser whenever the CDP endpoint",
			"                         is unreachable (start and reconnect); loopback --cdp-host only",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes",
			"                         (flag > env BORZ_TAB_IDLE_TIMEOUT > profile idleTabTimeout > 0; default disabled)",
			"  --max-tabs <n>          Keep at most <n> page tabs; close oldest non-current tabs",
			"                         (flag > env BORZ_MAX_TABS > profile maxTabs > 30; 0=unlimited)",
		},
		Examples: []string{
			"  borz server --host 127.0.0.1",
			"  borz server --host 0.0.0.0 --token \"$BORZ_TOKEN\"",
			"  borz server --host 0.0.0.0 --token \"$BORZ_TOKEN\" --ensure-browser",
			"  borz server shutdown",
		},
		Notes: "Clients authenticate with 'Authorization: Bearer <token>'. " +
			"Swagger UI is served at /docs and the OpenAPI spec at /openapi.yaml. " +
			"A managed browser actually launched by --ensure-browser is closed when the server shuts down; a pre-existing CDP endpoint is left running.",
	},
	"service": {
		Summary: "Install or control borz as a Windows service.",
		Usage:   "borz service [install|uninstall|start|stop|status] [--name N] [server flags]",
		Flags: []string{
			"  install                Register or update a Windows service that runs 'borz server'",
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
			"  --ensure-browser       Launch the managed browser whenever the CDP endpoint",
			"                         is unreachable (loopback --cdp-host only)",
			"  --idle-tab-timeout <m> Auto-close tabs idle for <m> minutes (default 0=disabled)",
			"  --max-tabs <n>          Keep at most <n> page tabs (default 30; 0=unlimited)",
		},
		Examples: []string{
			"  borz service install",
			"  borz service start",
			"  borz service install --host 0.0.0.0 --token \"$env:BORZ_TOKEN\"",
			"  borz service status",
			"  borz service uninstall",
		},
		Notes: "Windows service management requires an elevated shell. Re-running install " +
			"updates the service command line; restart a running service for new flags to take effect. " +
			"The service entry runs the REST server in the foreground under the Windows Service Control Manager. " +
			"Non-Windows platforms should use launchd, systemd, or a process manager instead.",
	},
	"browser": {
		Summary: "Inspect or repair which Chrome borz owns as this profile's managed browser.",
		Usage:   "borz browser [status|adopt] [--port <p>] [--json]",
		Flags: []string{
			"  status                 Recorded vs live browser identity on the port (default)",
			"  adopt                  Record the browser now on that port as borz's own",
			"  --port <p>             CDP port to inspect (default: the recorded one, else 19825)",
		},
		Examples: []string{
			"  borz browser status",
			"  borz browser adopt              # after 'managed browser identity mismatch'",
			"  borz browser adopt --port 19825",
		},
		Notes: "borz records the exact Chrome instance it launched (~/.borz/browser/browser.json)\n" +
			"so it never attaches to, or closes, a browser that is not its own. If that record\n" +
			"goes stale — a browser launched by a borz old enough not to record identities, or\n" +
			"a lost record — commands fail with 'managed browser identity mismatch' rather than\n" +
			"guessing. 'browser adopt' is the explicit fix: it records whatever is answering on\n" +
			"the port. Only adopt a browser you know is borz's own; adopting someone else's\n" +
			"Chrome means driving, and eventually closing, their session.",
	},
	"browser.status": {
		Summary: "Show the recorded managed-browser identity next to the one now on the port.",
		Usage:   "borz browser status [--port <p>] [--json]",
	},
	"browser.adopt": {
		Summary: "Record the browser currently on the CDP port as this profile's managed browser.",
		Usage:   "borz browser adopt [--port <p>] [--json]",
		Examples: []string{
			"  borz browser adopt",
			"  borz browser adopt --port 19825",
		},
	},
	"profile": {
		Summary: "Manage named browser targets in ~/.borz/profiles.json (managed, cdp, or remote transport).",
		Usage:   "borz profile [list|show|add|set|rm|purge] [<name>] [flags]",
		Flags: []string{
			"  list                     Declared profiles: name, transport, target, description",
			"  list --all               Every profile with local state, declared or not:",
			"                           status, disk usage, last used",
			"  show <name>              One profile's details (token redacted)",
			"  add <name>               Declare a profile (pick exactly one transport)",
			"  set <name>               Edit a declared profile in place",
			"  rm <name>                Delete a profile from the registry",
			"  purge <name>             Reclaim a profile's daemon, browser, and files",
			"  --logs                   Purge the profile's logs too (purge only)",
			"  --force                  Actually purge; without it, purge only previews",
			"  --managed                Transport: borz launches and owns a local Chrome",
			"  --cdp <url|host:port>    Transport: attach to an existing CDP endpoint",
			"  --remote <url>           Transport: talk HTTP to a remote borz server",
			"  --token <t>              Bearer token for --remote (env BORZ_TOKEN)",
			"  --daemon-port <p>        Stable local extension bridge port for managed/cdp",
			"                           ('dynamic' clears it; named profiles default dynamic)",
			"  --daemon-token <t>       Stable local extension bridge token for managed/cdp",
			"                           ('generate' creates one; 'dynamic' clears it)",
			"  --description <text>     One line saying what this profile is for",
			"                           (\"\" clears it); shown by list/show",
			"  --idle-tab-timeout <m>   Idle-tab auto-close in minutes for managed/cdp",
			"                           (0=disable, 'default'=unset; invalid for remote)",
			"  --max-tabs <n>           Maximum page tabs for managed/cdp",
			"                           (0=unlimited, 'default'=unset; invalid for remote)",
			"  --no-check               Save without probing the target",
		},
		Examples: []string{
			"  borz profile add mini --remote http://100.64.0.1:13333 --token \"$BORZ_TOKEN\"",
			"  borz profile add mdt --cdp 127.0.0.1:19845 --idle-tab-timeout 0 \\",
			"      --description \"MDT VPN Chrome via the SSH tunnel; never reap its tabs\"",
			"  borz profile add clean --managed --daemon-port 19827 --daemon-token generate \\",
			"      --description \"throwaway logged-out Chrome\"",
			"  borz --profile mini open https://example.com",
			"  BORZ_PROFILE=mdt borz snapshot",
		},
		Notes: "A profile is the single handle for \"which browser am I driving\". Undeclared\n" +
			"names (including 'default') resolve to the managed transport — today's\n" +
			"behaviour. cdp profiles never launch a browser: if the endpoint is down the\n" +
			"command fails instead of silently starting a managed Chrome. remote profiles\n" +
			"run no local daemon at all. idleTabTimeout and maxTabs are carried onto\n" +
			"auto-spawned daemons. idleTabTimeout defaults to 0 (disabled); maxTabs\n" +
			"defaults to 30. Each uses flag > env > profile > default.\n" +
			"description is free text (one line) that only exists so 'profile list' can\n" +
			"say which browser a name means — run it before picking a profile instead of\n" +
			"guessing from the name.\n" +
			"daemonPort/daemonToken pin the local extension bridge across daemon restarts;\n" +
			"restart a running daemon after changing either field. 'daemon token --copy'\n" +
			"returns the token accepted right now without exposing it in profile show/list.\n" +
			"profiles.json is stored with 0600 permissions\n" +
			"because it can hold bearer tokens; show/list never print them.",
	},
	"profile.list": {
		Summary: "List declared profiles with their transport, target, and description.",
		Usage:   "borz profile list [--all] [--json]",
		Flags: []string{
			"  --all                    Include undeclared profiles and show runtime state",
		},
		Examples: []string{
			"  borz profile list",
			"  borz profile list --all",
		},
		Notes: "Run this before choosing a profile: the description column says what each\n" +
			"one is for, which the name alone rarely does.\n" +
			"Plain list reads profiles.json only, so it cannot show a profile that was\n" +
			"never declared — and any '--profile <name>' creates runtime state whether or\n" +
			"not the name is declared. --all scans disk instead: it adds every profile\n" +
			"with a runtime directory or logs, plus a STATUS column (live, daemon only,\n" +
			"browser only, idle, logs only), how much disk it holds, and when it was last\n" +
			"used. 'browser only' means a managed Chrome is running that borz has no daemon\n" +
			"record for: normal right after a daemon exits, a leak once LAST USED says the\n" +
			"profile is out of use. Reclaim it with 'borz profile purge <name>'.",
	},
	"profile.purge": {
		Summary: "Stop a profile's daemon and browser, then delete its runtime state.",
		Usage:   "borz profile purge <name> [--logs] [--force] [--json]",
		Flags: []string{
			"  --logs                   Delete the profile's logs as well",
			"  --force                  Perform the purge; without it nothing is deleted",
		},
		Examples: []string{
			"  borz profile purge tmact-run-42",
			"  borz profile purge tmact-run-42 --force",
			"  borz profile purge old-experiment --logs --force",
		},
		Notes: "Previews by default and deletes nothing until --force: a managed profile's\n" +
			"browser directory holds real cookies and logins. Order is daemon first (a\n" +
			"daemon started with --close-owned-browser closes its Chrome on the way out),\n" +
			"then any browser that outlived it, then the files. A browser is only closed\n" +
			"after its CDP identity matches the one borz recorded for that profile, so a\n" +
			"stale port cannot take down someone else's Chrome.\n" +
			"Unlike 'profile rm' this works on undeclared names — that is the point, since\n" +
			"undeclared profiles are exactly the ones nothing else can clean up. It leaves\n" +
			"profiles.json alone; use 'profile rm' to drop a declaration.\n" +
			"The default profile cannot be purged: its runtime directory is the borz home\n" +
			"itself, which holds every other profile's state.",
	},
	"profile.show": {
		Summary: "Show one profile's transport, target, and purpose; tokens are redacted.",
		Usage:   "borz profile show <name> [--json]",
	},
	"profile.add": {
		Summary: "Declare a new profile with exactly one transport.",
		Usage:   "borz profile add <name> (--managed | --cdp <url|host:port> | --remote <url> [--token <t>]) [--daemon-port <p>] [--daemon-token <token|generate>] [--description <text>] [--idle-tab-timeout <m>] [--max-tabs <n>] [--no-check]",
		Flags: []string{
			"  --managed                borz launches and owns a local Chrome (default behaviour)",
			"  --cdp <url|host:port>    Attach to an existing CDP endpoint; never launches a browser",
			"  --remote <url>           Route commands to a remote borz server; no local daemon",
			"  --token <t>              Bearer token for --remote (env BORZ_TOKEN)",
			"  --daemon-port <p>        Fixed local daemon/extension bridge port; managed/cdp only",
			"  --daemon-token <t>       Fixed bridge token, or 'generate'; managed/cdp only",
			"  --description <text>     One line saying what this profile is for; any transport",
			"  --idle-tab-timeout <m>   Idle-tab auto-close in minutes (0=disable); managed/cdp only",
			"  --max-tabs <n>            Maximum page tabs (0=unlimited); managed/cdp only",
			"  --no-check               Skip probing (/status for remote, /json/version for cdp)",
		},
		Examples: []string{
			"  borz profile add mini --remote http://server:13333 --token \"$BORZ_TOKEN\"",
			"  borz profile add mdt --cdp 127.0.0.1:19845 --daemon-port 19826 --daemon-token generate",
			"  borz profile add mini --remote http://server:13333 --description \"Mac Mini's logged-in Chrome\"",
		},
	},
	"profile.set": {
		Summary: "Edit a declared profile: switch transport or update token/description/target/tab lifecycle settings.",
		Usage:   "borz profile set <name> [--managed | --cdp <url|host:port> | --remote <url>] [--token <t>] [--daemon-port <p|dynamic>] [--daemon-token <token|generate|dynamic>] [--description <text>] [--idle-tab-timeout <m|default>] [--max-tabs <n|default>] [--no-check]",
		Examples: []string{
			"  borz profile set mini --token \"$NEW_TOKEN\"",
			"  borz profile set mdt --cdp 127.0.0.1:9222",
			"  borz profile set mdt --description \"MDT VPN Chrome (SSH tunnel); work sites only\"",
			"  borz profile set mdt --description \"\"          # drop the description again",
			"  borz profile set mdt --idle-tab-timeout 0        # never auto-close its tabs",
			"  borz profile set mdt --idle-tab-timeout default  # back to flag/env/0 (disabled)",
			"  borz profile set mdt --max-tabs 30               # cap runaway tab creation",
			"  borz profile set mdt --daemon-port 19826 --daemon-token generate",
		},
	},
	"profile.rm": {
		Summary:  "Remove a profile from the registry; the name then resolves to managed again.",
		Usage:    "borz profile rm <name>",
		Examples: []string{"  borz profile rm mdt", "  borz profile remove mdt   # alias"},
	},
	"profile.remove": {
		Summary: "Alias for 'profile rm'.",
		Usage:   "borz profile remove <name>",
	},
	"client": {
		Summary: "Deprecated remote-client commands; superseded by 'borz profile'.",
		Usage:   "borz client [setup|status|enable|disable]",
		Flags: []string{
			"  setup <url>            Write a remote profile (named 'remote', or --as <name>)",
			"  setup --url <url>      Same as positional URL",
			"  BORZ_SERVER_URL        Env fallback when setup URL is omitted",
			"  --token <t>            Bearer token for the remote server",
			"  BORZ_TOKEN             Env fallback when --token is omitted",
			"  --as <name>            Profile name to write (default 'remote')",
			"  --no-check             Store config without probing /status",
			"  status                 Show the 'remote' profile's config",
			"  enable|disable         Deprecated no-ops kept for compatibility",
		},
		Examples: []string{
			"  borz profile add remote --remote http://server:19824 --token \"$BORZ_TOKEN\"   # preferred",
			"  borz client setup http://server:19824 --token \"$BORZ_TOKEN\"                  # deprecated",
			"  borz --profile remote open https://example.com",
		},
		Notes: "'client setup' now writes a remote-transport profile into ~/.borz/profiles.json " +
			"(0600; tokens never printed). Bare --remote is a deprecated alias for " +
			"--profile remote. A legacy ~/.borz/client.json is migrated into the 'remote' " +
			"profile automatically and left on disk for rollback. " +
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
		Usage:   "borz extension [download|update|install|path|status|capabilities|ping|call]",
		Flags: []string{
			"  download              Download the latest extension zip and extract it (default)",
			"  update                Alias for 'download' — overwrites the current install",
			"  install               Alias for 'download'",
			"  path                  Print the local install directory and exit",
			"  status                Query the connected extension capabilities",
			"  status --all-profiles Audit every profile without starting browsers",
			"  capabilities          Alias for 'status'",
			"  ping                  Verify extension RPC end-to-end",
			"  call <method> [json]  Raw extension RPC escape hatch",
		},
		Examples: []string{
			"  borz extension download",
			"  borz extension path",
			"  borz extension status --json",
			"  borz extension status --all-profiles",
			"  borz --profile clean extension ping",
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
		Usage:   "borz extension status [--all-profiles] [--json]",
		Notes:   "Requires the extension service worker to be connected to /v1/ext/ws. --all-profiles audits default plus every declared profile without auto-starting an offline browser.",
	},
	"extension.capabilities": {
		Summary: "Alias for 'extension status'.",
		Usage:   "borz extension capabilities [--all-profiles] [--json]",
	},
	"extension.ping": {
		Summary:  "Verify the selected profile's extension RPC bridge end-to-end.",
		Usage:    "borz extension ping [--json]",
		Examples: []string{"  borz --profile clean extension ping"},
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
		Flags: []string{
			"  --all   Dump every registered command's help",
		},
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
		Summary: "Open a new tab, optionally pointed at a URL (default about:blank).",
		Usage:   "borz tab new [url] [--viewport <preset|WxH>] [--dpr N] [--mobile] [--touch|--no-touch]",
		Flags: []string{
			"  --viewport <preset|WxH>  Apply a viewport before navigation",
			"  --dpr <n>                Device scale factor (default 1)",
			"  --mobile                 Enable mobile device metrics",
			"  --touch / --no-touch     Enable or disable touch emulation",
		},
		Examples: []string{
			"  borz tab new",
			"  borz tab new https://github.com",
			"  borz tab new https://example.com --viewport mobile",
		},
		Notes: "Unlike 'borz open', this always creates a fresh tab and never reuses an " +
			"existing one. Use 'open --new' if you want the same force-new behavior from the " +
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
	"tab.front": {
		Summary: "Bring a tab to the real OS foreground (default: the currently active tab).",
		Usage:   "borz tab front [n|--id <short-id>]",
		Examples: []string{
			"  borz tab front",
			"  borz tab front 2",
			"  borz tab front --id abc123",
		},
		Notes: "'tab select' activates a tab inside Chrome, but a minimized or occluded\n" +
			"window still reports document.visibilityState 'hidden' and pages throttle\n" +
			"accordingly (uploads stall, timers slow, media pauses). 'tab front'\n" +
			"additionally restores the Chrome window if minimized (Browser.setWindowBounds)\n" +
			"and focuses the page (Page.bringToFront), so the page becomes really visible.\n" +
			"The response reports the resulting visibilityState so scripts can verify.\n" +
			"If the page must merely BELIEVE it is visible (headless-ish automation),\n" +
			"see 'borz page visibility'.",
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
		Usage:    "borz site [list] [--scope client|server]",
		Examples: []string{"  borz site", "  borz site list", "  borz --profile mini site list --scope server"},
		Notes: "Entries tagged [local] come from your workspace; the rest are from the " +
			"community pack. Use 'site update' to refresh the community pack.",
	},
	"site.search": {
		Summary:  "Fuzzy-search adapters by name, description, or domain.",
		Usage:    "borz site search <query> [--scope client|server]",
		Examples: []string{"  borz site search hacker", "  borz site search 'linux forum'"},
	},
	"site.info": {
		Summary: "Print an adapter's description, domain, source, example, and args.",
		Usage:   "borz site info <name> [--scope client|server]",
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
		Notes: "Client scope only. Community adapters are cached under the user's config dir. Local adapters " +
			"you've placed in the workspace are not affected. Updates refuse to run when the community repo has local changes.",
	},
	"site.new": {
		Summary:  "Create a local site adapter template.",
		Usage:    "borz site new <platform/name>",
		Examples: []string{"  borz site new github/search"},
		Notes:    "Client scope only. Creates the file under the local sites directory and refuses to overwrite an existing adapter.",
	},
	"site.lint": {
		Summary:  "Validate an adapter's metadata and generated execution wrapper.",
		Usage:    "borz site lint <name-or-path>",
		Examples: []string{"  borz site lint github/search", "  borz site lint ./sites/github/search.js"},
		Notes:    "Client scope only. Checks required metadata, required/default consistency, required args, and adapter readability.",
	},
	"site.trust": {
		Summary:  "Trust the current SHA256 hash of a community adapter.",
		Usage:    "borz site trust <name>",
		Examples: []string{"  borz site trust twitter/search"},
		Notes:    "Client scope only. Community adapters are arbitrary JavaScript. Trust records the current hash in sites-trust.json; hash changes require re-trust or --force.",
	},
	"site.run": {
		Summary: "Run an adapter by name — equivalent to calling 'borz <platform>/<name> ...' directly.",
		Usage:   "borz site run <name> [args...] [--scope client|server] [--tab <id>] [--timeout <ms>] [--unwrap] [--force]",
		Flags: []string{
			"  --scope <scope>  client (default) reads adapters here; server reads them on the daemon",
			"  --timeout <ms>   Override the adapter eval timeout",
			"  --unwrap         Print a string result without JSON quotes",
			"  --force          Bypass domain mismatch and one-off community trust checks",
		},
		Examples: []string{"  borz site run hackernews/top 10", "  borz hackernews/top 10", "  borz --profile mini site run hackernews/top 10 --scope server"},
		Notes: "Use 'borz site info <name>' to discover the args an adapter expects " +
			"before running it. Without --tab, the daemon reuses or opens the adapter's site before running it. " +
			"client scope checks trust on this machine and sends the built script to any selected daemon. " +
			"server scope resolves and checks the adapter on the daemon. Explicit tabs still enforce the " +
			"domain guard; --force bypasses that guard and the selected scope's community trust check.",
	},

	// --- Subcommand pages: daemon.* ---
	"daemon.status": {
		Summary: "Print daemon status; when stopped, explain that the next browser command auto-starts it.",
		Usage:   "borz daemon status",
		Notes:   "Identical payload to the top-level 'borz status'. Status is read-only and does not start the daemon itself.",
	},
	"daemon.token": {
		Summary: "Print or copy the bearer token accepted by the selected profile's local daemon.",
		Usage:   "borz daemon token [--copy] [--json]",
		Flags:   []string{"  --copy   Copy the token to the system clipboard instead of printing it"},
		Examples: []string{
			"  borz daemon token --copy",
			"  borz --profile mdt daemon token --copy",
		},
		Notes: "When a daemon is running, this returns the token it accepts right now. When stopped, a configured stable daemonToken is returned without starting Chrome. Dynamic tokens are only available after the profile has started once. The token is a secret; profile show/list never expose it.",
	},
	"daemon.shutdown": {
		Summary:  "Ask the running daemon to exit cleanly.",
		Usage:    "borz daemon shutdown",
		Examples: []string{"  borz daemon shutdown", "  borz daemon stop   # alias"},
		Notes:    "Also closes a managed Chrome instance owned by borz. Browsers attached through a cdp profile are never closed.",
	},
	"daemon.restart": {
		Summary:  "Replace only the verified local daemon while preserving managed Chrome.",
		Usage:    "borz daemon restart [--json]",
		Examples: []string{"  borz daemon restart", "  borz daemon restart --json"},
		Notes:    "Uses the loopback-only health endpoint to verify the exact daemon PID, then forcibly replaces that process without running browser cleanup. This preserves the managed Chrome process, tab IDs, and browser session. --json returns success, previousPid (when present), newPid, recoveredStale (when applicable), and browserPreserved; failures return success:false plus error. The default profile and named profiles configured with --daemon-port can also recover an older stale daemon with no daemon.json; a named profile still using a dynamic port is refused because its daemon cannot be safely guessed. In-flight daemon requests or recordings are interrupted. Refuses remote profiles, non-loopback servers, and unverified PIDs.",
	},
	"daemon.stop": {
		Summary:  "Alias for 'daemon shutdown'. Asks the running daemon to exit cleanly.",
		Usage:    "borz daemon stop",
		Examples: []string{"  borz daemon stop"},
		Notes:    "Also closes a managed Chrome instance owned by borz. Browsers attached through a cdp profile are never closed.",
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
		Summary: "Register or update borz as a Windows service that runs the REST server.",
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

	// --- Subcommand pages: client.* (deprecated surface) ---
	"client.setup": {
		Summary: "Deprecated: write a remote-transport profile (prefer 'borz profile add').",
		Usage:   "borz client setup <server-url> [--token <token>] [--as <profile>] [--no-check]",
		Flags: []string{
			"  --url <url>      Same as positional server URL",
			"  --token <token>  Bearer token for the remote server",
			"  --as <name>      Profile name to write (default 'remote')",
			"  --no-check       Save config without probing /status",
		},
		Examples: []string{
			"  borz client setup http://127.0.0.1:19824",
			"  borz client setup --url http://127.0.0.1:19824 --no-check",
			"  BORZ_SERVER_URL=http://127.0.0.1:19824 BORZ_TOKEN=secret borz client setup",
			"  borz client setup https://browser.example.com --token \"$BORZ_TOKEN\"",
		},
		Notes: "Writes the profile into ~/.borz/profiles.json and probes the server's " +
			"authenticated /status endpoint first unless --no-check is set. If the URL has " +
			"no scheme, http:// is assumed. Route commands with --profile <name> (bare " +
			"--remote still selects the 'remote' profile). When <server-url> or --token is " +
			"omitted, setup falls back to BORZ_SERVER_URL and BORZ_TOKEN respectively.",
	},
	"client.enable": {
		Summary: "Deprecated no-op; routing follows the profile's transport.",
		Usage:   "borz client enable",
		Notes:   "Kept for compatibility only. Select a remote profile with --profile <name> instead.",
	},
	"client.disable": {
		Summary:  "Deprecated no-op; routing follows the profile's transport.",
		Usage:    "borz client disable",
		Examples: []string{"  borz client disable"},
	},
	"client.status": {
		Summary: "Show the 'remote' profile that the deprecated --remote flag selects.",
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

	// --- Subcommand pages: record.* ---
	"record.start": {
		Summary: "Start a daemon-managed recording bundle.",
		Usage:   "borz record start [url] [--url <url>] [--mode cdp|client] [--out <path.borzrec>] [--fps N]",
		Flags: []string{
			"  --id <id>              Recording id",
			"  --url <url>            URL to open before recording",
			"  --mode cdp|client      Capture via CDP tab or extension client",
			"  --out <path.borzrec>   Bundle output path",
			"  --viewport <WxH>       Viewport preset or size for capture",
			"  --fps N                Capture frame rate",
		},
	},
	"record.stop": {
		Summary: "Finalize the active recording.",
		Usage:   "borz record stop [id] [--recover]",
		Flags:   []string{"  --recover   Finalize a partially written recording when possible"},
	},
	"record.pause": {
		Summary: "Pause capture for the active recording.",
		Usage:   "borz record pause [id]",
	},
	"record.resume": {
		Summary: "Resume capture for the active recording.",
		Usage:   "borz record resume [id]",
	},
	"record.list": {
		Summary: "List active and recently completed recordings.",
		Usage:   "borz record list [--json]",
	},
	"record.info": {
		Summary: "Inspect a recording bundle or daemon recording.",
		Usage:   "borz record info <bundle|recording-id> [--json]",
	},
	"record.verify": {
		Summary: "Validate a .borzrec bundle's schema, ordering, and checksums.",
		Usage:   "borz record verify <bundle> [--json]",
	},
	"record.render": {
		Summary: "Render a .borzrec bundle to video or image output.",
		Usage:   "borz record render <bundle> [--preset share] [--out demo.mp4]",
		Flags: []string{
			"  --format <fmt>       Output format, inferred from --out when omitted",
			"  --fps N              Output frame rate",
			"  --width/--height N   Output dimensions",
			"  --ffmpeg <path>      ffmpeg binary path",
		},
	},
	"record.redact": {
		Summary: "Add a render-time redaction to a .borzrec bundle.",
		Usage:   "borz record redact <bundle> --selector <css> | --rect x,y,w,h",
	},
	"record.export": {
		Summary: "Export a stable JSON trace from a .borzrec bundle.",
		Usage:   "borz record export <bundle> --format trace.json [--out trace.json]",
	},
	"record.edit": {
		Summary: "Show bundle details or a daemon preview URL for editing.",
		Usage:   "borz record edit <bundle|recording-id>",
	},
	"record.play": {
		Summary: "Show bundle details or a daemon preview URL for playback.",
		Usage:   "borz record play <bundle|recording-id>",
	},

	// --- Subcommand pages: network.* ---
	"network.requests": {
		Summary: "List network requests captured for the current tab.",
		Usage:   "borz network requests [--filter S] [--method M] [--status C] [--with-body] [--since <seq|last_action>] [--limit <n>] [--tail] [--interval <duration|ms>] [--tab <id>]",
		Flags: []string{
			"  --filter <substr>    Only requests whose URL contains <substr>",
			"  --method <M>         Only requests with HTTP method M (GET, POST, ...)",
			"  --status <code>      Only requests whose response status matches <code>",
			"  --with-body          Include response bodies (heavier payload)",
			"  --limit <n>          Return at most the newest n requests",
			"  --since <seq|last_action>   Only requests newer than this checkpoint",
			"  --tail               Stream new requests as they arrive (Ctrl+C to stop)",
			"  --interval <duration|ms>  Polling interval in --tail mode (default 500ms)",
		},
		Examples: []string{
			"  borz network requests",
			"  borz network requests --filter /api/ --method POST",
			"  borz network requests --since last_action --with-body",
			"  borz network requests --tail --filter /api/",
		},
	},
	"network.clear": {
		Summary: "Drop all captured network requests for the current tab.",
		Usage:   "borz network clear [--tab <id>]",
		Notes:   "Pair with 'network requests --since last_action' if you just want a fresh window.",
	},

	// --- Subcommand pages: dialog.* ---
	"dialog.accept": {
		Summary: "Accept the open dialog (OK / Leave / prompt submitted), or arm the next one.",
		Usage:   "borz dialog accept [prompt-text] [--tab <id>]",
		Examples: []string{
			"  borz dialog accept",
			"  borz dialog accept 'Leo'       # prompt response text",
		},
		Notes: "If a dialog is already open on the tab it is answered immediately; otherwise " +
			"the next one is armed (one-shot), which is the reliable pattern — run it BEFORE the " +
			"click/navigation that triggers the dialog. For a prompt(), pass the response as the " +
			"second arg; for alert()/confirm() it is ignored.",
	},
	"dialog.dismiss": {
		Summary: "Dismiss the open dialog (Cancel / Stay on page), or arm the next one.",
		Usage:   "borz dialog dismiss [--tab <id>]",
		Notes: "If a dialog is already open on the tab it is answered immediately; otherwise the " +
			"next one is armed. Run BEFORE the click/navigation that triggers the dialog.",
	},
	"dialog.disarm": {
		Summary: "Drop a previously armed dialog handler without answering anything.",
		Usage:   "borz dialog disarm [--tab <id>]",
		Notes: "Arming is one-shot but never expires: an armed handler that is never triggered " +
			"would be consumed by whatever unrelated dialog opens next. Disarm when you abandon " +
			"the flow you armed it for.",
	},
	"dialog.status": {
		Summary:  "Report the open dialog, the armed handler, and recent dialog history.",
		Usage:    "borz dialog status [--tab <id>]",
		Examples: []string{"  borz dialog status"},
		Notes: "Read-only. Use it when a tab stops responding: a dialog nobody answered blocks " +
			"the page, and status shows its type and message so you know whether to accept or " +
			"dismiss. History covers the last 10 dialogs on the tab, including ones a human " +
			"clicked away in headful Chrome.",
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
	"tabs":      "tab",
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
	for i, arg := range rawArgs {
		if arg != "--help" && arg != "-h" {
			continue
		}
		if i > 0 && flagConsumesNextArg(rawArgs[i-1]) {
			continue
		}
		return true
	}
	if len(cmdArgs) > 0 && (cmdArgs[0] == "help" || cmdArgs[0] == "--help" || cmdArgs[0] == "-h") {
		return true
	}
	return false
}

func flagConsumesNextArg(arg string) bool {
	return cliValueFlagSet[arg]
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
	if maxN <= 0 {
		return nil
	}

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
