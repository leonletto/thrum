# Thrum Cursor Plugin

Thrum for Cursor packages commands, skills, rules, agents, hooks, and helper
scripts for durable multi-agent coordination.

This package is a **local developer preview**: installable and testable on your
machine, but not marketplace-polished. Expect rough edges and behavior that may
change.

## What is included

- Slash commands under `commands/`
- Agent skills under `skills/`
- Session rules under `rules/`
- Agent definitions under `agents/`
- Hooks in `hooks/hooks.json` (parity with Claude Code hooks is **incomplete**;
  some behaviors do not map 1:1 to Cursor)
- Scripts under `scripts/` (assert versions, sync from Claude plugin, etc.)

## Prerequisites

- `thrum` installed and on `PATH`
- `thrum init` already run in the repository

## Local install

1. Copy or symlink this `cursor-plugin` folder to
   `~/.cursor/plugins/local/thrum/` (so Cursor loads it as the `thrum` local
   plugin).
2. Restart Cursor after updates so the plugin reloads.

## Notes

- Treat this as **experimental**; validate behavior in your workflow before
  relying on it.
- Hook and automation parity with the Claude plugin is incomplete; use explicit
  `thrum` commands where automation is missing.
