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
    "single_agent_mode": true
  },
  "peers": {
    "auto_connect": true,
    "pairing_code_length": 16
  },
  "restart": {
    "max_lines": 1000,
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

Maximum lines in a restart snapshot.

- **Type:** integer
- **Default:** `1000`

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

Example config with restart enabled:

```json
{
  "restart": {
    "max_lines": 1000,
    "auto_threshold": 80,
    "graceful_timeout": 30
  }
}
```

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

`THRUM_HOME` pins all thrum commands to the specified repo path regardless of
the current working directory. This is set automatically by `thrum-startup.sh`
to prevent identity drift when an agent `cd`s into a different worktree.

`THRUM_AGENT_ID` pins the caller identity for daemon RPC calls, bypassing
identity file lookup. When set, commands like `thrum status`, `thrum prime`, and
`thrum overview` use this agent ID directly.

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
Runtime templates are created during initialization and are not tracked in
`config.json`.

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
