# Thrum Backup/Restore/Recover Design

**Date:** 2026-02-27
**Status:** Approved

## Overview

Add a `thrum backup` command that snapshots all thrum data (JSONL event logs,
local-only SQLite tables, config, identities, context) plus optional third-party
plugin data (beads, etc.) to a portable JSONL-based backup with automatic
rotation and retention.

## Commands

```
thrum backup [--dir <path>]        # Snapshot all data to backup dir
thrum backup status                # Show last backup, rotation stats
thrum backup restore [<archive>]   # Restore from backup (latest or specific)
thrum backup config                # Show effective backup config
```

## Data Sources

### JSONL Event Logs (source of truth)

Copied from the `a-sync` git worktree at `<git-common-dir>/thrum-sync/a-sync/`:
- `events.jsonl` — agent lifecycle events
- `messages/*.jsonl` — per-agent message events

### Local-Only SQLite Tables

Exported from `.thrum/var/messages.db` as JSONL:
- `message_reads` — read receipts (local-only, not synced)
- `subscriptions` — notification subscriptions
- `sync_checkpoints` — per-peer sync state

### Config & Identity Files

Direct file copies:
- `.thrum/config.json`
- `.thrum/identities/*.json`
- `.thrum/context/*.md`

### Third-Party Plugins

Configured in `.thrum/config.json`. Each plugin defines:
- A command to run (e.g., `bd backup --force`)
- A glob pattern for files to include (e.g., `.beads/backup/*`)

## Directory Layout

```
<backup-dir>/
  <repo-name>/
    current/                       # Latest uncompressed backup
      events.jsonl
      messages/
        agent1.jsonl
        agent2.jsonl
      local/
        message_reads.jsonl
        subscriptions.jsonl
        sync_checkpoints.jsonl
      config/
        config.json
        identities/
          coordinator_main.json
        context/
          coordinator_main.md
      plugins/
        beads/
          issues.jsonl
          dependencies.jsonl
          config.jsonl
          comments.jsonl
          labels.jsonl
          backup_state.json
      manifest.json                # Backup metadata
    archives/
      2026-02-27T124500.zip        # Rotated compressed backups
      pre-restore-2026-02-28T091500.zip  # Safety backups before restore
```

### Manifest Format

```json
{
  "version": 1,
  "timestamp": "2026-02-27T12:45:00Z",
  "thrum_version": "0.5.0",
  "repo_name": "thrum",
  "counts": {
    "events": 42,
    "message_files": 5,
    "local_tables": 3,
    "config_files": 4,
    "plugins": ["beads"]
  }
}
```

## Retention Policy (Grandfather-Father-Son)

After each backup, the previous `current/` is compressed into a timestamped
zip in `archives/`. Then rotation applies:

- **Daily:** Keep last 5 days of archives
- **Weekly:** Keep 4 weekly archives (oldest surviving daily per week)
- **Monthly:** Keep forever (oldest surviving weekly per month)

Defaults are configurable. Implemented in pure Go using `archive/zip`.

Pre-restore safety backups (`pre-restore-*.zip`) are exempt from rotation.

## Config

Added to `.thrum/config.json`:

```json
{
  "backup": {
    "dir": "",
    "retention": {
      "daily": 5,
      "weekly": 4,
      "monthly": -1
    },
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

- `dir`: Empty string = default `.thrum/backup`. When set, all future backups
  go to this directory. CLI `--dir` overrides config but does not persist.
- `retention.monthly`: `-1` means keep forever.
- `plugins`: Array of third-party backup sources. Each runs its command, then
  copies matching files into `plugins/<name>/` in the backup.

### Repo Name Derivation

The `<repo-name>` subfolder is derived from:
1. Git remote origin URL (e.g., `leonletto/thrum` → `thrum`)
2. Fallback: directory name of the repo root

This enables a single backup directory to hold backups from multiple repos.

## Backup Flow

1. Resolve backup directory (CLI flag > config > default `.thrum/backup`)
2. Derive repo name for subfolder
3. If `current/` exists, compress to timestamped zip in `archives/`
4. Copy JSONL event logs from `a-sync` worktree
5. Export local-only SQLite tables as JSONL
6. Copy config, identity, and context files
7. Run plugin commands and collect output files
8. Write `manifest.json`
9. Run retention/rotation on `archives/`

## Restore Flow

```
thrum backup restore [archive.zip]
```

1. **Safety backup:** If any existing thrum data is present, create a
   `pre-restore-<timestamp>.zip` safety backup before touching anything.
   This backup is exempt from rotation and lives in `archives/`.
2. Stop daemon if running
3. Extract archive (or use `current/` if no archive specified)
4. Copy JSONL files back to `a-sync` worktree
5. Import local-only JSONL tables into SQLite
6. Restore config/identity/context files
7. Rebuild SQLite projection from restored JSONL
8. Restart daemon
9. Run plugin restore commands if configured

## Implementation Notes

- Use `archive/zip` from Go stdlib — no external dependencies
- SQLite export uses the existing `safedb.DB` wrapper for safe access
- Repo name resolution reuses `gitctx` package
- Plugin commands run with the repo root as CWD, with a 60-second timeout
- Backup is non-destructive: never modifies source data
- Restore is destructive: always creates a safety backup first
- All file writes use atomic temp-file-then-rename pattern

## Testing Strategy

- Unit tests for retention/rotation logic (time-based, edge cases)
- Unit tests for JSONL export/import round-trip
- Unit tests for manifest generation and validation
- Integration tests: backup → corrupt data → restore → verify
- Integration tests: plugin command execution and file collection
