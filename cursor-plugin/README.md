# Thrum Cursor Plugin

Thrum multi-agent coordination plugin for Cursor Agent.

## Prerequisites

- [Thrum CLI](https://leonletto.github.io/thrum) installed and on PATH
- Cursor Agent with hooks support

## Install

Run from any git repo where you want Thrum coordination:

```bash
/path/to/thrum/cursor-plugin/local-install.sh
```

Or specify a target directory:

```bash
cursor-plugin/local-install.sh --target /path/to/project
```

This deploys into `.cursor/` with:
- **rules/** — `.mdc` files for sync worktree safety and session lifecycle
- **hooks.json** — session start, shell guard, stop check, compact hooks
- **mcp.json** — Thrum MCP server configuration
- **skills/** — Thrum skills (populated by `sync-skills.sh`)
- **commands/** — Thrum commands (populated by `sync-skills.sh`)

## Updating

After upstream changes, re-sync and re-install:

```bash
scripts/sync-skills.sh    # sync skills/commands from claude-plugin
cursor-plugin/local-install.sh  # re-deploy to .cursor/
```

## What's Included

| Component | Description |
|-----------|-------------|
| `rules/thrum-safety.mdc` | Blocks writes to internal sync worktree |
| `rules/thrum-session.mdc` | Session lifecycle reminders |
| `hooks/hooks.json` | Hook template (paths resolved at install) |
| `scripts/*.sh` | Hook scripts (shell guard, stop check, compact) |
| `local-install.sh` | Installer that deploys to `.cursor/` |
