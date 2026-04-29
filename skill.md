---
name: bb-browser
description: Drive the user's real Chrome session (cookies, logins, JS state) to inspect or automate web pages. Use when the user asks to open, click, fill, scrape, screenshot, or monitor a live website — especially anything that needs authentication, JavaScript rendering, or multi-step interaction. Prefer over generic fetch/web tools when a real browser is needed.
---

# bb-browser

A Go CLI + MCP + HTTP server that controls any Chromium browser over CDP. Three front-ends share one daemon and one live Chrome session.

## When to use

- Page needs login or session cookies (Gmail, GitHub, internal dashboards)
- SPA or JS-rendered content (plain HTTP fetch returns empty HTML)
- Multi-step flows (fill form → click → read result)
- Inspecting live-tab state: network traffic, console, JS errors, current URL
- Running site-specific adapters (e.g. `twitter/search`)

## When NOT to use

- Plain-HTML page with no auth — a simple `fetch` is lighter
- Programmatic API calls where the user has a token — call the API directly
- Anything the user wants to automate without a real browser on the host machine

## How to invoke

Three equivalent front-ends — pick based on the runtime:

### 1. MCP (preferred for AI agents)

If `bb-browser` is configured as an MCP server, call tools directly. Workflow:

1. `browser_tab_list` — the user may already be on the page, logged in, mid-flow. Reusing a tab preserves scroll/form state.
2. `browser_navigate {url}` — reuses a tab with the exact same URL unless you pass `new: true`.
3. `browser_snapshot {interactive: true, compact: true}` — returns an accessibility tree with `[ref=N]` handles.
4. Act with refs: `browser_click {ref: "5"}`, `browser_fill {ref: "3", text: "..."}`, `browser_press {key: "Enter"}`.
5. Snapshot again to verify, or read `browser_get {attribute: "text", ref: "..."}`, `browser_network`, `browser_console`, `browser_errors`.

All tools accept an optional `tab` param (short id from `browser_tab_list`) to target a specific tab.

Tool categories (36 total): navigation, interaction, observation (includes `browser_eval` for arbitrary JS), tab management, diagnostics, extension-backed Chrome APIs (`browser_extension_status`, `browser_extension_call`, `browser_bookmarks`, `browser_history`, `browser_downloads`, `browser_windows`), site adapters (`browser_site_list`/`_info`/`_run`).

### 2. Shell / CLI

```bash
bb-browser open <url>                            # reuses tab with same URL; --new forces fresh
bb-browser open <url> --wait-for '<selector>'    # block until selector exists (default 10s)
bb-browser click <ref> --wait-for '.modal'       # --wait-for works on most actions, not just open
bb-browser snapshot -i -c                        # -i: interactive only, -c: compact
bb-browser snapshot --text-only                  # reader-mode plain text (no refs); good for LLM context
bb-browser click <ref>
bb-browser fill <ref> <text>
bb-browser press <key>
bb-browser eval "<js>"                           # JS in page context → JSON
bb-browser eval --unwrap "document.title"        # print result raw (strings unquoted)
bb-browser eval --file ./extract.js              # read script from file
bb-browser eval --file ./greet.js --json-arg user='{"id":7}' --json-arg n=3   # inject JSON args as top-level consts
bb-browser eval "await fetch('/api/me').then(r=>r.json())"  # top-level await auto-wraps
bb-browser get <url|title|text|href|value> [ref]
bb-browser screenshot                            # base64 PNG
bb-browser network requests --since last_action
bb-browser network requests --tail --filter /api/    # live stream until Ctrl+C
bb-browser console --filter error
bb-browser fetch <url>                           # authenticated HTTP via page session
bb-browser tab                                   # list tabs
bb-browser extension status                      # extension connection + capabilities
bb-browser bookmarks search github               # Chrome bookmarks (extension)
bb-browser browser-history search github --limit 20
bb-browser downloads list --limit 20
bb-browser window list                           # Chrome windows (extension)
bb-browser <platform>/<adapter> [args]           # run a site adapter
```

Global flags: `--tab <id>`, `--json`, `--jq <expr>`, `--unwrap` (eval/site only), `--since <seq|last_action>`.

Per-command help: `bb-browser <cmd> --help` or `bb-browser help <cmd>`.

### 3. HTTP / REST (for n8n, Make, external services)

Server-mode exposes `/v1/*` JSON endpoints. Start it with:

```bash
bb-browser server --host 0.0.0.0 --token "$TOKEN"   # token required for non-loopback
```

Requests: `POST /v1/{snapshot,open,click,fill,...}` with JSON body. Auth header: `Authorization: Bearer <token>`. Responses: `{id, success, data?, error?}`.

Site adapters over HTTP: `GET /v1/sites`, `POST /v1/sites/info {name}`, `POST /v1/sites/run {name, args, tab?}`.

## Golden rules

1. **Always snapshot before interacting.** Refs are regenerated per snapshot — don't reuse stale ones across navigations.
2. **`open`/`browser_navigate` reuses same-URL tabs by default.** This is intentional to avoid tab blowup. Pass `new: true` to force a fresh tab when the user clearly wants one.
3. **Prefer compact interactive snapshots (`-i -c` or `{interactive: true, compact: true}`)** when you only need clickable/fillable elements — much shorter and cheaper.
4. **`browser_eval` is the escape hatch** for anything the structured tools can't express — custom DOM queries, reading `localStorage`, calling page APIs with the user's session.
5. **Use `--since last_action`** on network/console/errors to get only events since your last interaction. Avoids re-reading the full ring buffer.
6. **For page visuals**, use `browser_screenshot` — it shows the rendered UI (post-JS, post-CSS, with the user's logged-in state) that fetched HTML can't.
7. **Diagnose failures with `browser_console` + `browser_errors`** before assuming the automation is broken. Pages often log hints.
8. **Prefer `--wait-for '<selector>'` over `wait <ms>`** for any DOM change. Works on `open`, `click`, `fill`, `press`, `eval`, etc. — the action runs, then the daemon polls `document.querySelector(...)` until non-null or timeout (default 10s, override with `--timeout <ms>`).
9. **Use `eval --unwrap` to strip `{success, data, result, ...}` envelopes** when you only want the value — strings are emitted unquoted, other shapes as JSON. Combine with `--file <path>` for non-trivial scripts.
10. **Use extension-backed tools for browser-level state CDP cannot see**: all-domain cookies, bookmarks, browsing history, downloads, windows, tab groups, and browser events. Check `browser_extension_status` / `bb-browser extension status` first if one of these reports that no extension is connected.

## Site adapters

Site adapters are JS plugins that automate specific sites (e.g. twitter/search). They run on the server/daemon's filesystem (`~/.bb-browser/sites` for local, `~/.bb-browser/bb-sites` for community). Discover with `browser_site_list` or `bb-browser site list`; inspect with `browser_site_info`. Run with `browser_site_run {name, args}` or CLI shorthand `bb-browser <name> <args>`.

Pull community adapters: `bb-browser site update` (CLI only — triggers a git pull, intentionally not exposed over HTTP/MCP).

## Troubleshooting

- "Chrome not connected" → the daemon is up but CDP is down. Start Chrome, or let the daemon auto-launch: check `bb-browser status`.
- "a daemon may already be running" → `bb-browser daemon status`, `bb-browser daemon shutdown` if stale.
- When unsure where the stack is broken, run `bb-browser doctor` — it walks through home dir, daemon JSON, daemon process, daemon HTTP, CDP attach, tabs, and direct CDP discovery, and reports the first failing layer.
- Element ref not found → page changed between snapshot and action. Re-snapshot.
- Remote `server` refuses to start → non-loopback bind without `--token`. Set `BB_BROWSER_TOKEN` or pass `--token`.

## Further reading

- `llm.txt` — compressed spec of CLI, MCP, and REST surfaces
- `README.md` — human-oriented docs with examples
- Source: https://github.com/leolin310148/bb-browser-go
