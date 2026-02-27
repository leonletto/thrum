
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
    "ws_port": "auto"
  }
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

## Priority Chain

When the same setting can come from multiple sources, this order applies:

```text
CLI flag  >  Environment variable  >  config.json  >  Default
```

Environment variables are intended for CI/automation overrides, not primary
configuration. For day-to-day use, edit `config.json`.

### Environment Variable Reference

| Variable              | Overrides                | Example                   |
| --------------------- | ------------------------ | ------------------------- |
| `THRUM_LOCAL`         | `daemon.local_only`      | `THRUM_LOCAL=false`       |
| `THRUM_SYNC_INTERVAL` | `daemon.sync_interval`   | `THRUM_SYNC_INTERVAL=120` |
| `THRUM_WS_PORT`       | `daemon.ws_port`         | `THRUM_WS_PORT=9999`      |
| `THRUM_NAME`          | Agent identity selection | `THRUM_NAME=alice`        |
| `THRUM_ROLE`          | Agent role               | `THRUM_ROLE=planner`      |
| `THRUM_MODULE`        | Agent module             | `THRUM_MODULE=backend`    |

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
```
