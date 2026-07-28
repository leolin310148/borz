---
name: borz
description: Drive the user's real Chrome session (cookies, logins, JS state) to inspect or automate web pages. Use when the user asks to open, click, fill, scrape, screenshot, or monitor a live website — especially anything that needs authentication, JavaScript rendering, or multi-step interaction. Prefer over generic fetch/web tools when a real browser is needed.
---

# borz

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

If `borz` is configured as an MCP server, call tools directly. Workflow:

1. `browser_tab_list` — the user may already be on the page, logged in, mid-flow. Reusing a tab preserves scroll/form state.
2. `browser_navigate {url}` — reuses a tab with the exact same URL unless you pass `new: true`.
3. For RWD/mobile work, call `browser_viewport {preset: "mobile"}` before inspecting.
4. `browser_snapshot {interactive: true, compact: true}` — returns an accessibility tree with `[ref=N]` handles.
5. Act with refs: `browser_click {ref: "5"}`, `browser_fill {ref: "3", text: "..."}`, `browser_press {key: "Enter"}`.
6. Snapshot again to verify, or read `browser_get {attribute: "text", ref: "..."}`, `browser_network`, `browser_console`, `browser_errors`.

All tools accept an optional `tab` param (short id from `browser_tab_list`) to target a specific tab.

Tool categories (44 total): navigation, interaction, observation (includes `browser_viewport` for responsive layouts and `browser_eval` for arbitrary JS), browser testing (`browser_webauthn` for typed Passkey virtual authenticators), tab management, diagnostics, extension-backed Chrome APIs (`browser_extension_status`, `browser_extension_call`, `browser_bookmarks`, `browser_history`, `browser_downloads`, `browser_windows`), site adapters (`browser_site_list`/`_info`/`_run`).

### 2. Shell / CLI

```bash
borz open <url>                            # reuses tab with same URL; --new forces fresh
borz open <url> --viewport mobile          # open/apply a mobile viewport for RWD checks
borz viewport [mobile|tablet|desktop|reset]
borz open <url> --wait-for '<selector>'    # block until selector exists (default 10s)
borz click <ref> --wait-for '.modal'       # --wait-for works on most actions, not just open
borz snapshot -i -c                        # -i: interactive only, -c: compact
borz snapshot --text-only                  # reader-mode plain text (no refs); good for LLM context
borz click <ref>
borz fill <ref> <text>
borz press <key>
borz eval "<js>"                           # JS in page context → JSON
borz eval --unwrap "document.title"        # print result raw (strings unquoted)
borz eval --file ./extract.js              # read script from file
borz eval --file ./greet.js --json-arg user='{"id":7}' --json-arg n=3   # inject JSON args as top-level consts
borz eval "await fetch('/api/me').then(r=>r.json())"  # top-level await auto-wraps
borz get <url|title|text|href|value> [ref]
borz screenshot                            # base64 PNG
borz network requests --since last_action
borz network requests --tail --filter /api/    # live stream until Ctrl+C
borz console --filter error
borz fetch <url>                           # authenticated HTTP via page session
borz tab                                   # list tabs
borz extension status                      # extension connection + capabilities
borz bookmarks search github               # Chrome bookmarks (extension)
borz browser-history search github --limit 20
borz downloads list --limit 20
borz window list                           # Chrome windows (extension)
borz webauthn enable --tab <id>            # enable real Chrome WebAuthn test domain
borz webauthn add --tab <id> --json        # Passkey-ready CTAP2/internal authenticator
borz webauthn credentials <auth-id> --tab <id> --json
borz <platform>/<adapter> [args]           # run a site adapter
```

Global flags: `--tab <id>`, `--json`, `--jq <expr>`, `--unwrap` (eval/site only), `--since <seq|last_action>`, `--profile <name>`.

Per-command help: `borz <cmd> --help` or `borz help <cmd>`.

### Picking a profile

`--profile <name>` (or `BORZ_PROFILE`) is the single handle for **which browser am I driving**: a local managed Chrome, an existing CDP endpoint, or a remote `borz server` on another machine. No flag means the `default` profile.

```bash
borz profile list          # name, transport, target, DESCRIPTION — what each one is for
borz profile show <name>   # one profile's details (tokens redacted)
```

Never infer a profile from its name. Run `borz profile list` and read the description when the target browser isn't obvious, and ask the user when it stays ambiguous — the wrong profile means acting inside the wrong logged-in session. Descriptions are set with `borz profile set <name> --description "..."`.

### 3. HTTP / REST (for n8n, Make, external services)

Server-mode exposes `/v1/*` JSON endpoints. Start it with:

```bash
borz server --host 0.0.0.0 --token "$TOKEN"   # token required for non-loopback
```

Requests: `POST /v1/{snapshot,open,click,fill,...}` with JSON body. Auth header: `Authorization: Bearer <token>`. Responses: `{id, success, data?, error?}`.

Site adapters over HTTP: `GET /v1/sites`, `POST /v1/sites/info {name}`, `POST /v1/sites/run {name, args, tab?}`.

## Golden rules

1. **Always snapshot before interacting.** Refs are regenerated per snapshot — don't reuse stale ones across navigations.
2. **`open`/`browser_navigate` reuses same-URL tabs by default.** This is intentional to avoid tab blowup. Pass `new: true` to force a fresh tab when the user clearly wants one.
3. **Prefer compact interactive snapshots (`-i -c` or `{interactive: true, compact: true}`)** when you only need clickable/fillable elements — much shorter and cheaper.
4. **`browser_eval` is the escape hatch** for anything the structured tools can't express — custom DOM queries, reading `localStorage`, calling page APIs with the user's session.
5. **Use `--since last_action`** on network/console/errors to get only events since your last interaction. Avoids re-reading the full ring buffer.
6. **For RWD/mobile checks**, set viewport before observing: `browser_viewport {preset: "mobile"}` or `borz viewport mobile`. Re-snapshot afterward because refs can go stale after layout changes.
7. **For page visuals**, use `browser_screenshot` — it shows the rendered UI (post-JS, post-CSS, with the user's logged-in state) that fetched HTML can't.
8. **Diagnose failures with `browser_console` + `browser_errors`** before assuming the automation is broken. Pages often log hints.
9. **Prefer `--wait-for '<selector>'` over `wait <ms>`** for any DOM change. Works on `open`, `click`, `fill`, `press`, `eval`, etc. — the action runs, then the daemon polls `document.querySelector(...)` until non-null or timeout (default 10s, override with `--timeout <ms>`). When no selector is available (animations, backend polling, "give it a moment"), reach for the **global `--pre-delay <ms>` / `--post-delay <ms>` flags** (`preDelay`/`postDelay` on MCP, `preDelayMs`/`postDelayMs` on REST) to absorb the `sleep N && borz ...` shell pattern into one daemon call. Both are capped by the 30s daemon command timeout.
10. **Use `eval --unwrap` to strip `{success, data, result, ...}` envelopes** when you only want the value — strings are emitted unquoted, other shapes as JSON. Combine with `--file <path>` for non-trivial scripts.
11. **Use extension-backed tools for browser-level state CDP cannot see**: all-domain cookies, bookmarks, browsing history, downloads, windows, tab groups, and browser events. Check `browser_extension_status` / `borz extension status` first if one of these reports that no extension is connected.
12. **Use the typed WebAuthn lifecycle for Passkey E2E**: target one tab, `enable`, `add`, run the real registration/login UI, inspect with `credentials`, control negative/success paths with `set-user-verified` and `set-automatic-presence`, then `remove` and `disable`. `add` defaults to CTAP2/internal with resident key, UV, verified user, and automatic presence enabled. Keep `data.result.authenticatorId`; virtual state is scoped to that tab's CDP session.

## Site adapters

Site adapters are JS plugins that automate specific sites (e.g. twitter/search). They run on the server/daemon's filesystem (`~/.borz/sites` for local, `~/.borz/bb-sites` for community). Discover with `browser_site_list` or `borz site list`; inspect with `browser_site_info`. Run with `browser_site_run {name, args}` or CLI shorthand `borz <name> <args>`.

Pull community adapters: `borz site update` (CLI only — triggers a git pull, intentionally not exposed over HTTP/MCP).

## Troubleshooting

- "Chrome not connected" → the daemon is up but CDP is down. Start Chrome, or let the daemon auto-launch: check `borz status`.
- "a daemon may already be running" → `borz daemon status`, `borz daemon shutdown` if stale.
- When unsure where the stack is broken, run `borz doctor` — it walks through home dir, daemon JSON, daemon process, daemon HTTP, CDP attach, tabs, and direct CDP discovery, and reports the first failing layer.
- "managed browser identity mismatch on port N" → the Chrome borz recorded as its own is not the one now on that port. `borz browser status` shows recorded vs live; `borz browser adopt` re-records the live one when it is borz's own (e.g. launched by an older borz). Do not delete `~/.borz/browser/browser.json` by hand.
- "Daemon did not start in time" → the message names the daemon squatting on the port (version + pid) when `~/.borz/daemon.json` is missing; `kill` that pid and re-run.
- Element ref not found → page changed between snapshot and action. Re-snapshot.
- Remote `server` refuses to start → non-loopback bind without `--token`. Set `BORZ_TOKEN` or pass `--token`.
- Hit a papercut (confusing command, missing flag, unhelpful output)? Record it: `borz feedback "<what was awkward>" [--category ux|bug|feature] [--command <cmd>]`. It appends to the local file `~/.borz/feedback.jsonl` for the maintainer to review — takes one command, do it whenever borz slows you down.

## Further reading

- `llm.txt` — compressed spec of CLI, MCP, and REST surfaces
- `README.md` — human-oriented docs with examples
- Source: https://github.com/leolin310148/borz
