# Thrum Cursor Plugin

Thrum for Cursor packages commands, skills, rules, and agents for durable multi-agent coordination.

This folder is currently a scaffold-in-progress for the Cursor plugin package. Do not install it yet; the remaining plugin assets still need to be added in later tasks.

## Prerequisites

- `thrum` installed and on `PATH`
- `thrum init` already run in the repository

## Development install

- When the remaining plugin assets are added in later tasks, copy or symlink this folder into `~/.cursor/plugins/local/thrum/`.
- Restart Cursor after updates so the local plugin is reloaded.
- Until those assets exist, treat this package as scaffold-only and not yet installable.

## Notes

- Some Claude hook behaviors may not map 1:1 to Cursor.
- Later tasks will add the real rules, commands, skills, and agents for Cursor support where hooks do not map cleanly.
