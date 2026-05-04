# AGENTS.md

Concise coding-agent guidance for this repo. Use `CLAUDE.md` for fuller maintainer notes, `llm.txt` for the compact public surface reference, and `README.md` for user-facing docs.

## Project

`borz` is a single-binary Go CLI that controls Chromium through CDP. It has four public surfaces over one long-lived daemon: CLI, MCP stdio server, HTTP/REST API, and OpenAPI docs. The rename from `bb-browser` is still in progress, so legacy `BB_BROWSER_*` env vars and `cmd/bb-browser-shim/` remain supported.

## Commands

```bash
go build -o borz .
go vet ./...
go test -race ./...
go test -race -run TestFoo ./...
go test -race -coverprofile=c.out -covermode=atomic ./... && go tool cover -func=c.out
BORZ_E2E=1 go test -run TestE2ECLICommandsAgainstVerifySite -count=1 -v .
```

Set hooks once with `git config core.hooksPath .githooks`. Hooks and CI run vet, race tests, and enforce 85% total coverage.

## Architecture

```text
CLI / MCP / REST -> daemon HTTP API -> CDP WebSocket -> Chrome
```

Important locations:

- `main.go`, root `*_cli.go`: CLI dispatch and command logic
- `help.go`: command help and typo hints
- `internal/daemon/`: daemon, CDP, dispatch, REST, OpenAPI, snapshots, tab state
- `internal/mcp/`: MCP tools, handlers, response shaping
- `internal/protocol/`: shared request/response envelope
- `internal/client/`: browser discovery and managed launch
- `embed/buildDomTree.js`: DOM walker that creates snapshot refs

## Public Surface Sync

When adding or changing a browser action, keep these in sync:

- Daemon dispatch: `internal/daemon/dispatch.go`
- REST/OpenAPI: `internal/daemon/rest*.go`, `openapi.go`
- CLI: `main.go` or root `*_cli.go`
- MCP: `internal/mcp/handlers.go`, `tools.go`
- Help: `help.go`
- Tests for the touched CLI, daemon, REST, MCP, OpenAPI, and help behavior

Read `llm.txt` before changing commands, endpoints, tools, or documented behavior.

## Behaviors To Preserve

- `borz open <url>` reuses an existing tab with the exact same URL; `--new` forces a new tab.
- Snapshot refs are regenerated from the accessibility tree; re-snapshot after navigation or DOM-changing actions.
- `--wait-for <selector>` and `--timeout <ms>` apply to every page-changing action, not just `open`.
- CLI and MCP `eval` auto-wrap top-level `await`; REST `/v1/eval` does not.
- `borz server` must not bind non-loopback without a token.
- Daemon/client version mismatch is warning-only; never auto-restart, rediscover CDP, or launch Chrome from mismatch handling.
- Site adapters are arbitrary JS on the daemon filesystem and require trust when their SHA256 changes.
- Network, console, and error buffers are per-tab ring buffers.

## Subsystems

- `internal/site/`: trusted local/community site adapters
- `internal/recorder/`: `.borzrec` capture and render
- `internal/daemon/extbridge/`, `extension/`, `internal/extupdate/`: optional Chrome extension bridge and updater
- `internal/jq/`: built-in `--jq`
- `internal/jseval/`: eval wrapping and `--json-arg`
- `internal/diagnostics/`: `borz doctor`
- `internal/winservice/`: Windows service support
- `internal/selfupdate/`: binary self-update
- `internal/config/`: `~/.borz` config and legacy migration

## Conventions

Keep tests next to code. Use existing `*_more_test.go` and coverage-focused patterns where appropriate. Use platform suffix files (`_unix.go`, `_windows.go`, `_darwin.go`, `_linux.go`) instead of runtime GOOS branching. Update `help.go` for new CLI commands or flags.
