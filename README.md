# bb-browser-go

**Your browser is the API.** A CLI tool that lets you control and observe any Chromium-based browser from the terminal via the Chrome DevTools Protocol (CDP).

`bb-browser-go` is a Go port of [bb-browser](https://github.com/nicepkg/bb-browser) (Node.js). It ships as a single static binary with zero runtime dependencies.

## Installation

### Download prebuilt binary

Grab the latest release for your platform from [GitHub Releases](https://github.com/leolin310148/bb-browser-go/releases):

```bash
# macOS (Apple Silicon)
curl -LO https://github.com/leolin310148/bb-browser-go/releases/latest/download/bb-browser-darwin-arm64
chmod +x bb-browser-darwin-arm64
sudo mv bb-browser-darwin-arm64 /usr/local/bin/bb-browser

# macOS (Intel)
curl -LO https://github.com/leolin310148/bb-browser-go/releases/latest/download/bb-browser-darwin-amd64
chmod +x bb-browser-darwin-amd64
sudo mv bb-browser-darwin-amd64 /usr/local/bin/bb-browser

# Linux (x86_64)
curl -LO https://github.com/leolin310148/bb-browser-go/releases/latest/download/bb-browser-linux-amd64
chmod +x bb-browser-linux-amd64
sudo mv bb-browser-linux-amd64 /usr/local/bin/bb-browser

# Linux (ARM64)
curl -LO https://github.com/leolin310148/bb-browser-go/releases/latest/download/bb-browser-linux-arm64
chmod +x bb-browser-linux-arm64
sudo mv bb-browser-linux-arm64 /usr/local/bin/bb-browser
```

### Build from source

```bash
go install github.com/leolin310148/bb-browser-go@latest
```

Or clone and build:

```bash
git clone https://github.com/leolin310148/bb-browser-go.git
cd bb-browser-go
go build -o bb-browser .
```

## Prerequisites

You need a Chromium-based browser (Google Chrome, Microsoft Edge, Brave, Arc, etc.) installed on your machine.

`bb-browser` connects to the browser using CDP. It will automatically:

1. Detect a running browser with remote debugging enabled
2. Or launch a managed browser instance for you

If you prefer manual control, start Chrome with debugging enabled:

```bash
# macOS
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --remote-debugging-port=19825

# Linux
google-chrome --remote-debugging-port=19825

# Windows
"C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=19825
```

Or point to a remote browser via environment variable:

```bash
export BB_BROWSER_CDP_URL=http://127.0.0.1:19825
```

## Browser Extension (optional)

A small Chrome extension extends `bb-browser` with capabilities CDP cannot provide on its own — cross-domain cookies (`bb-browser cookies all`) and browser-level tab events (`bb-browser tab events`). Install it once per Chrome profile:

```bash
# Download the extension that matches your installed bb-browser binary
bb-browser extension download
```

This fetches `bb-browser-extension.zip` from the latest GitHub release, verifies its SHA-256, and extracts it into `~/.bb-browser/extension/` (the previous install is removed). The command then prints the directory and the load steps:

1. Open `chrome://extensions`
2. Enable **Developer mode** (top-right toggle)
3. Click **Load unpacked** and pick `~/.bb-browser/extension/`

Re-run `bb-browser extension update` after upgrading the binary to keep the extension in lockstep. The extension version mirrors the bb-browser release tag.

Commands that *don't* require the extension keep working without it. The ones that do (`cookies all`, `tab events`) will tell you when it's missing.

## How It Works

```
┌──────────┐       HTTP        ┌──────────┐     WebSocket/CDP     ┌──────────┐
│  bb-cli  │  ───────────────> │  daemon   │  ──────────────────>  │  Chrome  │
│ (client) │  <─────────────── │ (server)  │  <──────────────────  │ (browser)│
└──────────┘    JSON response  └──────────┘     DevTools Protocol  └──────────┘
```

When you run any command, `bb-browser`:

1. **Starts a daemon** (if not already running) that holds a persistent CDP WebSocket connection to Chrome
2. **Sends the command** as an HTTP request to the daemon
3. **The daemon translates** the command into CDP protocol calls and returns the result

The daemon runs in the background and auto-discovers your browser. You don't need to manage it manually.

## MCP Server

`bb-browser` includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server, letting AI assistants like Claude control your browser directly.

### Setup

Start the MCP server:

```bash
bb-browser mcp
```

This runs an MCP server over stdio. To use it with an MCP client, add it to your configuration:

Add to your MCP client configuration (e.g. `.claude/settings.json` for Claude Code):

```json
{
  "mcpServers": {
    "bb-browser": {
      "command": "bb-browser",
      "args": ["mcp"]
    }
  }
}
```

### Available Tools

The MCP server exposes 30 tools:

| Category | Tools |
|----------|-------|
| **Navigation** | `browser_navigate`, `browser_back`, `browser_forward`, `browser_refresh`, `browser_close` |
| **Interaction** | `browser_click`, `browser_hover`, `browser_fill`, `browser_type`, `browser_check`, `browser_uncheck`, `browser_select`, `browser_press`, `browser_scroll` |
| **Observation** | `browser_snapshot`, `browser_screenshot`, `browser_get`, `browser_eval`, `browser_wait` |
| **Tab Management** | `browser_tab_list`, `browser_tab_new`, `browser_tab_select`, `browser_tab_close` |
| **Diagnostics** | `browser_network`, `browser_console`, `browser_errors`, `browser_doctor` |
| **Site Adapters** | `browser_site_list`, `browser_site_info`, `browser_site_run` |

The workflow mirrors the CLI: call `browser_snapshot` to see the page structure with element refs, then use those refs with interaction tools like `browser_click` or `browser_fill`. Screenshots are returned as inline base64 PNG images.

Most action tools accept optional `waitFor` (CSS selector) and `timeout` (ms, default 10000) params — after the action runs, the daemon polls `document.querySelector(waitFor)` until it returns a non-null node or the timeout elapses. Use this for SPA loads or modals instead of fixed `browser_wait` calls.

Other notable params:

- `browser_snapshot` accepts `textOnly: true` for a reader-mode plain-text dump (no element refs) — useful for summarization or feeding the page to an LLM as context.
- `browser_eval` auto-wraps top-level `await` in an async IIFE so `await fetch(...)` works without manual boilerplate. Pass `noAutoAwait: true` to opt out.
- `browser_doctor` runs end-to-end stack diagnostics (binary → daemon → CDP → tabs) and returns the first failing layer with a remediation hint. Pass `json: true` for structured output.

## Server Mode

`bb-browser server` exposes the daemon as a remote-accessible HTTP API with ergonomic `/v1/*` REST routes. It's designed for integrations like n8n, Make, or any workflow tool that can send HTTP requests.

### Start the server

```bash
# Local-only (no auth required)
bb-browser server --host 127.0.0.1 --port 19824

# Remote-accessible (token required)
bb-browser server --host 0.0.0.0 --port 19824 --token "$(openssl rand -hex 16)"

# Or via env
export BB_BROWSER_TOKEN=mysecret
bb-browser server --host 0.0.0.0
```

The server refuses to bind a non-loopback address without a token. Clients authenticate with `Authorization: Bearer <token>`.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--host` | `BB_BROWSER_SERVER_HOST` | `0.0.0.0` | Bind address |
| `--port` | `BB_BROWSER_SERVER_PORT` | `19824` | Bind port |
| `--token` | `BB_BROWSER_TOKEN` | *(none)* | Required for non-loopback host |
| `--cdp-host` | — | `127.0.0.1` | Chrome CDP host |
| `--cdp-port` | — | `19825` | Chrome CDP port |

Stop the server:

```bash
bb-browser server shutdown
```

### Use a remote server from the CLI

Configure the CLI once, then pass `--remote` for commands that should use the
configured server:

```bash
bb-browser client setup http://server-host:19824 --token "$BB_BROWSER_TOKEN"
bb-browser --remote open https://example.com
```

Without `--remote`, browser actions such as `open`, `snapshot`, `click`,
`eval`, `tab`, `network`, and `cookies` always use the local daemon/CDP
connection:

```bash
bb-browser open https://example.com
```

To make only the current shell default to remote while other shells stay local,
define an alias in that shell:

```bash
alias bb-browser='bb-browser --remote'
```

The client config is stored at `~/.bb-browser/client.json` with 0600
permissions because it may contain the bearer token. `client setup` probes the
server's authenticated `/status` endpoint by default; pass `--no-check` only
when you need to save config before the server is reachable.

### REST endpoints

All `/v1/*` routes accept JSON request bodies and return JSON responses shaped as `{id, success, data?, error?}`. Include `Authorization: Bearer <token>` when a token is configured.

The full machine-readable contract is served by the daemon itself:

- `GET /openapi.yaml` — OpenAPI 3.1 spec (unauthenticated)
- `GET /docs` — interactive Swagger UI (unauthenticated)

Point any OpenAPI-aware tool (Postman, Insomnia, n8n's HTTP Request node, `openapi-generator`, `oapi-codegen`, etc.) at `http://<host>:<port>/openapi.yaml` to generate typed clients or import the collection.

| Method | Path | Body fields |
|--------|------|-------------|
| GET | `/healthz` | — *(unauthenticated)* |
| GET | `/openapi.yaml` | — *(unauthenticated, OpenAPI 3.1 spec)* |
| GET | `/docs` | — *(unauthenticated, Swagger UI)* |
| GET | `/status` | — |
| GET | `/v1/tabs` | — |
| POST | `/v1/tabs` | `{url?}` — open new tab |
| POST | `/v1/tabs/select` | `{tabId?, index?}` |
| POST | `/v1/tabs/close` | `{tabId?, index?}` |
| POST | `/v1/open` | `{url, new?, tab?, waitFor?, timeoutMs?}` — reuses a tab with the exact same URL when one exists; `new: true` forces a fresh tab |
| POST | `/v1/back` \| `/forward` \| `/refresh` | `{tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/close` | `{tab?}` |
| POST | `/v1/snapshot` | `{interactive?, compact?, maxDepth?, selector?, role?, mode?, tab?}` — `mode: "text"` returns a reader-mode plain-text dump (no element refs) |
| POST | `/v1/screenshot` | `{path?, tab?}` |
| POST | `/v1/get` | `{attribute, ref?, tab?}` |
| POST | `/v1/click` \| `/hover` \| `/check` \| `/uncheck` | `{ref, tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/fill` \| `/type` | `{ref, text, tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/select` | `{ref, value, tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/press` | `{key, modifiers?, tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/key` | `{keyType?, key?, code?, text?, modifiers?, tab?}` — raw OS-level key input (reaches canvas apps / SSH) |
| POST | `/v1/mouse` | `{mouseType?, x?, y?, button?, deltaX?, deltaY?, clickCount?, modifiers?, tab?}` — raw OS-level mouse input |
| POST | `/v1/clipboard-read` | `{tab?}` — returns `data.value` from `navigator.clipboard.readText()` |
| POST | `/v1/scroll` | `{direction, pixels?, tab?, waitFor?, timeoutMs?}` |
| POST | `/v1/eval` | `{script, tab?, waitFor?, timeoutMs?}` — clients are responsible for any `await` wrapping; the CLI's auto-await is not applied here |
| POST | `/v1/wait` | `{ms?, tab?}` |
| POST | `/v1/network` | `{command?, filter?, method?, status?, withBody?, since?, tab?}` |
| POST | `/v1/console` | `{command?, filter?, since?, tab?}` |
| POST | `/v1/errors` | `{command?, filter?, since?, tab?}` |
| POST | `/v1/fetch` | `{url, method?, tab?}` — authenticated fetch |
| GET \| POST | `/v1/doctor` | — daemon-side health summary (`{ok, checks[]}`); returns 503 on a failing check |
| GET | `/v1/sites` | — list site adapters on the server |
| POST | `/v1/sites/info` | `{name}` — adapter metadata |
| POST | `/v1/sites/run` | `{name, args?, tab?}` — run a site adapter |
| POST | `/command` | raw `protocol.Request` — escape hatch |

`waitFor` (CSS selector) and `timeoutMs` (default 10000) are honored across all action endpoints. After the action returns, the daemon polls `document.querySelector(waitFor)` on a 100 ms tick until it resolves to a non-null node or the timeout elapses, then returns. Combine with chained REST calls instead of guessing with fixed `/v1/wait` durations.

### Example: curl

```bash
TOKEN=mysecret
BASE=http://host:19824

curl -s $BASE/healthz

curl -s -X POST $BASE/v1/open \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'

curl -s -X POST $BASE/v1/snapshot \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"interactive":true,"compact":true}'

curl -s -X POST $BASE/v1/click \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ref":"4"}'
```

### Example: n8n

Use n8n's **HTTP Request** node:

- **Method:** `POST`
- **URL:** `http://bb-host:19824/v1/snapshot`
- **Authentication:** Header Auth → `Authorization: Bearer <token>`
- **Body:** JSON → `{ "interactive": true, "compact": true }`

Chain nodes to open → snapshot → click → extract. A dedicated n8n community node is on the roadmap.

## Quick Start

```bash
# Open a webpage
bb-browser open https://example.com

# Take a snapshot of the page (accessibility tree with element references)
bb-browser snapshot

# Click an element by its ref number from the snapshot
bb-browser click 5

# Fill a text input
bb-browser fill 3 "hello world"

# Get the page title
bb-browser get title

# Take a screenshot
bb-browser screenshot

# Execute JavaScript in the page
bb-browser eval "document.title"

# Get JSON output for scripting
bb-browser snapshot --json

# Filter with jq expressions
bb-browser snapshot --jq ".snapshotData.refs | keys | length"
```

## Commands

### Navigation

| Command | Description |
|---------|-------------|
| `open <url>` | Open a URL. Reuses an existing tab when one has the exact same URL (focus only, no reload); otherwise opens a new tab. Pass `--new` to force a fresh tab, or `--tab <id>` to target a specific tab. |
| `back` | Navigate back in history |
| `forward` | Navigate forward in history |
| `refresh` | Reload the current page |
| `close` | Close the current tab |

```bash
# Open a URL. Reuses an existing tab with the same URL if one exists,
# otherwise creates a new tab. This prevents tab-blowup in automated workflows.
bb-browser open https://github.com

# Force a fresh tab even if one already has this URL
bb-browser open https://github.com --new

# Navigate a specific existing tab by ID
bb-browser open https://github.com --tab ab1c

# Navigate back
bb-browser back

# Wait for a selector before returning (default 10s, override with --timeout <ms>)
bb-browser open https://app.example.com --wait-for ".dashboard-loaded"
```

`--wait-for <selector>` and `--timeout <ms>` work on **every action that changes the page** — `open`, `click`, `hover`, `fill`, `type`, `check`, `uncheck`, `select`, `press`, `scroll`, `eval`, `back`, `forward`, `refresh`. The daemon runs the action, then polls `document.querySelector(...)` on a 100 ms tick until the node appears or the timeout elapses. Prefer this over `wait <ms>` for any DOM change.

### Observation

These commands let you **see** what's on the page.

#### `snapshot` - Get the accessibility tree

The most important command. It returns a structured text representation of the page with **ref numbers** you can use to interact with elements.

```bash
# Full accessibility tree
bb-browser snapshot

# Interactive elements only (buttons, links, inputs, etc.)
bb-browser snapshot -i

# Compact output (shorter names, no tag names)
bb-browser snapshot -c

# Limit tree depth
bb-browser snapshot -d 3

# Filter by selector/keyword
bb-browser snapshot -s "search"

# Combine flags
bb-browser snapshot -i -c

# Reader-mode plain text (title + URL + visible text only) — no element refs.
# Good for "summarize this page" or feeding the page to an LLM as context.
bb-browser snapshot --text-only
```

Example output:

```
- navigation <nav>
  - link [ref=0] "Home" <a>
  - link [ref=1] "About" <a>
  - link [ref=2] "Contact" <a>
- main <main>
  - heading "Welcome" <h1>
  - textbox [ref=3] "Search..." <input>
  - button [ref=4] "Submit" <button>
```

The `[ref=N]` numbers are what you use with interaction commands like `click`, `fill`, `type`, etc.

#### `screenshot`

```bash
# Capture screenshot (returned as base64 data URL in JSON)
bb-browser screenshot

# Save to file (use with --json and jq)
bb-browser screenshot --json --jq ".data.dataUrl"
```

#### `get` - Get element or page attributes

```bash
# Get the current page URL
bb-browser get url

# Get the page title
bb-browser get title

# Get the text content of an element
bb-browser get text 5

# Get an HTML attribute of an element
bb-browser get href 2
bb-browser get class 4
bb-browser get value 3
```

#### `network` - Monitor network requests

```bash
# List all captured network requests
bb-browser network

# Filter by URL pattern
bb-browser network requests --filter "api"

# Filter by HTTP method
bb-browser network requests --method POST

# Filter by status code
bb-browser network requests --status 404
bb-browser network requests --status 5xx

# Include response bodies
bb-browser network requests --with-body

# Show only requests since last action
bb-browser network requests --since last_action

# Live stream new requests as they arrive (Ctrl+C to stop).
# Pairs with --filter / --method / --status and --json (JSONL output).
bb-browser network requests --tail
bb-browser network requests --tail --filter /api/ --method POST

# Clear captured requests
bb-browser network clear
```

#### `console` - Read console messages

```bash
# Get all console messages
bb-browser console

# Filter messages
bb-browser console --filter "error"

# Only messages since last action
bb-browser console --since last_action

# Clear console buffer
bb-browser console --clear
```

#### `errors` - Read JavaScript errors

```bash
# Get all JS errors
bb-browser errors

# Filter errors
bb-browser errors --filter "TypeError"

# Clear error buffer
bb-browser errors --clear
```

### Interaction

All interaction commands use **ref numbers** from the `snapshot` output.

#### `click` / `hover`

```bash
# Click a button (ref=4 from snapshot)
bb-browser click 4

# Hover over an element
bb-browser hover 2
```

#### `fill` / `type`

```bash
# Clear the input and fill with new text
bb-browser fill 3 "hello world"

# Append text to current value (like typing)
bb-browser type 3 " more text"
```

#### `check` / `uncheck`

```bash
# Check a checkbox
bb-browser check 7

# Uncheck it
bb-browser uncheck 7
```

#### `select`

```bash
# Select a dropdown option by value
bb-browser select 6 "option2"
```

#### `press`

```bash
# Press a key
bb-browser press Enter
bb-browser press Tab
bb-browser press ArrowDown
bb-browser press Escape
```

#### `scroll`

```bash
# Scroll down (default 300px)
bb-browser scroll down

# Scroll up 500px
bb-browser scroll up 500

# Scroll left/right
bb-browser scroll left 200
bb-browser scroll right 200
```

#### `eval` - Execute JavaScript

```bash
# Run arbitrary JavaScript in the page context
bb-browser eval "document.title"
bb-browser eval "document.querySelectorAll('a').length"
bb-browser eval "window.location.href"

# Multi-word scripts
bb-browser eval "document.querySelector('h1').textContent"

# Top-level await is auto-wrapped in an async IIFE — no manual boilerplate.
bb-browser eval "await fetch('/api/data').then(r => r.json())"

# Disable auto-wrapping if you need the raw script.
bb-browser eval --no-auto-await "(async () => { ... })()"

# Read a script from disk so long extraction snippets aren't trapped in a shell quote.
bb-browser eval --file ./extract.js

# Inject CLI-supplied JSON values as top-level consts the script can read.
bb-browser eval --file ./greet.js --json-arg user='{"id":7}' --json-arg n=3

# Print the result raw — strings unquoted, other shapes JSON-formatted.
# Removes the need for ' | jq .data.result' on every call.
bb-browser eval --unwrap "document.title"
```

#### `wait`

```bash
# Wait 1 second (default)
bb-browser wait

# Wait 2 seconds
bb-browser wait 2000
```

### Tab Management

```bash
# List all open tabs
bb-browser tab

# Open a new tab
bb-browser tab new
bb-browser tab new https://google.com

# Switch to tab by index
bb-browser tab 0
bb-browser tab 2

# Switch to tab by short ID
bb-browser tab select ab1c

# Close a tab
bb-browser tab close 2
bb-browser tab close --id ab1c
```

Every response includes a short `tab` ID (e.g., `ab1c`) that you can use to target specific tabs:

```bash
# Run commands on a specific tab
bb-browser snapshot --tab ab1c
bb-browser click 3 --tab ab1c
bb-browser eval "document.title" --tab ab1c
```

### Frame (iframe) Navigation

```bash
# Switch to an iframe by CSS selector
bb-browser frame "#my-iframe"
bb-browser frame "iframe[name='content']"

# Switch back to the main frame
bb-browser frame main
```

### Dialog Handling

```bash
# Auto-accept future dialogs (alert, confirm, prompt)
bb-browser dialog accept

# Auto-dismiss future dialogs
bb-browser dialog dismiss

# Accept with prompt text
bb-browser dialog accept "my input"
```

### Authenticated Fetch

Make HTTP requests using the browser's cookies and session:

```bash
# GET request using browser's auth context
bb-browser fetch https://api.example.com/me

# POST request
bb-browser fetch https://api.example.com/data --method POST
```

This is useful for accessing authenticated APIs without extracting cookies manually.

### Trace (Record User Actions)

```bash
# Start recording
bb-browser trace start

# Check status
bb-browser trace status

# Stop and get recorded events
bb-browser trace stop
```

### Site Adapters

Site adapters are JavaScript plugins that automate interactions with specific websites.

```bash
# List available adapters
bb-browser site list

# Search for adapters
bb-browser site search twitter

# Get adapter details
bb-browser site info twitter/search

# Run an adapter
bb-browser site run twitter/search "AI news"
# or shorthand:
bb-browser twitter/search "AI news"

# Pull community adapters
bb-browser site update
```

### Daemon Management

```bash
# Start daemon in foreground (for debugging)
bb-browser daemon

# With custom CDP port
bb-browser daemon --cdp-port 9222

# Check daemon status
bb-browser daemon status
# or
bb-browser status

# Stop the daemon
bb-browser daemon shutdown
```

### Diagnosing the stack

When something doesn't work it's not always obvious which layer is broken: stale `daemon.json`, dead daemon process, daemon up but not attached to CDP, or no tabs. `bb-browser doctor` walks the stack top-down and reports the first failing layer with a remediation hint.

```bash
bb-browser doctor          # human-readable report
bb-browser doctor --json   # structured {ok, checks[]} for scripts
```

Exit code is `1` on any fail; warnings (e.g. daemon not started yet) do not fail. The same diagnostic is exposed to AI agents as the `browser_doctor` MCP tool and to remote integrations as `GET /v1/doctor` (returns 503 on a failing check).

## Global Flags

| Flag | Description |
|------|-------------|
| `--tab <id>` | Target a specific tab by short ID or index |
| `--json` | Output results as JSON |
| `--jq <expr>` | Apply a jq-like filter to the output |
| `--since <seq\|last_action>` | Only return events after a sequence number or the last action |

### JSON Output

Every command supports `--json` for machine-readable output:

```bash
bb-browser snapshot --json
bb-browser tab --json
bb-browser network requests --json
```

### jq Filtering

Built-in jq-compatible expression filtering (no external `jq` binary needed):

```bash
# Get just the snapshot text
bb-browser snapshot --jq ".data.snapshotData.snapshot"

# Count tabs
bb-browser tab --json --jq ".data.tabs | length"

# Get all request URLs
bb-browser network requests --jq ".data.networkRequests[].url"

# Filter network requests by status
bb-browser network requests --jq '.data.networkRequests[] | select(.status > 400) | {url: .url, status: .status}'
```

### Incremental Queries

Use `--since` to only get events that occurred after a specific point:

```bash
# Get events since a sequence number
bb-browser network requests --since 42

# Get events since the last user action
bb-browser console --since last_action
bb-browser errors --since last_action
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BB_BROWSER_CDP_URL` | Override CDP endpoint (e.g., `http://127.0.0.1:9222`) |
| `BB_BROWSER_HOME` | Override config directory (default: `~/.bb-browser`) |

## Use Cases

### Web Scraping with Authentication

```bash
# Open the target site and log in manually (or use fill/click to automate)
bb-browser open https://app.example.com/login
bb-browser snapshot -i
bb-browser fill 0 "user@example.com"
bb-browser fill 1 "password123"
bb-browser click 2

# Now fetch authenticated API data
bb-browser fetch https://app.example.com/api/dashboard --json
```

### Automated Testing

```bash
# Navigate to the app
bb-browser open http://localhost:3000

# Fill in a form
bb-browser snapshot -i
bb-browser fill 0 "Test User"
bb-browser fill 1 "test@example.com"
bb-browser click 3

# Verify the result
bb-browser get text 5
bb-browser errors
```

### Monitoring & Debugging

```bash
# Watch network traffic
bb-browser open https://myapp.com
bb-browser network requests --filter "api" --json

# Check for JS errors
bb-browser errors

# Read console output
bb-browser console --filter "warning"
```

### AI Agent Integration

`bb-browser` is designed to work well with AI agents. The recommended approach is the built-in **MCP server** (see [MCP Server](#mcp-server) above), which lets AI assistants call browser tools directly without shell commands.

For agents that use shell-based tool calling, the CLI works just as well:

```bash
# The agent runs snapshot to "see" the page
bb-browser snapshot -i -c

# The agent decides which element to interact with based on the ref numbers
bb-browser click 4
bb-browser fill 7 "search query"

# The agent checks the result
bb-browser snapshot -i -c
```

## Typical Workflow

```bash
# 1. Open a page
bb-browser open https://news.ycombinator.com

# 2. See what's on the page
bb-browser snapshot -i

# 3. Interact with elements using ref numbers
bb-browser click 5

# 4. See the result
bb-browser snapshot -i

# 5. Extract data
bb-browser eval "document.querySelector('.title').textContent"

# 6. Get structured JSON output
bb-browser snapshot --json --jq ".data.snapshotData.refs"
```

## Development

Activate the repo's pre-commit hook once per clone to run `go vet`, the
race-enabled test suite, and the 70% coverage floor before every commit:

```bash
git config core.hooksPath .githooks
```

The hook skips when no `.go` files are staged. Bypass with `git commit
--no-verify` if needed.

### Local Chrome e2e tests

The browser e2e test starts `internal/e2e_verify_site` on localhost, starts a
local daemon, connects to a real Chromium-based browser through CDP, and drives
the CLI against that page. It is opt-in locally and explicitly skips in GitHub
Actions.

```bash
BB_BROWSER_E2E=1 go test -run TestE2ECLICommandsAgainstVerifySite -count=1 -v .
```

Set `BB_BROWSER_CDP_URL=http://host:port` to force a specific Chrome CDP
endpoint; otherwise the normal managed-browser discovery flow is used.

## License

MIT
