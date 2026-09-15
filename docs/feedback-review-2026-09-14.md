# Feedback review — 2026-09-14

Scope: follow-up review of local feedback after the 2026-09-07 pass. Original
IDs below are 1-based line numbers from the pre-cleanup 177-record log.

## Resolved in this pass

- #153, #162, #175: click accepts a tightly bounded checkbox/radio wrapper
  sibling as the hit target. It observes the control state and only invokes the
  original control fallback when the wrapper click did not change it.
- #165: `fetch` preserves malformed JSON as raw text with `parseError`, supports
  `--raw`, and can write the body locally with `--output`.
- #168: snapshots accept `--limit` / `limit` on CLI, MCP, and REST. Truncated
  output reports its original line count and only returns refs that remain in
  the visible result.
- #172: `navigate` is a current-tab CLI command; unlike `open`, it never creates
  a tab.
- #173: `extract --text` is a reader-mode alias for `snapshot --text-only`.
- #174: rejecting an interactive community-adapter trust prompt now prints the
  exact `site info`, `site trust`, and one-shot `--force` recovery commands.
- #177: remote-profile download results are marked with
  `filesystem: "remote"` and the selected profile, making daemon-host paths
  explicit.

The deployment verification records #148–#150 were also already covered by
the mouse CLI/MCP and jq fixes on `main`.

## Cleanup

After tests passed, 93 proven-resolved or verification-only records were moved
out of the active log into
`~/.borz/archive/feedback-resolved-2026-09-14.jsonl`. The cleanup left 84 active
records that are feature requests, external constraints, site-specific cases
awaiting reproduction, or otherwise lack enough evidence to call fixed. New
feedback written afterward remains in the active log as expected.

## Validation

- `go vet ./...`
- `go test -race -coverprofile=... -covermode=atomic ./...`: passed; total
  coverage 87.4%.
- `BORZ_E2E=1 go test -run '^TestE2EFeedbackRegressions$' -count=1 -v .`:
  passed against real Chrome, including the covered checkbox fixture.
