---
description:
  Park this agent for operator-initiated wake. Saves a standard 11-section
  snapshot (does NOT signal coordinator) then thrum-tmux-kills own session.
  Use when the operator is shutting down (e.g. computer restart) and the
  agent should resume from snapshot on next boot.
---

# Sleep — Park Until Operator Wake

Compose a standard 11-section prose continuation, write it directly to your restart file, then end session cleanly and kill own tmux session. The agent goes to sleep until the operator wakes it later via `thrum tmux create <session-name>`. Unlike `/thrum:restart`, sleep does NOT signal the coordinator and does NOT wait for an external mover — it terminates its own tmux session.

## When to Use

- The operator is shutting down the machine (e.g. computer restart) and wants this agent's work durably parked.
- The operator wants to free a tmux session slot but resume this agent's work later.
- You (the agent) decide independently that further progress requires the operator's attention later, not the coordinator's now.

For routine context-exhaustion / rate-limit restarts where the coordinator should bring you back in-place, use `/thrum:restart` instead.

## Steps

### 1. Resolve identity + verify tmux session (Tier 1 pre-check; run BEFORE anything else)

```bash
# Resolve identity + repo root (needed before reading the partial):
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"

# Tier 1 pre-check: tmux session must exist BEFORE writing snapshot:
SESSION=$(thrum whoami --field tmux_session)
if [ -z "$SESSION" ]; then
  echo "ERROR: /thrum:sleep requires a tmux-managed agent session (tmux_session field is empty)."
  echo "Use /thrum:restart for non-tmux sessions."
  exit 1
fi
```

If `tmux_session` is empty: ABORT before writing any snapshot. No status change, no session end. Exit code 1. The skill is the wrong tool for non-tmux agents.

### 2. Read the shared snapshot-composition partial

Read the partial at the absolute path:

```text
${REPO}/claude-plugin/commands/_snapshot-protocol.md
```

Apply its Step 2 (compose your continuation) per the structure guidance.

**Use the STANDARD 11-section structure.** For comprehensive designer/architect-grade snapshots, use `/thrum:sleep-extended` instead.

**Note on §1 framing:** For sleep snapshots, the Big Picture section frames as "where work stands at park time" rather than "what shipped" — the agent is parking, not completing. Composition discipline (1–3 sentences, specific, load-bearing-first) is identical to restart.

### 3. Write the continuation

Per Step 3 of the partial, use the Write tool to save your composed continuation to `${REPO}/.thrum/restart/${AGENT}.md`. On next boot, `thrum prime` auto-injects this file — same mechanism as restart wake.

### 4. Mark agent operational status idle

```bash
thrum agent set-status idle
# NOTE: idle-status write becomes observable after thrum-9neg lands.
```

Per thrum-9neg agent-status-wiring verdict, `idle` is the signal that this agent has parked. NO new "sleeping" state was added — `idle` covers both "no active work" and "parked for operator wake."

If `thrum agent set-status` returns an error (e.g. rate-limited), continue to Step 5 — the snapshot on disk is the load-bearing artifact, not the status field. The operator can set status post-wake.

### 5. End session cleanly

```bash
thrum session end
```

This emits an `agent.session.end` event cleanly so the dead-agent sweeper does NOT later treat the disconnect as a crash. If `thrum session end` fails, continue to Step 6.

### 6. Kill own tmux session

```bash
thrum tmux kill "$SESSION"
```

Tmux pane terminates → runtime process exits → no further activity until operator wakes the agent via `thrum tmux create <session-name>`.

## How wake works

The operator brings you back later by running `thrum tmux create <session-name>` (or analogous). On runtime start, `thrum prime` auto-injects the snapshot at `.thrum/restart/<your-agent-id>.md` — same mechanism used by restart. Resume from §16 / §9 immediate-next-actions.

The snapshot file moves to `.thrum/agents/<your-agent-id>/sessions/` archive on wake (same as restart). Worst-case fallback: previous Claude session may be resumable via Claude Code's native session-continuation mechanism.

## Programmatic use (operator shutdown scripts)

The underlying mechanic — write snapshot + set status idle + session end + tmux kill — can be invoked from an operator's shutdown script directly via the bash commands above (without going through the skill). Out of scope for thrum-rwhg; document only.
