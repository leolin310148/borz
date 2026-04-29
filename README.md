# borz

**Your browser is the API.** A CLI tool that lets you control and observe any Chromium-based browser from the terminal via the Chrome DevTools Protocol (CDP).

`borz` is a Go port of the original [bb-browser](https://github.com/nicepkg/bb-browser) (Node.js). It ships as a single static binary with zero runtime dependencies.

## Installation

### One-line install (macOS/Linux)

Install the latest release binary to `/usr/local/bin/borz`:

```bash
curl -fsSL https://github.com/leolin310148/borz/raw/main/scripts/install.sh | bash
```

Install somewhere else, or pin a release:

```bash
curl -fsSL https://github.com/leolin310148/borz/raw/main/scripts/install.sh | BORZ_INSTALL_DIR="$HOME/.local/bin" bash
curl -fsSL https://github.com/leolin310148/borz/raw/main/scripts/install.sh | BORZ_VERSION=v0.1.0 bash
```

The installer detects `linux/darwin` + `amd64/arm64`, downloads the matching GitHub Release asset, verifies `checksums.txt`, and installs the executable as `borz`.

### Windows PowerShell install

Run PowerShell, then install the latest `borz.exe` into `%LOCALAPPDATA%\Programs\borz\bin`:

```powershell
irm https://github.com/leolin310148/borz/raw/main/scripts/install.ps1 | iex
```

To pin a version or choose another directory:

```powershell
irm https://github.com/leolin310148/borz/raw/main/scripts/install.ps1 -OutFile install.ps1
.\install.ps1 -Version v0.1.0 -InstallDir "$env:USERPROFILE\bin"
```

The PowerShell installer verifies the release checksum and adds the install directory to the user `PATH` unless `-NoPath` is passed.

### Rename transition

The primary binary is now `borz`. Release artifacts also include a temporary
`bb-browser-*` compatibility wrapper that prints a deprecation notice and
forwards to `borz`; update scripts and aliases to call `borz` directly. On first
write, an existing `~/.bb-browser` config directory is migrated to `~/.borz`
unless `~/.borz` already exists.

### Manual download

Grab the latest release for your platform from [GitHub Releases](https://github.com/leolin310148/borz/releases):

```bash
# macOS (Apple Silicon)
curl -LO https://github.com/leolin310148/borz/releases/latest/download/borz-darwin-arm64
chmod +x borz-darwin-arm64
sudo mv borz-darwin-arm64 /usr/local/bin/borz

# macOS (Intel)
curl -LO https://github.com/leolin310148/borz/releases/latest/download/borz-darwin-amd64
chmod +x borz-darwin-amd64
sudo mv borz-darwin-amd64 /usr/local/bin/borz

# Linux (x86_64)
curl -LO https://github.com/leolin310148/borz/releases/latest/download/borz-linux-amd64
chmod +x borz-linux-amd64
sudo mv borz-linux-amd64 /usr/local/bin/borz

# Linux (ARM64)
curl -LO https://github.com/leolin310148/borz/releases/latest/download/borz-linux-arm64
chmod +x borz-linux-arm64
sudo mv borz-linux-arm64 /usr/local/bin/borz
```

Windows manual install:

```powershell
Invoke-WebRequest -UseBasicParsing https://github.com/leolin310148/borz/releases/latest/download/borz-windows-amd64.exe -OutFile borz.exe
.\borz.exe version
```

### Build from source

```bash
go install github.com/leolin310148/borz@latest
```

Or clone and build:

```bash
git clone https://github.com/leolin310148/borz.git
cd borz
go build -o borz .
```

## Prerequisites

You need a Chromium-based browser (Google Chrome, Microsoft Edge, Brave, Arc, etc.) installed on your machine.

`borz` connects to the browser using CDP. It will automatically:

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
export BORZ_CDP_URL=http://127.0.0.1:19825
```

## Browser Extension (optional)

A Chrome extension extends `borz` with browser-level capabilities CDP cannot provide on its own — cross-domain cookies, bookmarks, browsing history, downloads, windows, tab groups, raw extension RPC, and browser event streams. Install it once per Chrome profile:

```bash
# Download the extension that matches your installed borz binary
borz extension download
```

This fetches `borz-extension.zip` from the latest GitHub release, verifies its SHA-256, and extracts it into `~/.borz/extension/` (the previous install is removed). The command then prints the directory and the load steps:

1. Open `chrome://extensions`
2. Enable **Developer mode** (top-right toggle)
3. Click **Load unpacked** and pick `~/.borz/extension/`

Re-run `borz extension update` after upgrading the binary to keep the extension in lockstep. The extension version mirrors the borz release tag.

Commands that *don't* require the extension keep working without it. Extension-backed commands tell you when it is missing:

```bash
borz extension status             # connected extension capabilities
borz extension call <method> '{}' # raw extension RPC escape hatch
borz cookies all [domain]          # all-domain cookie store
borz bookmarks tree|search|create|update|remove
borz browser-history search        # Chrome browsing history
borz downloads list|search|start|erase|cancel|pause|resume
borz window list|new|focus|close   # browser windows (alias: windows)
borz tab events --tail             # tabs/windows/bookmarks/history/download event stream
```

## How It Works

```
┌──────────┐       HTTP        ┌──────────┐     WebSocket/CDP     ┌──────────┐
│ borz CLI │  ───────────────> │  daemon   │  ──────────────────>  │  Chrome  │
│ (client) │  <─────────────── │ (server)  │  <──────────────────  │ (browser)│
└──────────┘    JSON response  └──────────┘     DevTools Protocol  └──────────┘
```

When you run any command, `borz`:

1. **Starts a daemon** (if not already running) that holds a persistent CDP WebSocket connection to Chrome
2. **Sends the command** as an HTTP request to the daemon
3. **The daemon translates** the command into CDP protocol calls and returns the result

The daemon runs in the background and auto-discovers your browser. You don't need to manage it manually.

## MCP Server

`borz` includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server, letting AI assistants like Claude control your browser directly.

### Setup

Start the MCP server:

```bash
borz mcp
```

This runs an MCP server over stdio. To use it with an MCP client, add it to your configuration:

Add to your MCP client configuration (e.g. `.claude/settings.json` for Claude Code):

```json
{
  "mcpServers": {
    "borz": {
      "command": "borz",
      "args": ["mcp"]
    }
  }
}
```

### Available Tools

The MCP server exposes 36 tools:

| Category | Tools |
|----------|-------|
| **Navigation** | `browser_navigate`, `browser_back`, `browser_forward`, `browser_refresh`, `browser_close` |
| **Interaction** | `browser_click`, `browser_hover`, `browser_fill`, `browser_type`, `browser_check`, `browser_uncheck`, `browser_select`, `browser_press`, `browser_scroll` |
| **Observation** | `browser_snapshot`, `browser_screenshot`, `browser_get`, `browser_eval`, `browser_wait` |
| **Tab Management** | `browser_tab_list`, `browser_tab_new`, `browser_tab_select`, `browser_tab_close` |
| **Diagnostics** | `browser_network`, `browser_console`, `browser_errors`, `browser_doctor` |
| **Extension-backed** | `browser_extension_status`, `browser_extension_call`, `browser_bookmarks`, `browser_history`, `browser_downloads`, `browser_windows` |
| **Site Adapters** | `browser_site_list`, `browser_site_info`, `browser_site_run` |

The workflow mirrors the CLI: call `browser_snapshot` to see the page structure with element refs, then use those refs with interaction tools like `browser_click` or `browser_fill`. Screenshots are returned as inline base64 PNG images.

Most action tools accept optional `waitFor` (CSS selector) and `timeout` (ms, default 10000) params — after the action runs, the daemon polls `document.querySelector(waitFor)` until it returns a non-null node or the timeout elapses. Use this for SPA loads or modals instead of fixed `browser_wait` calls.

Other notable params:

- `browser_snapshot` accepts `textOnly: true` for a reader-mode plain-text dump (no element refs) — useful for summarization or feeding the page to an LLM as context.
- `browser_eval` auto-wraps top-level `await` in an async IIFE so `await fetch(...)` works without manual boilerplate. Pass `noAutoAwait: true` to opt out.
- `browser_doctor` runs end-to-end stack diagnostics (binary → daemon → CDP → tabs) and returns the first failing layer with a remediation hint. Pass `json: true` for structured output.

## Server Mode

`borz server` exposes the daemon as a remote-accessible HTTP API with ergonomic `/v1/*` REST routes. It's designed for integrations like n8n, Make, or any workflow tool that can send HTTP requests.

### Start the server

```bash
# Local-only (no auth required)
borz server --host 127.0.0.1 --port 19824

# Remote-accessible (token required)
borz server --host 0.0.0.0 --port 19824 --token "$(openssl rand -hex 16)"

# Or via env
export BORZ_TOKEN=mysecret
borz server --host 0.0.0.0
```

The server refuses to bind a non-loopback address without a token. Clients authenticate with `Authorization: Bearer <token>`.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--host` | `BORZ_SERVER_HOST` | `0.0.0.0` | Bind address |
| `--port` | `BORZ_SERVER_PORT` | `19824` | Bind port |
| `--token` | `BORZ_TOKEN` | *(none)* | Required for non-loopback host |
| `--cdp-host` | — | `127.0.0.1` | Chrome CDP host |
| `--cdp-port` | — | `19825` | Chrome CDP port |

### Windows service

On Windows, install `borz server` as a native Windows Service from an elevated PowerShell or Command Prompt:

```powershell
# Install latest borz.exe, register the service, and start it
irm https://github.com/leolin310148/borz/raw/main/scripts/install.ps1 -OutFile install.ps1
.\install.ps1 -Service -StartService

# Or, after borz.exe is already installed
borz service install
borz service start
borz service status
```

By default the service listens only on `127.0.0.1:19824`. To expose it remotely, bind a non-loopback host and provide a bearer token:

```powershell
borz service install --host 0.0.0.0 --port 19824 --token "$env:BORZ_TOKEN"
borz service start
```

The service command supports:

```powershell
borz service install [--name borz] [--host 127.0.0.1] [--port 19824] [--token TOKEN]
borz service start|stop|status [--name borz]
borz service uninstall [--name borz]
```

Service installation writes the server flags into the Windows Service Control Manager registration. If you pass `--token`, administrators on the machine can inspect it in the service configuration; rotate the token if you later change exposure.

Stop the server:

```bash
borz server shutdown
```

### Use a remote server from the CLI

Configure the CLI once, then pass `--remote` for commands that should use the
configured server:

```bash
borz client setup http://server-host:19824 --token "$BORZ_TOKEN"
borz --remote open https://example.com
```

When a remote CLI command writes a screenshot path, the file is saved on the
client machine running the CLI, not on the remote server:

```bash
borz --remote screenshot ./out.png
```

Without `--remote`, browser actions such as `open`, `snapshot`, `click`,
`eval`, `tab`, `network`, and `cookies` always use the local daemon/CDP
connection:

```bash
borz open https://example.com
```

To make only the current shell default to remote while other shells stay local,
define an alias in that shell:

```bash
alias borz='borz --remote'
```

The client config is stored at `~/.borz/client.json` with 0600
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
| GET | `/v1/ext/capabilities` | — connected extension capabilities |
| POST | `/v1/ext/call` | `{method, params?}` — raw extension RPC |
| GET | `/v1/cookies/all` | query: `domain?`, `name?`, `url?` — all-domain cookies |
| GET \| POST | `/v1/bookmarks/*` | bookmark tree/search/create/update/remove |
| GET \| POST | `/v1/browser-history/*` | Chrome history search/delete |
| GET \| POST | `/v1/downloads/*` | downloads search/start/erase/cancel/pause/resume/show |
| GET \| POST | `/v1/windows*` | browser window list/create/update/close |
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
- **URL:** `http://borz-host:19824/v1/snapshot`
- **Authentication:** Header Auth → `Authorization: Bearer <token>`
- **Body:** JSON → `{ "interactive": true, "compact": true }`

Chain nodes to open → snapshot → click → extract. A dedicated n8n community node is on the roadmap.

## Quick Start

```bash
# Open a webpage
borz open https://example.com

# Take a snapshot of the page (accessibility tree with element references)
borz snapshot

# Click an element by its ref number from the snapshot
borz click 5

# Fill a text input
borz fill 3 "hello world"

# Get the page title
borz get title

# Take a screenshot
borz screenshot

# Execute JavaScript in the page
borz eval "document.title"

# Get JSON output for scripting
borz snapshot --json

# Filter with jq expressions
borz snapshot --jq ".snapshotData.refs | keys | length"
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
borz open https://github.com

# Force a fresh tab even if one already has this URL
borz open https://github.com --new

# Navigate a specific existing tab by ID
borz open https://github.com --tab ab1c

# Navigate back
borz back

# Wait for a selector before returning (default 10s, override with --timeout <ms>)
borz open https://app.example.com --wait-for ".dashboard-loaded"
```

`--wait-for <selector>` and `--timeout <ms>` work on **every action that changes the page** — `open`, `click`, `hover`, `fill`, `type`, `check`, `uncheck`, `select`, `press`, `scroll`, `eval`, `back`, `forward`, `refresh`. The daemon runs the action, then polls `document.querySelector(...)` on a 100 ms tick until the node appears or the timeout elapses. Prefer this over `wait <ms>` for any DOM change.

### Observation

These commands let you **see** what's on the page.

#### `snapshot` - Get the accessibility tree

The most important command. It returns a structured text representation of the page with **ref numbers** you can use to interact with elements.

```bash
# Full accessibility tree
borz snapshot

# Interactive elements only (buttons, links, inputs, etc.)
borz snapshot -i

# Compact output (shorter names, no tag names)
borz snapshot -c

# Limit tree depth
borz snapshot -d 3

# Filter by selector/keyword
borz snapshot -s "search"

# Combine flags
borz snapshot -i -c

# Reader-mode plain text (title + URL + visible text only) — no element refs.
# Good for "summarize this page" or feeding the page to an LLM as context.
borz snapshot --text-only
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
borz screenshot

# Save to file (use with --json and jq)
borz screenshot --json --jq ".data.dataUrl"
```

#### `get` - Get element or page attributes

```bash
# Get the current page URL
borz get url

# Get the page title
borz get title

# Get the text content of an element
borz get text 5

# Get an HTML attribute of an element
borz get href 2
borz get class 4
borz get value 3
```

#### `network` - Monitor network requests

```bash
# List all captured network requests
borz network

# Filter by URL pattern
borz network requests --filter "api"

# Filter by HTTP method
borz network requests --method POST

# Filter by status code
borz network requests --status 404
borz network requests --status 5xx

# Include response bodies
borz network requests --with-body

# Show only requests since last action
borz network requests --since last_action

# Live stream new requests as they arrive (Ctrl+C to stop).
# Pairs with --filter / --method / --status and --json (JSONL output).
borz network requests --tail
borz network requests --tail --filter /api/ --method POST

# Clear captured requests
borz network clear
```

#### `console` - Read console messages

```bash
# Get all console messages
borz console

# Filter messages
borz console --filter "error"

# Only messages since last action
borz console --since last_action

# Clear console buffer
borz console --clear
```

#### `errors` - Read JavaScript errors

```bash
# Get all JS errors
borz errors

# Filter errors
borz errors --filter "TypeError"

# Clear error buffer
borz errors --clear
```

### Interaction

All interaction commands use **ref numbers** from the `snapshot` output.

#### `click` / `hover`

```bash
# Click a button (ref=4 from snapshot)
borz click 4

# Hover over an element
borz hover 2
```

#### `fill` / `type`

```bash
# Clear the input and fill with new text
borz fill 3 "hello world"

# Append text to current value (like typing)
borz type 3 " more text"
```

#### `check` / `uncheck`

```bash
# Check a checkbox
borz check 7

# Uncheck it
borz uncheck 7
```

#### `select`

```bash
# Select a dropdown option by value
borz select 6 "option2"
```

#### `press`

```bash
# Press a key
borz press Enter
borz press Tab
borz press ArrowDown
borz press Escape
```

#### `scroll`

```bash
# Scroll down (default 300px)
borz scroll down

# Scroll up 500px
borz scroll up 500

# Scroll left/right
borz scroll left 200
borz scroll right 200
```

#### `eval` - Execute JavaScript

```bash
# Run arbitrary JavaScript in the page context
borz eval "document.title"
borz eval "document.querySelectorAll('a').length"
borz eval "window.location.href"

# Multi-word scripts
borz eval "document.querySelector('h1').textContent"

# Top-level await is auto-wrapped in an async IIFE — no manual boilerplate.
borz eval "await fetch('/api/data').then(r => r.json())"

# Disable auto-wrapping if you need the raw script.
borz eval --no-auto-await "(async () => { ... })()"

# Read a script from disk so long extraction snippets aren't trapped in a shell quote.
borz eval --file ./extract.js

# Inject CLI-supplied JSON values as top-level consts the script can read.
borz eval --file ./greet.js --json-arg user='{"id":7}' --json-arg n=3

# Print the result raw — strings unquoted, other shapes JSON-formatted.
# Removes the need for ' | jq .data.result' on every call.
borz eval --unwrap "document.title"
```

#### `wait`

```bash
# Wait 1 second (default)
borz wait

# Wait 2 seconds
borz wait 2000
```

### Tab Management

```bash
# List all open tabs
borz tab

# Open a new tab
borz tab new
borz tab new https://google.com

# Switch to tab by index
borz tab 0
borz tab 2

# Switch to tab by short ID
borz tab select ab1c

# Close a tab
borz tab close 2
borz tab close --id ab1c
```

Every response includes a short `tab` ID (e.g., `ab1c`) that you can use to target specific tabs:

```bash
# Run commands on a specific tab
borz snapshot --tab ab1c
borz click 3 --tab ab1c
borz eval "document.title" --tab ab1c
```

### Frame (iframe) Navigation

```bash
# Switch to an iframe by CSS selector
borz frame "#my-iframe"
borz frame "iframe[name='content']"

# Switch back to the main frame
borz frame main
```

### Dialog Handling

```bash
# Auto-accept future dialogs (alert, confirm, prompt)
borz dialog accept

# Auto-dismiss future dialogs
borz dialog dismiss

# Accept with prompt text
borz dialog accept "my input"
```

### Authenticated Fetch

Make HTTP requests using the browser's cookies and session:

```bash
# GET request using browser's auth context
borz fetch https://api.example.com/me

# POST request
borz fetch https://api.example.com/data --method POST
```

This is useful for accessing authenticated APIs without extracting cookies manually.

### Trace (Record User Actions)

```bash
# Start recording
borz trace start

# Check status
borz trace status

# Stop and get recorded events
borz trace stop
```

### Site Adapters

Site adapters are JavaScript plugins that automate interactions with specific websites.

```bash
# List available adapters
borz site list

# Search for adapters
borz site search twitter

# Get adapter details
borz site info twitter/search

# Run an adapter
borz site run twitter/search "AI news"
# or shorthand:
borz twitter/search "AI news"

# Pull community adapters
borz site update
```

### Daemon Management

```bash
# Start daemon in foreground (for debugging)
borz daemon

# With custom CDP port
borz daemon --cdp-port 9222

# Check daemon status
borz daemon status
# or
borz status

# Stop the daemon
borz daemon shutdown
```

### Diagnosing the stack

When something doesn't work it's not always obvious which layer is broken: stale `daemon.json`, dead daemon process, daemon up but not attached to CDP, or no tabs. `borz doctor` walks the stack top-down and reports the first failing layer with a remediation hint.

```bash
borz doctor          # human-readable report
borz doctor --json   # structured {ok, checks[]} for scripts
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
borz snapshot --json
borz tab --json
borz network requests --json
```

### jq Filtering

Built-in jq-compatible expression filtering (no external `jq` binary needed):

```bash
# Get just the snapshot text
borz snapshot --jq ".data.snapshotData.snapshot"

# Count tabs
borz tab --json --jq ".data.tabs | length"

# Get all request URLs
borz network requests --jq ".data.networkRequests[].url"

# Filter network requests by status
borz network requests --jq '.data.networkRequests[] | select(.status > 400) | {url: .url, status: .status}'
```

### Incremental Queries

Use `--since` to only get events that occurred after a specific point:

```bash
# Get events since a sequence number
borz network requests --since 42

# Get events since the last user action
borz console --since last_action
borz errors --since last_action
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `BORZ_CDP_URL` | Override CDP endpoint (e.g., `http://127.0.0.1:9222`) |
| `BORZ_HOME` | Override config directory (default: `~/.borz`) |

Legacy `BB_BROWSER_*` environment variables are still accepted during the rename transition.

## Use Cases

### Web Scraping with Authentication

```bash
# Open the target site and log in manually (or use fill/click to automate)
borz open https://app.example.com/login
borz snapshot -i
borz fill 0 "user@example.com"
borz fill 1 "password123"
borz click 2

# Now fetch authenticated API data
borz fetch https://app.example.com/api/dashboard --json
```

### Automated Testing

```bash
# Navigate to the app
borz open http://localhost:3000

# Fill in a form
borz snapshot -i
borz fill 0 "Test User"
borz fill 1 "test@example.com"
borz click 3

# Verify the result
borz get text 5
borz errors
```

### Monitoring & Debugging

```bash
# Watch network traffic
borz open https://myapp.com
borz network requests --filter "api" --json

# Check for JS errors
borz errors

# Read console output
borz console --filter "warning"
```

### AI Agent Integration

`borz` is designed to work well with AI agents. The recommended approach is the built-in **MCP server** (see [MCP Server](#mcp-server) above), which lets AI assistants call browser tools directly without shell commands.

For agents that use shell-based tool calling, the CLI works just as well:

```bash
# The agent runs snapshot to "see" the page
borz snapshot -i -c

# The agent decides which element to interact with based on the ref numbers
borz click 4
borz fill 7 "search query"

# The agent checks the result
borz snapshot -i -c
```

## Typical Workflow

```bash
# 1. Open a page
borz open https://news.ycombinator.com

# 2. See what's on the page
borz snapshot -i

# 3. Interact with elements using ref numbers
borz click 5

# 4. See the result
borz snapshot -i

# 5. Extract data
borz eval "document.querySelector('.title').textContent"

# 6. Get structured JSON output
borz snapshot --json --jq ".data.snapshotData.refs"
```

## Development

Activate the repo's pre-commit hook once per clone to run `go vet`, the
race-enabled test suite, and the 80% coverage floor before every commit:

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
BORZ_E2E=1 go test -run TestE2ECLICommandsAgainstVerifySite -count=1 -v .
```

Set `BORZ_CDP_URL=http://host:port` to force a specific Chrome CDP
endpoint; otherwise the normal managed-browser discovery flow is used.

## License

MIT
