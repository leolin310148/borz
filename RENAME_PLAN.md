# Rename Plan: `bb-browser-go` → `borz`

## Why rename

`bb-browser` / `bb-browser-go` is long, hard to type, and not memorable. The `-go` suffix advertises the implementation language (which users don't care about) and the `bb-` prefix is opaque. We want a short, distinctive, greppable name that fits the project's identity: a CLI that drives Chrome via an extension, with remote-server routing and browser-data APIs.

## Chosen name: `borz`

- 4 letters, one syllable, easy to type and say.
- No meaningful collision in the browser-automation / dev-tools space. Only notable hit on GitHub is [`l2zou/borz-client`](https://github.com/l2zou/borz-client), an unrelated low-activity Rust CLI for a social-network platform.
- Effectively available as a Go module path (`github.com/leolin310148/borz`) and as a Homebrew formula name (to be verified before tap publish).

## Migration strategy (high level)

1. **Rename the GitHub repo** `leolin310148/bb-browser-go` → `leolin310148/borz`.
   - GitHub installs a permanent silent redirect from the old URL: existing `git remote`s, release-artifact links, and API calls keep working indefinitely.
   - Caveat: the redirect breaks if the old name slot is ever reclaimed (new repo created with old name, or old name transferred). Don't recreate `bb-browser-go`.

2. **Do not create a stub `bb-browser-go` repo.** A visible "we moved" notice would require occupying the old name, which kills the silent redirect and breaks existing `git pull`s. The silent redirect is better UX than a loud signpost.

3. **Communicate the rename via release notes**, not via a stub repo. Cut a final `bb-browser-go` release with a CHANGELOG entry explaining the rename and the new install URLs, then continue all future releases under the `borz` name.

4. **Binary rename: dual-binary transition.** Ship `borz` as the primary binary plus a thin `bb-browser` wrapper that execs `borz` and prints a one-line deprecation notice to stderr. Drop the wrapper after ~2 minor versions. This minimizes breakage for existing users' shell aliases, scripts, and CI.

## Touchpoints in this repo

Code / config:
- `go.mod` — module path `github.com/leolin310148/bb-browser-go` → `github.com/leolin310148/borz`.
- All Go imports of `github.com/leolin310148/bb-browser-go/...` (every package under `internal/`).
- `main.go`, `help.go` — any hardcoded program name in usage strings, version output, or `argv[0]`-sensitive logic.
- `internal/config/config.go` — branding constants, default config-dir name (e.g. `~/.bb-browser` → `~/.borz`; needs migration logic, see below).
- `internal/selfupdate/selfupdate.go` — release-asset URL pattern.
- `internal/extupdate/extupdate.go` — extension-asset URL pattern.
- `internal/daemon/embed/openapi.yaml` — service title / info block.
- `skill.md` — any references to the CLI name.
- Test files referencing the binary name in fixtures or expected output.

Release / CI:
- `.github/workflows/ci.yml` lines 86, 103, 108 — artifact names: `bb-browser-${GOOS}-${GOARCH}` → `borz-${GOOS}-${GOARCH}`, `bb-browser-extension.zip` → `borz-extension.zip`, checksums glob.
- The dual-binary shim: emit both `borz-*` and a small `bb-browser-*` wrapper in the release job.

Docs:
- `README.md` — install snippets (download URLs, `mv` target paths), all examples, badges, the `bb-browser-go` → original `bb-browser` Node.js attribution line.

Repo cleanup:
- `bb-browser` and `bb-browser-go` in the repo root are committed pre-built binaries (~11 MB each). They should be removed regardless of the rename and added to `.gitignore`. Replace with a clean `dist/` build output.

## Binary rename — concrete plan

Primary binary: `borz`.

Transition wrapper (`bb-browser`):
- Behavior: `exec` the resolved `borz` binary on the same `PATH`, forwarding all argv and env unchanged.
- Side effect: print exactly one line to stderr on each invocation, e.g. `bb-browser is deprecated; please use 'borz' instead. This wrapper will be removed in v<N+2>.`
- Implementation: separate tiny Go `main` under `cmd/bb-browser-shim/` (or equivalent), built and shipped from the same release workflow.
- Removal: after two minor releases, drop the shim build target and the `bb-browser-*` release artifacts.

Config / state directory:
- If we currently use `~/.bb-browser` (or similar), the daemon should: prefer `~/.borz` if present; otherwise read `~/.bb-browser` and on first write migrate it to `~/.borz`. One-shot, transparent. Document in CHANGELOG.

## Step-by-step execution order

1. **Land the rename PR on `main` (still in the old repo):**
   - Update `go.mod` module path and all imports.
   - Update `main.go` / `help.go` program-name strings.
   - Update `internal/config` defaults + add config-dir migration shim.
   - Update `README.md` and `skill.md`.
   - Update `.github/workflows/ci.yml` artifact names.
   - Add `cmd/bb-browser-shim/` with the deprecation wrapper.
   - Remove the committed `bb-browser` and `bb-browser-go` binaries; add to `.gitignore`.
   - Tag a final pre-rename release (e.g. `v<X>.<Y>.<Z>-final-bb-browser`) with CHANGELOG entry announcing the rename.

2. **Rename the GitHub repo** in the GitHub UI: Settings → Repository name → `borz`.
   - Verify the silent redirect works: `git ls-remote https://github.com/leolin310148/bb-browser-go.git` should resolve.
   - Update `git remote set-url origin git@github.com:leolin310148/borz.git` locally.

3. **Cut the first `borz`-named release** (e.g. `v<X+1>.0.0`).
   - Artifacts: `borz-darwin-arm64`, `borz-darwin-amd64`, `borz-linux-amd64`, `borz-linux-arm64`, `borz-windows-amd64.exe`, `borz-extension.zip`, plus `bb-browser-*` shim variants.
   - CHANGELOG: rename announcement + install instructions for the new binary name.

4. **Two releases later**, drop the `bb-browser-*` shim artifacts and the `cmd/bb-browser-shim/` directory.

## What we're explicitly NOT doing

- Not creating a placeholder `bb-browser-go` repo to display a notice. Would break GitHub's silent redirect.
- Not transferring the old repo name elsewhere. Same reason.
- Not changing the Chrome extension's user-facing name unless we want to (separate decision; the extension ID is what matters technically).
- Not bumping to v1.0 just because of the rename. Use the next natural version bump.

## Open questions

- Final binary name confirmed: `borz` (not `brox`, which collides with [`broxme/brox-browser`](https://github.com/broxme/brox-browser) and [`wuha-io/broxjs`](https://github.com/wuha-io/broxjs) in adjacent territory).
- Homebrew tap / formula availability for `borz` — verify before publishing.
- Whether to keep the `leolin310148` GitHub user namespace or move to a project org (`borz-cli`, `borz-dev`, etc.) at the same time. Doing both at once is cheap if we're already breaking URLs.
- Chrome extension display name and store listing — keep, or rebrand to match?
