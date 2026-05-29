---
description:
  Park this agent for operator-initiated wake with a comprehensive 16-section
  snapshot (does NOT signal coordinator) then thrum-tmux-kills own session. Use
  for designer/architect-grade work where wake may be cold and must recover wire
  contracts + capability matrix + design rationale.
---

# Sleep — Extended (16-section snapshot)

Compose a comprehensive 16-section prose continuation, write it directly to your
restart file, then end session cleanly and kill own tmux session. The agent goes
to sleep until the operator wakes it later. Same termination semantics as
`/thrum:sleep`; the only difference is snapshot grade.

## When to use extended vs standard

- **Use `/thrum:sleep` (standard)** for routine park-and-resume where future-you
  can reconstruct from project state + a compact 11-section snapshot.
- **Use `/thrum:sleep-extended` (this variant)** for designer/architect-grade
  work: parking a complex brainstorm with multiple Leon-decided forks, parking a
  fanout implementation (≥3 call sites or ≥2 epics), or any sleep where the next
  wake may be a fresh restart and must recover wire-contract precision without
  re-reading the source files.

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
  echo "ERROR: the sleep-extended command requires a tmux-managed agent session (tmux_session field is empty)."
  echo "Use the restart-extended command for non-tmux sessions."
  exit 1
fi
```

If `tmux_session` is empty: ABORT before writing any snapshot. No status change,
no session end. Exit code 1.

### 2. Read the shared snapshot-composition partial

Read the partial at the absolute path:

```text
${REPO}/claude-plugin/commands/_snapshot-protocol.md
```

Apply its Step 2 (compose your continuation) per the structure guidance.

**Use the EXTENDED 16-section structure.** The structure block is `§1.` through
`§16.` with per-section guidance documented in the partial.

**Note on §3 framing:** For sleep snapshots, §3 frames as "where work stands at
park time" rather than "what shipped" — the agent is parking, not completing.
Composition discipline (1–3 sentences, specific, load-bearing-first) is
identical to restart-extended.

### 3. Write the continuation

Per Step 3 of the partial, use the Write tool to save your composed continuation
to `${REPO}/.thrum/restart/${AGENT}.md`. On next boot, `thrum prime`
auto-injects this file — same mechanism as restart wake.

### 4. Mark agent operational status idle

```bash
thrum agent set-status idle
# NOTE: idle-status write becomes observable after thrum-9neg lands.
```

Per thrum-9neg agent-status-wiring verdict, `idle` is the signal that this agent
has parked. NO new "sleeping" state was added — `idle` covers both "no active
work" and "parked for operator wake."

If `thrum agent set-status` returns an error, continue to Step 5 — the snapshot
on disk is the load-bearing artifact.

### 5. End session cleanly

```bash
thrum session end
```

This emits `agent.session.end` cleanly so the dead-agent sweeper does NOT treat
the disconnect as a crash.

### 6. Kill own tmux session

```bash
thrum tmux kill "$SESSION"
```

Tmux pane terminates → runtime process exits → no further activity until
operator wakes the agent.

## How wake works

The operator brings you back via `thrum tmux create <session-name>`. On runtime
start, `thrum prime` auto-injects the snapshot — same mechanism as restart.
Resume from §16 immediate-next-actions.

The snapshot file moves to `.thrum/agents/<your-agent-id>/sessions/` archive on
wake (same as restart).
