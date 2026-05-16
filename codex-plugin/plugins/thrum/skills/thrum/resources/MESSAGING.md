# Messaging Protocol

## Message Lifecycle

1. **Send** — `thrum send "msg" --to @name` (direct)
2. **Deliver** — Daemon writes to recipient's inbox (JSONL in
   `.git/thrum-sync/`)
3. **Receive** — `thrum inbox` or `thrum wait` (blocking)
4. **Verify sent state** — `thrum sent` or `thrum message get <msg-id>`
5. **Read** — Auto-marked read when displayed via `thrum inbox`; use
   `thrum inbox --unread` to peek without marking. Explicit:
   `thrum message read --all`
6. **Reply** — `thrum reply <msg-id> "response"` (same audience)

## Addressing

| Target              | Routing                                                       | When to use                          |
| ------------------- | ------------------------------------------------------------- | ------------------------------------ |
| `--to @lead_agent`  | **Direct** — routes to the named agent                        | Default for all task messages        |
| `--to @coordinator` | **Role fanout** — ALL agents with that role (warning emitted) | Only when you want every coordinator |
| `--to @everyone`    | **Broadcast** — all registered agents                         | Critical alerts                      |

**Critical:** `@coordinator` is a role, not an agent name. Sending
`--to @coordinator` fans out to every agent registered with that role. Use
`thrum team` to find agent names, then send `--to @<name>` for direct messages.

- **Reply:** `thrum reply <msg-id>` — same audience as original
- **Unknown recipient** — hard error; verify names with `thrum team`

## Permission-Prompt Routing

Runtime permission prompts (e.g. "Allow this tool?") route to recipients listed
in `permission_supervisors` in `.thrum/config.json`. **The array is
authoritative** — the daemon sends the nudge to each entry in order, with no
auto-detect or broadcast fallback behind it.

```jsonc
{
  "permission_supervisors": [
    "coordinator", // role → fans out to every active coordinator
    "@coordinator_main", // specific agent (name-based)
    "@user:leon-letto", // user → auto-bridges to Telegram if configured
  ],
}
```

**Invariant:** the list must include at least one coordinator-role recipient —
either the bare role `"coordinator"` or an `@coordinator_*` agent name. When
absent or empty, the resolver defaults to `["coordinator"]`. If the list is
non-empty but has no coordinator entry, the daemon warns at start and continues
(prompts may still go undelivered if the listed agents are offline).

## Threading

Replies auto-create implicit threads. When you `thrum reply`, the daemon assigns
a shared `thread_id` to both parent and reply. No explicit thread creation
needed. The UI groups conversations by `thread_id`.

## Context Management

### Session Initialization

`thrum prime` gathers all context in one call:

- Agent identity (name, role, module)
- Team (active agents and their intents)
- Inbox (unread messages with summaries)
- Git context (branch, uncommitted files)
- Daemon health and sync state

The **SessionStart** hook prompts you to run `$thrum-prime`. The **PreCompact**
hook auto-saves context to a backup file.

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

For multi-worktree setups, see [WORKTREES.md](WORKTREES.md) for identity setup.

## Unified Workflow: Thrum + Beads

```bash
# Find work → Beads
bd ready

# Claim and announce → Beads + Thrum
bd update bd-123 --status in_progress
thrum send "Starting bd-123" --to @lead_agent

# Work, update → Thrum
thrum send "Progress: auth module complete" --to @lead_agent

# Complete → Beads + Thrum
bd close bd-123 --reason "Done with tests"
thrum send "bd-123 done, ready for review" --to @reviewer1
```
