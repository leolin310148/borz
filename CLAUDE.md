# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`borz` is a single-binary Go CLI that drives any Chromium browser via the Chrome DevTools Protocol (CDP). It's a Go port of the Node.js `bb-browser`. The rename transition is still in flight — legacy `BB_BROWSER_*` env vars and a `bb-browser-shim` compatibility wrapper (`cmd/bb-browser-shim/`) are still shipped.

The full user-facing reference is `README.md`. A condensed agent-oriented overview is `llm.txt` — read it before adding commands or endpoints to make sure the three surfaces stay in sync.

## Commands

```bash
go build -o borz .                  # build
go vet ./...                        # lint
go test -race ./...                 # full test suite
go test -race -run TestFoo ./...    # single test
go test -race -coverprofile=c.out -covermode=atomic ./... && go tool cover -func=c.out
BORZ_E2E=1 go test -run TestE2ECLICommandsAgainstVerifySite -count=1 -v .   # opt-in real-Chrome e2e (skipped in CI)
```

Activate the repo pre-commit hook once per clone: `git config core.hooksPath .githooks`. It runs `go vet`, race tests, and enforces an **85% total coverage floor** — CI does the same. The hook skips when no `.go` files are staged.

## Architecture

### Three surfaces, one daemon

borz is a CLI client + a long-lived **daemon** that owns the persistent CDP WebSocket. All three front-ends speak to the same daemon:

```
CLI (main.go, *_cli.go) ──┐
MCP server (internal/mcp) ─┼──► daemon HTTP API ──► CDP WebSocket ──► Chrome
HTTP/REST server (`borz server`)──┘
```

The daemon is auto-spawned on first command. Browser auto-discovery + managed-launch lives in `internal/client/` (per-OS files). Daemon entry: `internal/daemon/server.go`; CDP plumbing: `cdp.go`; request dispatch: `dispatch.go`; REST routes: `rest*.go`; OpenAPI doc: `openapi.go`. Per-tab ringbuffers for network/console/errors are in `ringbuffer.go` + `tabstate.go`.

### Snapshot → ref → act

The core interaction model is: `snapshot` returns an accessibility tree with numeric `[ref=N]` ids, and interaction commands (`click`, `fill`, `type`, …) take those refs. The DOM-walking script that produces refs is `embed/buildDomTree.js` (embedded at build time). Snapshot logic: `internal/daemon/snapshot.go`, text-mode variant in `textsnapshot.go`.

### Wire protocol

Requests and responses share one shape across CLI/MCP/REST: `protocol.Request` / `{id, success, data?, error?}` envelope (`internal/protocol/`). When adding a new browser action, the change usually touches **all three layers**:

1. `internal/daemon/dispatch.go` (handle the request, talk to CDP)
2. `internal/daemon/rest.go` + `openapi.go` (REST route + spec)
3. `main.go` or a `*_cli.go` file (CLI subcommand)
4. `internal/mcp/handlers.go` + `tools.go` (MCP tool)
5. `help.go` (help text — used by `borz help <cmd>` and typo hints)

Keep these in sync; tests assert this (e.g. MCP handler tests, REST tests, OpenAPI test).

### Top-level Go files in repo root

`main.go` is the central CLI dispatcher. Per-command CLI logic that doesn't fit cleanly is split into `*_cli.go` files at the repo root (`ext_cli.go`, `extension_cli.go`, `client_cli.go`, `record_cli.go`, `viewport_cli.go`, `tail.go`, `doctor.go`). Help text is generated from `help.go` (a large data table — `help_test.go` enforces that every CLI command has an entry).

### Subsystems worth knowing

- `internal/site/` — **site adapters**: user/community JS plugins (`~/.borz/sites`, `~/.borz/bb-sites`) that automate specific websites. They run on the **daemon**'s filesystem with SHA256 trust prompts; remote clients invoking `/v1/sites/*` get whatever the server has. Domain origin guards + `entry`/`timeoutMs`/`output` schema are validated here.
- `internal/recorder/` + `record_cli.go` — `.borzrec` bundle capture (CDP mode or extension/client mode) and ffmpeg-based render with cursor/click/redaction overlays.
- `internal/daemon/extbridge/` + `extension/` — optional Chrome extension that exposes browser-level APIs CDP can't reach (all-domain cookies, bookmarks, history, downloads, windows, event streams). Extension is downloaded by `borz extension download` and version-locked to the binary; `internal/extupdate/` handles fetch/verify/extract.
- `internal/jq/` — built-in jq-compatible filter (no external `jq` binary). Used by `--jq` on every command.
- `internal/jseval/` — `eval` script preprocessing: top-level-await auto-wrap (`wrap.go`) + `--json-arg` injection (`jsonarg.go`).
- `internal/diagnostics/` — `borz doctor` end-to-end stack check, exposed via CLI, MCP (`browser_doctor`), and REST (`/v1/doctor`).
- `internal/winservice/` + `borz service` — Windows Service Control Manager registration for `borz server`.
- `internal/selfupdate/` — `borz update` self-replacement, with platform-specific replace strategies.
- `internal/config/` — `~/.borz/` layout (`daemon.json`, `client.json` 0600). Migrates `~/.bb-browser` → `~/.borz` on first write if the new dir doesn't already exist.

### Behaviors that bite

- `borz open <url>` **reuses an existing tab with the exact same URL** (focus only, no reload). Use `--new` to force fresh. Tests rely on this.
- `--wait-for <selector>` and `--timeout <ms>` are honored on **every page-changing action**, not just `open`. The daemon polls `document.querySelector` on a 100 ms tick.
- `borz eval` auto-wraps top-level `await` in an async IIFE (CLI + MCP only — REST `/v1/eval` does NOT auto-wrap; clients are responsible).
- `borz server` refuses to bind a non-loopback host without `--token` / `BORZ_TOKEN`.
- Daemon/client version mismatch is warning-only; never auto-restart, rediscover CDP, or launch Chrome from mismatch handling.
- Site adapters are arbitrary JS in the user's real Chrome session; changed SHA256 must be re-trusted (`borz site trust`) or run once with `--force`.

## Conventions

- Tests live next to the code (`foo.go` / `foo_test.go`) and there are dedicated `*_more_test.go` / `coverage_more_test.go` files in many packages purely to keep the 85% floor — when adding code, expect to add coverage there too.
- Cross-platform code uses build-tag suffixes (`_unix.go`, `_windows.go`, `_darwin.go`, `_linux.go`); follow that pattern instead of runtime `GOOS` checks.
- Help text lives in `help.go`. New CLI commands or flags must be added there or `help_test.go` will fail.
