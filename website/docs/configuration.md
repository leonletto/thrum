---
title: "Configuration"
description:
  "Thrum configuration model — config.json schema, runtime tiers, priority
  chain, and the config show command"
category: "guides"
order: 3
tags: ["configuration", "config", "runtime", "daemon", "settings", "config-show"]
last_updated: "2026-02-12"
---

# Configuration

Thrum uses `.thrum/config.json` as the single source of truth for user
preferences. Everything works with sensible defaults — you only need to
edit config.json when you want to change something.

## Config File

Located at `.thrum/config.json` in your repository:

```json
{
  "runtime": {
    "primary": "claude"
  },
  "daemon": {
    "local_only": true,
    "sync_interval": 60,
    "ws_port": "auto"
  }
}
```

This file is created during `thrum init` and can be edited at any time.

## Schema Reference

### `runtime.primary`

Which AI coding runtime to generate configs for.

- **Type:** string
- **Default:** auto-detected or `"cli-only"`
- **Values:** `"claude"`, `"codex"`, `"cursor"`, `"gemini"`, `"auggie"`, `"cli-only"`
- **Set by:** `thrum init` (interactive prompt or `--runtime` flag)

### `daemon.local_only`

Disable remote git sync (local-only mode).

- **Type:** boolean
- **Default:** `false`
- **Override:** `THRUM_LOCAL=true` environment variable

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

```
CLI flag  >  Environment variable  >  config.json  >  Default
```

Environment variables are intended for CI/automation overrides, not primary
configuration. For day-to-day use, edit `config.json`.

### Environment Variable Reference

| Variable | Overrides | Example |
|----------|-----------|---------|
| `THRUM_LOCAL` | `daemon.local_only` | `THRUM_LOCAL=true` |
| `THRUM_SYNC_INTERVAL` | `daemon.sync_interval` | `THRUM_SYNC_INTERVAL=120` |
| `THRUM_WS_PORT` | `daemon.ws_port` | `THRUM_WS_PORT=9999` |
| `THRUM_NAME` | Agent identity selection | `THRUM_NAME=alice` |
| `THRUM_ROLE` | Agent role | `THRUM_ROLE=planner` |
| `THRUM_MODULE` | Agent module | `THRUM_MODULE=backend` |

## Runtime Tiers

Thrum supports multiple AI coding runtimes at different levels of maturity:

| Tier | Runtimes | Support Level |
|------|----------|---------------|
| **Tier 1** (Fully Supported) | Claude Code, Augment | Tested in production, MCP native |
| **Tier 2** (Community Supported) | Cursor, Codex | Templates available, community-maintained |
| **Tier 3** (Experimental) | Gemini, Amp | Community templates, not guaranteed |

### Runtime Selection

During `thrum init`, Thrum detects installed runtimes and prompts you to
select one:

```
Detected AI runtimes:
  1. Claude Code    (found .claude/settings.json)
  2. Augment        (found .augment/)

Which is your primary runtime? [1]:
```

You can also specify directly: `thrum init --runtime claude`

To change your runtime after init, edit `.thrum/config.json` directly or
run `thrum init --runtime <name> --force`.

## Viewing Configuration

Use `thrum config show` to see the effective configuration and where each
value comes from:

```
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

- **Identity files** (`.thrum/identities/*.json`) — per-agent config, one
  file per registered agent
- **Context files** (`.thrum/context/*.md`) — volatile session state
- **Runtime templates** — generated config files for your AI runtime
  (CLAUDE.md, .cursorrules, etc.)
