# Messaging Protocol

## Message Lifecycle

1. **Send** — `thrum send "msg" --to @name` (direct) or `--to @group` (group)
2. **Deliver** — Daemon writes to recipient's inbox (JSONL in
   `.git/thrum-sync/`)
3. **Receive** — `thrum inbox` or `thrum wait` (blocking)
4. **Read** — Auto-marked read when displayed via `thrum inbox`
5. **Reply** — `thrum reply <msg-id> "response"` (same audience)

## Addressing

- **Direct:** `--to @agent-name` — routes to named agent
- **Role fanout:** `--to @role` — routes via auto-created role group (all agents
  with that role); sender gets a warning — prefer direct @name
- **Group:** `--to @group-name` — all members of group
- **Broadcast:** `--to @everyone` — all agents
- **Reply:** `thrum reply <msg-id>` — same audience as original
- **Unknown recipient** — hard error; verify names with `thrum team`

## Context Management

### Session Initialization

`thrum prime` gathers all context in one call:

- Agent identity (name, role, module)
- Team (active agents and their intents)
- Inbox (unread messages with summaries)
- Git context (branch, uncommitted files)
- Daemon health and sync state

Plugin hooks auto-run `thrum prime` on **SessionStart** and **PreCompact**.

### After Compaction

Context auto-recovers via the PreCompact hook. The agent sees:

1. Their identity and session state
2. Any unread messages accumulated during compaction
3. Current team state
4. Quick command reference

### Identity Persistence

Agent identities are stored in `.thrum/identities/<name>.json` and persist
across sessions. Registration via `thrum quickstart` is idempotent —
re-registering with the same name updates the existing identity.

For multi-worktree setups, set `THRUM_NAME` env var to distinguish agents:

```bash
export THRUM_NAME=feature_agent
thrum quickstart --role impl --module feature --intent "Feature work"
```

## Unified Workflow: Thrum + Beads

```bash
# Find work → Beads
bd ready

# Claim and announce → Beads + Thrum
bd update bd-123 --status in_progress
thrum send "Starting bd-123" --to @lead

# Work, update → Thrum
thrum send "Progress: auth module complete" --to @lead

# Complete → Beads + Thrum
bd close bd-123 --reason "Done with tests"
thrum send "bd-123 done, ready for review" --to @reviewer
```
