---
name: recover-agent-state
description:
  Use after suspected scheduled-agent crash, when state.md may be partial /
  malformed / unparseable — validates structure BEFORE writing, preserves
  corrupt content to state.md.broken, and routes the daemon-side §6.5 corruption
  flow (sets the auto-respawn gate flag + pages the operator via the canonical
  Q3-D escalation).
allowed-tools: "Bash(thrum:*)"
---

# Thrum: Recover Agent State

This is the scheduled-agent recovery counterpart to `/thrum:update-agent-state`.
Run it at wake time when you suspect the prior session crashed mid-write — most
commonly when the wake-primer (Step 1 of `/thrum:prime-agent`) mentions an
unclean shutdown, or when `/thrum:update-agent-state` itself returns a "recovery
required" error.

## Why this exists

State.md has a strict 19-session sliding-window format (4 verbatim

- 3 blocks of 5). A crash mid-write can leave the file partially serialized —
  broken YAML-ish structure, missing sections, count violations. If
  `/thrum:update-agent-state` is invoked on top of that broken file, the parser
  rejects the input AND the writer would otherwise overwrite the partial data
  with a fresh state, destroying whatever was recoverable.

Spec §6.5 mandates: do NOT silently overwrite. Validate first; if the parse
fails, preserve the corrupt content + page the operator + block auto-respawn
until the operator clears the gate. This skill is one of the 5 escalation sites
in the B-B1 substrate (spec §8: idle-nudge exhaust, stage-failure 3-consecutive,
auto- respawn loop guard, state.md parse failure, nudge target offline).

## Step 1: Run the recover command

```bash
thrum agent state recover
```

The CLI:

1. Reads `.thrum/agents/<your-agent-id>/state.md` (no file → no-op, exit 0 with
   "nothing to recover").
2. Validates structure via the strict `agentstate.Parse` parser.
3. **Parses cleanly:** prints "state.md OK — no recovery needed" and exits 0.
   Continue with your session.
4. **Parse fails:**
   - Moves the corrupt file to `state.md.broken` (preserves content for operator
     review).
   - Calls the `agent.mark_state_corruption` RPC, which:
     - Sets `agents.state_md_parse_failed_at = now` (gates auto-respawn per spec
       §3.x).
     - Appends a `state_md_parse_failed` row to `agent_lifecycle_events` with
       `details.broken_path`.
     - Routes a Q3-D escalation to the operator via `escalation.RouteEscalation`
       with `Source: "b-b1.state_md_parse_failed"`.
   - Prints a status block telling the operator where the corrupt content
     lives + how to clear the flag.

The skill does NOT call `RouteEscalation` directly — the daemon owns the routing
decision (email vs. supervisor agent fallback per spec §8 Q3-D chain). The skill
just invokes the CLI, which calls the RPC, which routes the alert.

## Step 2: After recovery — auto-respawn is BLOCKED

The daemon will not auto-respawn this agent while the corruption flag is set.
The CLI's output names the operator-side clear command:

```bash
thrum agent ack-state-corruption <agent-id>
```

This is intentional safety: rapid crashes leaving partial state.md writes can
stack damage if auto-respawn keeps trying. The operator reviews the `.broken`
file, repairs `state.md` (or accepts the loss), then clears the gate.

DO NOT try to circumvent the flag by recreating state.md yourself during the
same session. The agent.register or auto-respawn flow will refuse to fire while
the flag is set; an in-session state.md write would parse OK but the gate
remains tripped until the operator clears it.

## When NOT to use this skill

- The session is already running cleanly (you just woke and `/thrum:prime-agent`
  succeeded with no errors). No prior-crash signal means no recovery needed;
  running this skill is a no-op but still costs a daemon RPC roundtrip.
- You're an operator-spawned agent (coordinator, on-demand task). This skill
  targets the scheduled-agent state.md; project state.md has its own recovery
  flow.
- The state.md is fine but you want to discard a stale entry — this skill does
  NOT support selective rollback. Use `/thrum:update-agent-state` to overwrite
  via the normal PromoteAndDrop flow, or manually edit the file (the parser
  accepts any structurally-valid input).

## Combined-skill usage example

A typical scheduled-agent wake that defensively checks for corruption before
doing anything else:

```bash
# Step 1 of /thrum:prime-agent — read the inbox literally.
thrum inbox --unread

# Defensive recovery check — no-op if state.md is clean.
thrum agent state recover

# Step 2 of /thrum:prime-agent — skill-library diff.
ls .claude/skills/ > /tmp/current_skills.txt
AGENT_NAME=$(thrum whoami --field agent_id)
diff ".thrum/agents/${AGENT_NAME}/last_seen_skills.txt" \
     /tmp/current_skills.txt || true
```

If `thrum agent state recover` reports the file as corrupt, the agent should
EXIT and let the operator intervene. Continuing to work on top of a corrupted
state would just create more work-in-progress that gets lost when the operator
manually repairs the state.
