---
title: "Configuration"
description:
  "Thrum configuration model — config.json schema, runtime tiers, priority
  chain, and the config show command"
category: "infrastructure"
order: 3
tags:
  ["configuration", "config", "runtime", "daemon", "settings", "config-show"]
last_updated: "2026-04-19"
---

## Configuration

Thrum uses `.thrum/config.json` as the single source of truth for user
preferences. Everything works with sensible defaults — you only need to edit
config.json when you want to change something.

## Config File

Located at `.thrum/config.json` in your repository:

```json
{
  "daemon": {
    "local_only": true,
    "sync_interval": 60,
    "ws_port": "auto",
    "peer_port": "auto",
    "single_agent_mode": true,
    "log_level": "info"
  },
  "peers": {
    "auto_connect": true,
    "pairing_code_length": 16
  },
  "worktrees": {
    "base_path": "~/.thrum/worktrees/myproject",
    "beads_enabled": true,
    "thrum_enabled": true
  },
  "orchestration": {
    "merge_target": "main",
    "default_autonomy": "end_only"
  },
  "permission_supervisors": ["coordinator"],
  "project_name": "myproject",
  "identity": {
    "daemon_id": "d_01HYTESTULID01234567890AB",
    "repo_name": "myproject",
    "hostname": "mymachine",
    "repo_path": "/Users/me/dev/myproject",
    "git_origin_url": "https://github.com/me/myproject",
    "init_at": "2026-04-17T06:30:00Z"
  },
  "identity_guard": {
    "cross_worktree": "strict",
    "quickstart_self_rename": "strict",
    "quickstart_name_collision": "strict",
    "non_git_bootstrap": "strict",
    "unauthenticated_rpc": "strict",
    "daemon_writer_liveness": "strict",
    "prime_ownership": "strict",
    "dead_pid_auto_reclaim": "warn"
  },
  "restart": {
    "max_lines": 200,
    "auto_threshold": 0,
    "graceful_timeout": 30
  },
  "backup": {
    "dir": "/path/to/backups",
    "schedule": "24h",
    "retention": {
      "daily": 5,
      "weekly": 4,
      "monthly": -1
    },
    "post_backup": "aws s3 sync /path/to/backups s3://my-bucket/thrum",
    "plugins": [
      {
        "name": "beads",
        "command": "bd backup --force",
        "include": [".beads/backup/*"]
      }
    ]
  }
}
```

After `thrum init`, a minimal config looks like:

```json
{
  "runtime": { "primary": "claude" },
  "daemon": { "single_agent_mode": true }
}
```

This file is created during `thrum init` and can be edited at any time.

## Schema Reference

### `daemon.local_only`

Disable remote git sync (local-only mode).

- **Type:** boolean
- **Default:** `true`
- **Override:** `THRUM_LOCAL=false` environment variable

### `daemon.sync_interval`

Seconds between git sync operations.

- **Type:** integer (seconds)
- **Default:** `60`
- **Override:** `THRUM_SYNC_INTERVAL` environment variable

### `daemon.ws_port`

WebSocket server port.

- **Type:** string
- **Default:** `"auto"` (find free port dynamically)
- **Values:** `"auto"` or a specific port number like `"9999"`
- **Override:** `THRUM_WS_PORT` environment variable

### `daemon.peer_port`

Tailscale peer-to-peer listener port for cross-repo communication.

- **Type:** string
- **Default:** `"auto"`
- **Values:** `"auto"` or a specific port like `"9100"`
- **Override:** `THRUM_TS_PORT` environment variable

### `daemon.single_agent_mode`

Disable messaging infrastructure for single-agent workflows. When enabled, Thrum
skips the background listener, cron watchdog, stop hook checks, and inbox
processing. Context management features (prime, context save/show, sessions)
remain active.

- **Type:** boolean
- **Default:** `false` (but `thrum init` sets it to `true` for new workspaces)
- **Toggle:** `thrum single-agent-mode true|false`

See [Single-Agent Mode](single-agent-mode.md) for details.

### `daemon.log_level`

Slog log level for the daemon log file (`.thrum/var/daemon.log`).

- **Type:** string
- **Default:** `"info"`
- **Values:** `"debug"`, `"info"`, `"warn"` (or `"warning"`), `"error"`
- **Case:** insensitive

The daemon uses [lumberjack](https://github.com/natefinch/lumberjack) for log
rotation: 10 MB max file size, 4 rotated backups, 28-day retention, gzip
compression. View logs with `thrum daemon logs` (see
[CLI Reference](cli.md#thrum-daemon-logs)).

## Worktrees

Settings for `thrum worktree create/teardown/list` (alias:
`thrum worktree setup`). These are written automatically by `thrum init` and can
be edited in `config.json`. See [Orchestrator Role](orchestrator-role.md) for
how the orchestrator uses these.

### `worktrees.base_path`

Directory where `thrum worktree create` puts new worktrees.

- **Type:** string (absolute path)
- **Default:** `~/.thrum/worktrees/<project>` (inferred from repo name; migrated
  from `~/.workspaces/<project>` in v0.10.0 — set the legacy path explicitly if
  you want to keep using it)

### `worktrees.beads_enabled`

Whether to create `.beads/redirect` in new worktrees.

- **Type:** boolean
- **Default:** `true` (set by `thrum init`)

### `worktrees.thrum_enabled`

Whether to create `.thrum/redirect` and `.thrum/identities/` in new worktrees.

- **Type:** boolean
- **Default:** `true` (set by `thrum init`)

## Orchestration

Settings for the orchestrator role's execution lifecycle. See
[Orchestrator Role](orchestrator-role.md) for details.

### `orchestration.merge_target`

Branch for the final merge after all epics complete.

- **Type:** string
- **Default:** `"main"`

### `orchestration.default_autonomy`

When to request human approval during plan execution.

- **Type:** string
- **Default:** `"end_only"`
- **Values:** `"per_epic"` (approve after each epic) or `"end_only"` (approve
  only at the end)

## Permission Supervisors

### `permission_supervisors`

List of recipients for permission-prompt nudges from tmux-managed agents. When a
tmux agent pauses waiting for a permission confirmation, the daemon delivers a
nudge message to every entry in this list.

- **Type:** array of strings
- **Default:** `["coordinator"]` (applied at nudge-dispatch time)

Each entry is one of:

- A role name (`"coordinator"`, `"orchestrator"`) — broadcasts to every active
  agent with that role
- A specific agent name (`"@impl_team_fix"`) — direct delivery to that agent
- A specific user (`"@user:leon-letto"`) — direct delivery; auto-forwarded to
  Telegram if the bridge is configured for that user

Example:

```json
{
  "permission_supervisors": ["coordinator", "@user:leon-letto"]
}
```

### `project_name`

Short human-readable identifier used to form the `@supervisor_<project>`
pseudo-agent sender identity on permission-prompt nudges. Falls back to
`filepath.Base(repo_root)` at daemon boot if empty or absent.

- **Type:** string
- **Default:** (none — derived from repo root directory name)
- **Example:** `"thrum"` → nudges appear from `@supervisor_thrum`

## Peers

Settings for cross-repo peer connections. See
[Architecture — Peer System](architecture.md#cross-repo-peer-system) for how
this works.

### `peers.auto_connect`

Automatically connect to all known peers when the daemon starts.

- **Type:** boolean
- **Default:** `true`

### `peers.pairing_code_length`

Length of generated pairing codes for `thrum peer add`.

- **Type:** integer
- **Default:** `16`

## Backup

Thrum can archive your message history and agent data using a
Grandfather-Father-Son (GFS) rotation scheme. The `backup` section is omitted
from `config.json` if you are using all defaults.

### `backup.dir`

Directory where backup archives are written.

- **Type:** string (path)
- **Default:** `.thrum/backup` (relative to repo root)

### `backup.schedule`

Automatic backup interval. The daemon runs a backup at this interval when
running. Use `thrum backup schedule` to configure via CLI.

- **Type:** string (Go duration format)
- **Default:** none (scheduled backups disabled)
- **Examples:** `"24h"`, `"12h"`, `"8h"`, `"168h"` (1 week)
- **CLI:** `thrum backup schedule 24h` / `thrum backup schedule off`
- **Note:** Daemon must be restarted after changing the schedule

### `backup.retention.daily`

Number of daily backup archives to retain.

- **Type:** integer
- **Default:** `5`
- **Special:** `0` keeps no daily backups; `-1` keeps all

### `backup.retention.weekly`

Number of weekly backup archives to retain.

- **Type:** integer
- **Default:** `4`
- **Special:** `0` keeps no weekly backups; `-1` keeps all

### `backup.retention.monthly`

Number of monthly backup archives to retain.

- **Type:** integer
- **Default:** `-1` (keep all monthly backups)
- **Special:** `0` keeps no monthly backups

### `backup.post_backup`

Shell command to run after each backup completes successfully. Useful for
offloading archives to cloud storage or triggering external notifications.

- **Type:** string
- **Default:** none
- **Example:** `"aws s3 sync /path/to/backups s3://my-bucket/thrum"`

### `backup.plugins`

List of third-party backup plugin definitions. Each plugin is an object with the
following fields:

| Field     | Type             | Description                                        |
| --------- | ---------------- | -------------------------------------------------- |
| `name`    | string           | Unique plugin identifier                           |
| `command` | string           | Shell command to run before collecting files       |
| `include` | array of strings | Glob patterns for files to collect into the backup |

Use `thrum backup plugin add --preset beads` to add the built-in Beads preset,
or define custom plugins manually. The command runs from the repo root before
file collection; include patterns are evaluated after the command completes.

Example:

```json
{
  "backup": {
    "dir": "~/.thrum-backups",
    "schedule": "24h",
    "retention": {
      "daily": 7,
      "weekly": 4,
      "monthly": -1
    },
    "post_backup": "rsync -a ~/.thrum-backups/ backup-host:/thrum/",
    "plugins": [
      {
        "name": "beads",
        "command": "bd backup --force",
        "include": [".beads/backup/*"]
      }
    ]
  }
}
```

## Restart

Session restart settings for context snapshot behavior. See
[Session Restart & Context Recovery](session-restart.md).

### `restart.max_lines`

Maximum lines in a restart snapshot. The snapshot now appends a brief `git log`
/ `git status` / `bd ready` / `thrum inbox` summary at the tail, so the
effective context delivered on restart is richer per line. The default was
reduced from `1000` to `200` in v0.9.0 to reflect this.

- **Type:** integer
- **Default:** `200`

### `restart.auto_threshold`

Context window usage percentage that triggers automatic restart. Set to 0 to
disable.

- **Type:** integer (0-100)
- **Default:** `0` (disabled)

### `restart.graceful_timeout`

Seconds to wait for an agent to save its own snapshot during graceful restart
before falling back to force extraction.

- **Type:** integer (seconds)
- **Default:** `30`

### `restart.silence_watchdog_seconds`

How long after `thrum tmux launch` or `thrum tmux restart` the daemon waits for
the agent pane to produce output before injecting a contextual nudge. The
watchdog fires only if the pane is silent for the configured duration; an
actively-producing agent is not interrupted.

- **Type:** integer (seconds)
- **Default:** `30`
- **Special values:** `0` → use default (30s); negative → disable the watchdog

Example config with restart enabled:

```json
{
  "restart": {
    "max_lines": 200,
    "auto_threshold": 80,
    "graceful_timeout": 30,
    "silence_watchdog_seconds": 30
  }
}
```

## Daemon Identity

The `identity` block stores the daemon's per-repo fingerprint. It is populated
automatically by `thrum init` (and by the first `thrum daemon start` on a
pre-v0.9.0 install that upgrades). You never need to edit this block manually.

### `identity` block

```json
{
  "identity": {
    "daemon_id": "d_01HYTESTULID01234567890AB",
    "repo_name": "thrum",
    "hostname": "leonsmacm1pro",
    "repo_path": "/Users/leon/dev/opensource/thrum",
    "git_origin_url": "https://github.com/leonletto/thrum",
    "init_at": "2026-04-17T06:30:00Z"
  }
}
```

| Field            | Set once or refreshed | Description                                                                                                                                       |
| ---------------- | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `daemon_id`      | Set once              | ULID-based (`d_` + 26 chars). Generated at `thrum init`; rotated only when migrating from a legacy hostname-derived id. Never changes on re-init. |
| `repo_name`      | Refreshed             | `filepath.Base(repo_root)`. Updated on every Bootstrap call (daemon start or `thrum init`).                                                       |
| `hostname`       | Refreshed             | `os.Hostname()` result. Updated on every Bootstrap call.                                                                                          |
| `repo_path`      | Refreshed             | Absolute repo path. Updated on every Bootstrap call.                                                                                              |
| `git_origin_url` | Set once              | Output of `git config --get remote.origin.url`. Set at init; not updated if already non-empty.                                                    |
| `init_at`        | Set once              | RFC 3339 UTC timestamp of `daemon_id` creation or rotation. Not changed on re-init of an existing valid ULID.                                     |

The "set once" fields are the stable identity keys. The "refreshed" fields are
informational metadata that keeps the block current across hostname changes,
path moves, and re-clones. See [Identity System](identity.md) for the full
daemon identity lifecycle.

## Identity Guards

The `identity_guard` block configures the enforcement mode for each of Thrum's
eight identity ownership checkpoints. Every guard defaults to `strict` if the
block is absent.

### `identity_guard` block

```json
{
  "identity_guard": {
    "cross_worktree": "strict",
    "quickstart_self_rename": "strict",
    "quickstart_name_collision": "strict",
    "non_git_bootstrap": "strict",
    "unauthenticated_rpc": "strict",
    "daemon_writer_liveness": "strict",
    "prime_ownership": "strict",
    "dead_pid_auto_reclaim": "warn"
  }
}
```

**Enforcement modes:**

| Mode     | Behavior                                                                                     |
| -------- | -------------------------------------------------------------------------------------------- |
| `strict` | Guard fires; RPC or command is refused. The default for all guards.                          |
| `warn`   | Guard fires; the violation is logged but the action proceeds. Useful for incident diagnosis. |
| `off`    | Guard check is skipped entirely. Use only when you understand why it is firing.              |

**Guard keys:**

| Key                         | What it checks                                                                                                                                                                                                                |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cross_worktree`            | Caller's ancestor PID chain does not contain the identity file's `agent_pid`. The central ownership enforcement rule.                                                                                                         |
| `quickstart_self_rename`    | Caller already owns an identity in this directory and is attempting to re-register under a new name (G1a).                                                                                                                    |
| `quickstart_name_collision` | Requested agent name is already held by a live foreign process (G1b).                                                                                                                                                         |
| `non_git_bootstrap`         | `thrum daemon start` or `thrum init` was called from outside a git-anchored directory (G2).                                                                                                                                   |
| `unauthenticated_rpc`       | Mutating RPC with no verifiable caller identity, or a caller asserting an identity the daemon cannot corroborate (G3). The `identity_mismatch` sub-case (forgery) is unconditional and fires regardless of this mode setting. |
| `daemon_writer_liveness`    | Daemon attempted to write to an identity file whose recorded agent PID is no longer alive (G4).                                                                                                                               |
| `prime_ownership`           | `thrum prime` was called from inside a sub-agent whose closest runtime ancestor is not the identity file's owner (G5).                                                                                                        |
| `dead_pid_auto_reclaim`     | Dead owner's identity was reclaimed by a new caller. Informational; defaults to `warn` to log reclaims without blocking them.                                                                                                 |

**Daemon-level overrides:** `.thrum/var/guard-daemon.json` accepts the same key
shape as `identity_guard` and is merged on top of the repo-level config. Use
this to change a single guard mode without editing the tracked `config.json`.
Daemon-level values take precedence over repo-level values; both layers take
precedence over the built-in `strict` defaults.

For per-error remediation steps, see
[Troubleshooting — Identity Guards](troubleshooting-identity.md).

## Tmux Configuration

If you're using [tmux-managed sessions](tmux-sessions.md), create a
`~/.tmux.conf` with mouse support:

```bash
# ~/.tmux.conf
set -g mouse on
```

Without this, scrolling doesn't work in tmux sessions and the experience feels
broken. This one line makes tmux behave like a regular terminal.

## Priority Chain

When the same setting can come from multiple sources, this order applies:

```text
CLI flag  >  Environment variable  >  config.json  >  Default
```

Environment variables are intended for CI/automation overrides, not primary
configuration. For day-to-day use, edit `config.json`.

### Environment Variable Reference

| Variable              | Overrides                       | Example                    |
| --------------------- | ------------------------------- | -------------------------- |
| `THRUM_LOCAL`         | `daemon.local_only`             | `THRUM_LOCAL=false`        |
| `THRUM_SYNC_INTERVAL` | `daemon.sync_interval`          | `THRUM_SYNC_INTERVAL=120`  |
| `THRUM_WS_PORT`       | `daemon.ws_port`                | `THRUM_WS_PORT=9999`       |
| `THRUM_NAME`          | Agent identity selection        | `THRUM_NAME=alice`         |
| `THRUM_ROLE`          | Agent role                      | `THRUM_ROLE=planner`       |
| `THRUM_MODULE`        | Agent module                    | `THRUM_MODULE=backend`     |
| `THRUM_HOME`          | Repo path for all commands      | `THRUM_HOME=/path/to/repo` |
| `THRUM_AGENT_ID`      | Caller identity for daemon RPC  | `THRUM_AGENT_ID=alice`     |
| `THRUM_SOCKET`        | Unix socket path override       | `THRUM_SOCKET=/tmp/t.sock` |
| `THRUM_TS_AUTHKEY`    | Tailscale auth key for peering  | `THRUM_TS_AUTHKEY=tskey-…` |
| `THRUM_TS_PORT`       | Tailscale listener port         | `THRUM_TS_PORT=9100`       |
| `THRUM_TS_HOSTNAME`   | tsnet hostname override         | `THRUM_TS_HOSTNAME=myhost` |
| `THRUM_TS_STATE_DIR`  | tsnet state directory           | `THRUM_TS_STATE_DIR=…`     |
| `THRUM_TS_ENABLED`    | _(deprecated)_ Tailscale toggle | `THRUM_TS_ENABLED=true`    |
| `THRUM_NO_HINTS`      | Suppress Shape B stderr hints   | `THRUM_NO_HINTS=1`         |

`THRUM_NO_HINTS` suppresses the contextual hint lines that some commands emit to
stderr after execution (`thrum send`, `thrum tmux create`, `thrum init`). Truthy
semantics: any non-empty value except `"0"` or `"false"` (case-insensitive)
disables hints. The `--quiet` flag and `--json` flag also suppress hints (with
`--json`, hints move into the JSON output `hints` array instead of being dropped
entirely). See [CLI Hints](cli-hints.md) for the full hint system reference.

`THRUM_HOME` pins all thrum commands to the specified repo path regardless of
the current working directory. This is set automatically by `thrum-startup.sh`
to prevent identity drift when an agent `cd`s into a different worktree.

`THRUM_AGENT_ID` pins the caller identity for daemon RPC calls, bypassing
identity file lookup. When set, commands like `thrum prime` and `thrum overview`
use this agent ID directly.

## Runtime Templates

During `thrum init`, Thrum can generate configuration files for various AI
coding runtimes:

- **Claude Code** - CLAUDE.md and .claude/agents/
- **Augment** - .augment/ directory
- **Cursor** - .cursorrules
- **Codex** - codex.yaml
- **Gemini** - .gemini/
- **CLI-only** - No runtime configuration files

Use `thrum init --runtime <name>` to specify which runtime template to generate.
The selected runtime is saved as `runtime.primary` in `config.json`. The
generated template files (CLAUDE.md, .cursorrules, etc.) are not tracked in
`config.json` — only the primary runtime choice is.

## Viewing Configuration

Use `thrum config show` to see the effective configuration and where each value
comes from:

```text
Thrum Configuration
  Config file: .thrum/config.json

Runtime
  Primary:     claude (from config.json)
  Detected:    claude, augment

Daemon
  Local-only:    true (config.json)
  Sync interval: 60s (default)
  WS port:       9999 (active)
  Status:        running (PID 7718)
  Socket:        .thrum/var/thrum.sock

Identity
  Agent:       claude_planner
  Role:        planner
  Module:      coordination

Overrides (environment)
  THRUM_NAME=claude_planner
```

Use `thrum config show --json` for machine-readable output.

## Role Config

The `role_config` top-level key persists role template answers written by
`/thrum:configure-roles` and `thrum roles save-config`. It is never set by hand
— the skill and CLI command are the intended writers.

### `role_config` structure

```json
{
  "role_config": {
    "schema_version": 1,
    "roles": {
      "implementer": {
        "autonomy": "autonomous",
        "scope": "auth module",
        "rendered_hash": "sha256:abc123..."
      },
      "coordinator": {
        "autonomy": "strict",
        "scope": "",
        "rendered_hash": "sha256:def456..."
      }
    }
  }
}
```

**Per-role fields** (under `role_config.roles.<role-name>`):

| Field           | Type   | Description                                                                              |
| --------------- | ------ | ---------------------------------------------------------------------------------------- |
| `autonomy`      | string | Template variant selected: `"strict"` or `"autonomous"`                                  |
| `scope`         | string | User-supplied scope string embedded into the rendered template (may be empty)            |
| `rendered_hash` | string | SHA-256 of the shipped template body used when this role was last rendered (drift check) |

**Top-level fields** (under `role_config`):

| Field            | Type    | Description                                                                                          |
| ---------------- | ------- | ---------------------------------------------------------------------------------------------------- |
| `schema_version` | integer | Schema version of the saved config; compared against the current shipped version for drift detection |

**Writes are atomic:** both `/thrum:configure-roles` and
`thrum roles save-config` write via a temp file + rename, preserving every other
top-level key (`backup`, `daemon`, `identity`, `telegram`, etc.) byte-identical
via `json.RawMessage` round-trip.

Run `thrum roles refresh` after a Thrum upgrade to re-render templates from
saved answers without re-running the interactive skill. See
[Role-Based Preamble Templates](role-templates.md) for the full workflow.

## What's NOT in config.json

These remain separate for good reasons:

- **Identity files** (`.thrum/identities/*.json`) — per-agent config, one file
  per registered agent
- **Context files** (`.thrum/context/*.md`) — volatile session state
- **Runtime templates** — generated config files for your AI runtime (CLAUDE.md,
  .cursorrules, etc.)

Note: `daemon`, `backup`, and `runtime` settings _are_ all stored in
`config.json`. The items above are intentionally kept as separate files because
they are per-agent or volatile state, not global repository settings.

## Monitor Jobs

Monitor jobs watch long-running processes and emit matches as synthetic Thrum
messages. Configuration lives in the monitor state file managed by
`thrum monitor start/list/show/stop/logs/restart` — not in `config.json`.

Key behavior:

- **Debounce:** leading-edge, default 60s, minimum 30s. First match fires
  immediately; subsequent matches within the window are suppressed.
- **Persistence:** monitors survive daemon restarts automatically.
- **Scope:** local-socket-only. Monitors don't sync to remote peers.

See [Monitor Jobs](monitor-jobs.md) for the full command reference and
configuration options.

## Next Steps

- [Single-Agent Mode](single-agent-mode.md) — how `single_agent_mode` changes
  daemon behavior and when to disable it
- [Daemon Architecture](daemon.md) — how the daemon reads config.json and
  applies the settings at startup
- [Sync Protocol](sync.md) — the sync loop behavior controlled by `local_only`
  and `sync_interval`
- [Identity System](identity.md) — the identity files that live alongside
  config.json in `.thrum/`
- [CLI Reference](cli.md) — `thrum config show`, `thrum peer`, and other
  configuration-related commands
