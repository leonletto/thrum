---
name: thrum-ops
description: Use when the user asks to run or troubleshoot Thrum operational flows such as quickstart, inbox triage, wait loops, context save/load, daemon status, and sync/session health checks.
# source: claude-plugin/skills/thrum/SKILL.md (condensed for codex)
# last-synced: 2026-03-01
---

# Thrum Ops

Use this skill for operational command execution and session lifecycle management.

## Use this when
- Starting a new agent session (`quickstart`, `session start`, intent setup).
- Running inbox/overview triage loops.
- Handling context persistence before or after compaction.
- Checking daemon/sync/session health.

## Operational loop
1. Bootstrap identity/session (`thrum quickstart ...` or `thrum session start`).
2. Triage with `thrum overview` and `thrum inbox --unread`.
3. Act with `send`, `reply`, `group`, `team` as needed.
4. Persist context using update/load context flow.
5. Verify daemon and sync health before handoff.

## Command baseline
```bash
thrum quickstart --role <role> --module <module> --intent "<intent>"
thrum overview
thrum inbox --unread
thrum wait --timeout 120s
thrum context show
thrum sync status
thrum daemon status
```

## Sessions & Context

```bash
thrum session start                      Start session
thrum session end                        End session
thrum session set-intent "..."           Update work description
thrum context prime                      Same as thrum prime
thrum context show                       Show saved work context
thrum context save --file <path>         Save context from file
thrum overview                           Combined status + team + inbox
```

**Context workflow:**
- Before compaction: save via update-context flow
- After compaction: run load-context to restore work state
- `thrum prime` gathers identity, team, inbox, git context, sync health

## Daemon & Sync

```bash
thrum daemon start                       Start daemon
thrum daemon stop                        Stop daemon
thrum daemon status                      Daemon health
thrum sync force                         Force immediate sync
thrum sync status                        Sync state
```

## Quickstart Details

Common roles: `coordinator`, `implementer`, `planner`, `reviewer`, `tester`.

```bash
# Full registration
thrum quickstart --role implementer --module <branch> --intent "Implementing <epic>"

# Just update intent
thrum session set-intent "Now working on X"

# Heartbeat (keep visible)
thrum agent heartbeat
```

## References
- `references/CLI_REFERENCE.md`
- `references/quickstart.md`
- `references/overview.md`
- `references/inbox.md`
- `references/send.md`
- `references/reply.md`
- `references/group.md`
- `references/team.md`
- `references/wait.md`
- `references/prime.md`
- `references/update-context.md`
- `references/load-context.md`
