---
name: thrum-ops
description: Use when the user asks to run or troubleshoot Thrum operational flows such as quickstart, inbox triage, wait loops, context save/load, daemon status, and sync/session health checks.
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
thrum wait --timeout 120
thrum context show
thrum sync status
thrum daemon status
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
