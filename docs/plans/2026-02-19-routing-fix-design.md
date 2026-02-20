# SendMessage Routing Fix Design

**Date:** 2026-02-19
**Status:** Approved
**Scope:** MCP routing parity, recipient validation, delivery feedback, reply
routing, waiter subscriptions, `--all` removal, name-only routing, role groups

## Problem Statement

Thrum's message routing has multiple failure modes that cause messages to be
silently lost or delivered to wrong recipients:

1. **MCP `check_messages` uses a narrow filter** — only matches `MentionRole`
   (role string), missing name-directed messages, broadcasts, and group
   messages. The CLI uses the comprehensive `buildForAgentClause` which works
   correctly.

2. **MCP send omits `CallerAgentID`** — messages are attributed to the daemon's
   config identity instead of the actual sender, breaking multi-worktree setups.

3. **MCP mark-read uses wrong identity** — read-state is recorded under the
   daemon's identity, not the MCP client's.

4. **Replies don't route back to sender** — reply copies the parent's role-based
   audience but never adds the original sender as a recipient.

5. **Reply group scope reconstruction is malformed** — produces `"@group:reviewers"`
   instead of `"@reviewers"`, creating dead mention refs.

6. **No recipient existence validation** — sending to `@nonexistent` silently
   succeeds. The message is stored but never delivered. No error or warning.

7. **Always returns "delivered" status** — MCP hardcodes `status: "delivered"`
   and `recipient_status: "unknown"` regardless of actual routing.

8. **MCP waiter doesn't wake for broadcasts/groups** — WebSocket subscription
   only covers mention-role matches, not group scopes.

9. **`thrum wait --all` is a footgun** — the flag is a no-op for filtering
   (never passes `for_agent`/`for_agent_role`), so all agents see all messages.

10. **Role-based routing is implicit and ambiguous** — `@implementer` fans out
    to all agents with that role via invisible inbox matching. Stale agents from
    dead worktrees still receive messages. Groups already solve multi-agent
    addressing explicitly.

## Design

### 1. MCP Routing Parity

**Fix `check_messages`** to use `ForAgent`/`ForAgentRole` instead of
`MentionRole`. This triggers `buildForAgentClause` — the same 3-part OR the CLI
uses (mentions + group membership + legacy broadcasts).

**Fix `CallerAgentID`** on all MCP paths: send, broadcast, check_messages, and
mark-read. The MCP server already resolves `s.agentID` at startup — inject it
into every RPC request.

**Fix self-exclusion** — add `ExcludeSelf: true` to `check_messages`, matching
CLI behavior.

### 2. Reply Routing

**Add original sender to reply audience.** When replying, extract
`parent.Author.AgentID` and add it to the reply's mentions (deduplicated,
excluding self).

**Fix group scope reconstruction.** Change `"@"+scope.Type+":"+scope.Value` to
`"@"+scope.Value` so `IsGroup()` can find the group.

### 3. Recipient Validation and Delivery Feedback

**Hard error on unknown recipients.** In `HandleSend`, after the `IsGroup`
check, validate each non-group mention against the agents table:

```
IsGroup(mention)?     → group scope (existing behavior)
agents.agent_id = ?   → valid agent name
agents.role = ?       → valid role (now a group, see §6)
none matched          → hard error, message NOT stored
```

The error lists all unknown recipients so the sender can fix and resend:
`"unknown recipients: @nonexistent — no matching agent, role, or group found"`

**Updated `SendResponse`** adds `ResolvedTo int` (count of resolved mentions)
and `Warnings []string` (informational, e.g., "role has 4 agents").

**MCP status** is derived from validation: `"delivered"` when all mentions
resolve. Unknown recipients cause an RPC error (not a status field) since the
message is rejected.

### 4. Waiter Group Subscription

Add a second `subscribe` call in `waiter.setup()` for
`scope={type:"group", value:"everyone"}`. The subscription infrastructure
already supports scope subscriptions — the waiter just never registered one.
This ensures broadcasts and `@everyone` messages trigger WebSocket push.

### 5. Remove `--all` from `thrum wait`

**Remove the flag entirely.** It's currently a no-op for filtering — `waitCmd`
never passes `for_agent`/`for_agent_role`. All agents see all messages.

**Always filter by agent identity.** `waitCmd` resolves the local agent ID and
role, passes them as `ForAgent`/`ForAgentRole` in `WaitOptions`.

**Default `afterTime` to "now"** unconditionally when `--after` is not specified
(previously this only happened with `--all`).

**Update ~20 files** that reference `wait --all` in templates, docs, CLAUDE.md,
and plugin files.

### 6. Name-Only Routing with Auto Role Groups

**Remove role from inbox matching.** `buildForAgentValues` returns only
`[agentID]`, not `[agentID, agentRole]`. The role string is no longer a routing
target in the mention filter (Part 1 of `buildForAgentClause`).

**Auto-create role groups on registration.** When an agent registers with
role=implementer, auto-create a group named `"implementer"` with member
`{type:"role", value:"implementer"}`. Now `@implementer` resolves as a group
send through the existing group system. The group is visible in
`thrum group list` and manageable via `thrum group member add/remove`.

**Enforce name≠role.** Three validation checks prevent addressing ambiguity:

| Check | Example | Error |
|-------|---------|-------|
| Name = own role | `name="implementer" role="implementer"` | "agent name cannot be the same as its role" |
| Name = existing role | `name="worker"` when role "worker" exists | "agent name conflicts with existing role" |
| Role = existing name | `role="alice"` when agent "alice" exists | "role conflicts with existing agent name" |

**Group membership for role routing.** Part 2 of `buildForAgentClause` (group
membership subquery) still uses `forAgentRole` to check `group_members WHERE
member_type='role' AND member_value=?`. This is the correct path — role-based
fan-out now flows through the explicit group system.

### 7. Backwards Compatibility

- **Existing messages** with `ref_type='mention', ref_value='implementer'` (a
  role string) will only be delivered to an agent literally named "implementer"
  or through the auto-created role group. This is correct — ambiguously
  addressed messages should route through the group system.
- **`parseMention`** (renamed from `parseMentionRole`) preserves agent names
  through the send path. No behavioral change — names were already passed
  through.
- **`ForAgentRole`** is still passed in RPC requests and used for group
  membership queries (Part 2). Only the direct mention matching (Part 1) loses
  the role.
- **Daemon RPC** gains `Warnings` and `ResolvedTo` fields on `SendResponse`
  (additive, non-breaking for existing callers that ignore unknown fields).

## Routing Model After Fix

| Address | Resolves To | Mechanism |
|---------|-------------|-----------|
| `@impl_api` | Exactly one agent | Name lookup (agent_id match in mention refs) |
| `@implementer` | All agents with role=implementer | Group (auto-created, `member_type='role'`) |
| `@everyone` | All agents | Group (built-in, `member_value='*'`) |
| `@backend-team` | Explicit members | Group (user-created) |
| `@nonexistent` | Error | Recipient validation rejects with guidance |

## Files Affected

### Modified files
- `internal/mcp/tools.go` — parseMention rename, CallerAgentID on send/broadcast/check/mark-read, ForAgent/ForAgentRole, ExcludeSelf, delivery status
- `internal/mcp/tools_test.go` — parseMention tests
- `internal/mcp/types.go` — SendMessageOutput gains ResolvedTo/Warnings, drops RecipientStatus
- `internal/mcp/waiter.go` — CallerAgentID on mark-read, @everyone scope subscription
- `internal/cli/message.go` — reply includes sender, fix group scope reconstruction
- `internal/cli/message_test.go` — reply tests
- `internal/cli/send.go` — SendResult gains ResolvedTo/Warnings
- `internal/cli/wait.go` — remove All, add ForAgent/ForAgentRole
- `internal/cli/wait_test.go` — update tests
- `internal/cli/prime.go` — remove --all from generated command
- `internal/cli/inbox.go` — ForAgentRole usage
- `internal/context/context.go` — remove --all
- `internal/daemon/rpc/message.go` — recipient validation in HandleSend, SendResponse fields, buildForAgentValues name-only
- `internal/daemon/rpc/agent.go` — auto role group creation, name≠role validation
- `internal/daemon/rpc/agent_test.go` — registration tests
- `cmd/thrum/main.go` — send display warnings, wait remove --all + add identity
- `CLAUDE.md` — message-listener pattern
- `internal/cli/templates/` (5 files) — remove --all
- `website/docs/` (5 files) — remove --all
- `claude-plugin/` (3 files) — remove --all

### New files
- `internal/mcp/routing_test.go` — MCP routing parity integration test

## Non-Goals

- Changing the daemon protocol version
- Changing the a-sync branch format or JSONL storage
- Adding message TTL or automatic expiration
- Thread auto-linking (thread_id vs reply_to remain independent)
- Active-session-gated routing (agents between sessions should still see
  messages when they return)
