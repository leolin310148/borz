# Design: unified profiles (`~/.borz/profiles.json`)

Status: approved by Leo, ready to implement.

## Problem

Three orthogonal ideas are tangled today, and none of them can express
"I have several browsers and I want to name them".

1. **`--profile <name>`** (`main.go:92-101`, `internal/config/config.go:60-73`)
   only isolates a *local runtime directory*: `daemon.json`, the managed
   browser's `user-data`, the `cdp-port` file, and logs. There is **no
   per-profile config file** — a profile is defined entirely by its directory
   existing. Nothing remembers *which browser* a profile talks to.

2. **`--remote`** (`internal/client/client.go:86`, `:97-116`) is a **global
   singleton**: `~/.borz/client.json` holds exactly one `{url, token}`. You
   cannot register two servers. Registering Mini necessarily clobbers the
   mdt-vpn entry. Worse, the remote branch short-circuits before any local
   daemon logic (`client.go:568-580`), so `--remote --profile mdt` **silently
   ignores the profile** — a quiet footgun, not an error.
   The `enabled` field is vestigial: `EnabledRemoteConfig()` (`client.go:184-196`)
   never reads it. Routing depends solely on the `--remote` flag.

3. **CDP host/port** exists only as a daemon **start-time flag**
   (`--cdp-host` / `--cdp-port`, `main.go:1045-1051`). It is never persisted.
   The only CDP artifact on disk is the per-profile `browser/cdp-port` file,
   and only for browsers borz itself launched (`client.go:819`).

## Goal

Make **the profile the single handle** for "which browser am I driving", and
let each profile declare **how** to reach it. One flag (`--profile`, or
`BORZ_PROFILE`) selects everything.

## Config: `~/.borz/profiles.json`

Single file, mode `0600` (it holds bearer tokens). Not profile-scoped — it is
the registry *of* profiles.

```jsonc
{
  "version": 1,
  "profiles": {
    "default": { "transport": "managed" },
    "clean":   { "transport": "managed" },
    "mini":    {
      "transport": "remote",
      "url": "http://100.116.143.73:13333",
      "token": "<bearer>"
    },
    "mdt": {
      "transport": "cdp",
      "cdpUrl": "http://127.0.0.1:19845",
      "idleTabTimeout": 0,
      "maxTabs": 30
    }
  }
}
```

### Transports

| `transport` | Meaning | Local daemon? | Fields |
| --- | --- | --- | --- |
| `managed` | borz launches and owns a Chrome under the profile's `browser/user-data`. Today's default behaviour. | yes (auto-spawn) | `idleTabTimeout`, `maxTabs` (optional) |
| `cdp` | Attach to an **existing** CDP endpoint (e.g. a Chrome started with `--remote-debugging-port`, possibly reached over an SSH tunnel). The endpoint is finally *persisted*. | yes (auto-spawn, pointed at that endpoint) | `cdpUrl`, or `cdpHost` + `cdpPort`; `idleTabTimeout`, `maxTabs` (optional) |
| `remote` | Talk HTTP to a remote `borz server`. No browser and no daemon locally. | **no** | `url`, `token` |

`cdpUrl` is the preferred spelling (it subsumes `BORZ_CDP_URL`); accept
`cdpHost`/`cdpPort` as an alternative and normalise both into one internal
`CDPTarget{Host, Port}`. Reject a profile that sets both spellings
inconsistently.

An **absent** `profiles.json`, or a profile name not present in it, means
`{"transport": "managed"}` — i.e. today's behaviour. Users who never touch
profiles must see zero change.

### `idleTabTimeout` (added after the initial rollout)

"Never auto-close this target's tabs" is a property of the *target*, not of
whoever happens to start the daemon. The motivating case is a `cdp` profile
attached (over an SSH tunnel) to a browser we do not own that carries live SSO
sessions: it must run with the idle-tab reaper disabled, and before this field
existed that guarantee lived in a launchd plist passing `--idle-tab-timeout 0`
— exactly the kind of config-outside-the-config this design is meant to kill.
If the plist's daemon wasn't up, older releases silently reverted to their
then-30-minute default and reaped someone else's tabs.

- Optional per-profile field, in **minutes**; `0` disables auto-close.
  Meaningful for `managed` and `cdp`. On `remote` profiles it is **rejected**
  at validation (the server owns tab lifecycle) — never silently ignored.
- Precedence: `--idle-tab-timeout` flag > `BORZ_TAB_IDLE_TIMEOUT` env >
  profile `idleTabTimeout` > default 0. Idle-tab auto-close is therefore off
  unless explicitly enabled.
- Auto-spawn (`internal/client`) passes `--idle-tab-timeout <n>` to the daemon
  it starts when the profile declares the field — unless the env var is set,
  in which case the flag is omitted so the inherited env keeps outranking the
  profile inside the daemon's own resolution.
- The daemon also resolves the profile layer itself (it knows its `--profile`),
  so a hand-started `borz daemon --profile mdt` honours the field too.
- The effective value is observable: daemon `GET /status` reports
  `idleTabCloseMinutes`.

### `maxTabs`

`maxTabs` is an independent hard guard against runaway tab creation and the
resulting browser memory pressure.

- Default 30 page tabs; `0` disables the cap.
- Meaningful for `managed` and `cdp`; rejected on `remote` because the remote
  server owns tab lifecycle.
- Precedence: `--max-tabs` > `BORZ_MAX_TABS` > profile `maxTabs` > default 30.
- Auto-spawn carries a declared profile value to the daemon unless the env var
  is set, matching `idleTabTimeout`.
- Enforcement runs immediately after a page target is registered. If the count
  exceeds the cap, the daemon closes the oldest non-current tabs until it is
  back at the limit. In-flight closes are excluded so concurrent target-created
  events cannot over-close.
- “Oldest” means `TabState.CreatedAt`, i.e. daemon-observed registration order.
  CDP does not expose original creation timestamps for tabs that already exist
  when the daemon starts.
- Daemon `GET /status` reports the effective `maxTabs`.

### What does NOT move into this file

`daemon.json` stays exactly as it is: **runtime state** (pid, bind host, bind
port, token), written by the daemon at start, deleted on shutdown, per-profile.
Config is *declared intent*; `daemon.json` is *observed reality*. Merging them
would make the file both hand-editable and machine-clobbered. Same for
`browser/cdp-port` and the managed `user-data` dir.

## Resolution

Replace the scattered `useRemote` global + ad-hoc CDP discovery with one
function, conceptually:

```go
// internal/config (or a new internal/profile package)
type Target struct {
    Kind      TransportKind      // Managed | CDP | Remote
    Remote    RemoteTarget       // URL, Token        (Kind == Remote)
    CDP       CDPTarget          // Host, Port        (Kind == CDP)
}

func ResolveTarget(profile string) (Target, error)
```

Order:

1. `--profile` / `BORZ_PROFILE` / `BB_BROWSER_PROFILE` → profile name (unchanged,
   `main.go:92-101`).
2. `ResolveTarget(name)` reads `profiles.json`; missing entry → `managed`.
3. Dispatch:
   - **`remote`** → every browser command POSTs to `url` with
     `Authorization: Bearer <token>`. No `EnsureDaemon()`, no browser spawn —
     the existing remote branch, just fed from the profile instead of
     `client.json`.
   - **`cdp`** → `EnsureDaemon()` as today, but `DiscoverCDPPort()` is
     **bypassed**: the daemon is spawned with `--cdp-host/--cdp-port` taken
     straight from the profile. If that endpoint is unreachable, fail with a
     clear error — do **not** silently fall back to launching a managed Chrome
     (that would open a stranger's browser when the SSH tunnel is down).
   - **`managed`** → exactly today's `EnsureDaemon()` + `DiscoverCDPPort()` +
     `launchManagedBrowser()` path, unchanged.
4. Lifecycle commands (`daemon status|stop`, `server status|shutdown`) keep
   targeting the **local** profile-scoped `daemon.json` (`client.go:713`, `:606`).
   For a `remote` profile they must say so plainly rather than pretending a
   local daemon exists — e.g. `daemon status --profile mini` →
   "profile 'mini' is a remote profile (http://…); there is no local daemon".

## CLI surface

```
borz profile list                       # name, transport, target, (live?)
borz profile show <name>                # token redacted
borz profile add <name> --remote <url> --token <t>
borz profile add <name> --cdp <url|host:port>
borz profile add <name> --managed
borz profile set <name> --token <t>     # edit fields in place
borz profile add <name> --cdp <hp> --idle-tab-timeout 0   # never reap this target's tabs
borz profile set <name> --idle-tab-timeout <m|default>    # 'default' clears the field
borz profile set <name> --max-tabs <n|default>            # 0 means unlimited
borz profile rm <name>
```

- `--token` falls back to `BORZ_TOKEN`, matching `client setup` today.
- `profile add` probes the target (`/status` for remote, `/json/version` for
  cdp) unless `--no-check`, matching `client setup --no-check`.
- Never print a token; `show`/`list` redact.
- Name validation reuses `config.SetProfile`'s rules (single portable path
  segment) — a profile name still becomes a directory for `managed`/`cdp`.

### Deprecations (keep working, warn once)

- `borz client setup <url> --token T` → writes a profile named **`remote`**
  (or `--as <name>`) and prints "deprecated: use `borz profile add`".
- `--remote` → resolves to the profile that `client.json` used to point at.
  Concretely: on first run, **migrate** `client.json` into `profiles.json` as a
  profile named `remote`, and treat bare `--remote` as `--profile remote`.
  Keep `client.json` on disk (do not delete) so a rollback is painless.
- `--remote` combined with an explicit `--profile X` is currently a silent
  no-op. Make it an **error**: "--remote and --profile are mutually exclusive;
  --profile selects the transport".
- `client enable|disable` and the `enabled` field: already dead. Keep the
  commands as no-ops that print a deprecation note; do not carry `enabled` into
  `profiles.json`.

## Migration

On any command that reads config, if `profiles.json` is absent **and**
`client.json` exists with a non-empty `url`, write:

```jsonc
{ "version": 1, "profiles": { "remote": { "transport": "remote", "url": …, "token": … } } }
```

atomically (temp file + `os.Rename`, 0600), log one line at debug level, and
carry on. Idempotent; never overwrites an existing `profiles.json`.

## Testing

- Unit: `ResolveTarget` for each transport, missing file, missing profile,
  malformed entry, both cdp spellings, mutually-exclusive-flag error.
- Unit: migration from `client.json` — happens once, is idempotent, does not
  clobber an existing `profiles.json`, preserves 0600.
- E2E (the repo already has a strong e2e suite — extend it, don't invent a new
  harness): a `managed` profile still auto-spawns and drives a browser; a `cdp`
  profile attaches to an already-running Chrome and does **not** launch one; a
  `cdp` profile with a dead endpoint fails loudly instead of launching a browser;
  a `remote` profile never spawns a local daemon.
- Verify `borz --profile default …` and a bare `borz …` on a machine with no
  `profiles.json` behave **exactly** as before.

## Non-goals

- Multiple simultaneous transports per profile, or fallback chains.
- Any change to the daemon's HTTP API, the extension protocol, or MCP.
- Merging `daemon.json` into config.
- Secret storage beyond a 0600 file (no Keychain integration for now).
