---
name: thrum-restart-extended
description: Save a comprehensive 16-section restart snapshot and prepare for session restart. Use for designer/architect-grade handoffs needing wire contracts, capability matrix, anticipated Q&A, and design rationale that the standard /thrum:restart compact format cannot carry.
# source: claude-plugin/commands/restart-extended.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Restart Extended

Use this skill when the user explicitly wants the `restart-extended` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


## Session Restart — Extended (16-section snapshot)

Compose a comprehensive 16-section prose continuation, write it directly to your restart file, then orchestrate the handoff. The extended structure is fundamentally different from standard: it preserves wire contracts (types + signatures + file:line cites), capability matrices (per-surface row-by-row tables), anticipated implementer Q&A, design inventory (entanglement classes / pattern catalogue), and locked decision rationale. Use this variant when the next session may be cold and must recover the full design grammar from this artifact alone.

### When to use extended vs standard

- **Use `/thrum:restart` (standard)** for routine context-exhaustion or rate-limit restarts where the work is well-trafficked and future-you can reconstruct from project state + recent inbox + a compact 11-section snapshot.
- **Use `/thrum:restart-extended` (this variant)** for designer/architect-grade handoffs: locking a complex brainstorm with multiple Leon-decided forks, handing off a fanout implementation (≥3 call sites or ≥2 epics), or any session where the next session may be a fresh restart and must recover wire-contract precision without re-reading the source files.

### Steps

#### 1. Resolve identity and repo root (run BEFORE reading the partial)

```bash
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"
```

(Duplicates the partial's Step 1 verbatim — necessary because the partial's path depends on `$REPO`.)

#### 2. Read the shared snapshot-composition partial

Read the partial at the absolute path:

```text
${REPO}/claude-plugin/commands/_snapshot-protocol.md
```

It carries the CRITICAL DISCIPLINE block and BOTH the standard 11-section structure and the EXTENDED 16-section structure with per-section guidance.

**Use the EXTENDED 16-section structure.** The structure block is `§1.` through `§16.` with the per-section guidance documented in the partial.

#### 3. Write the continuation

Per Step 3 of the partial, use the Write tool to save your composed continuation to `${REPO}/.thrum/restart/${AGENT}.md`. `thrum prime` auto-injects this file on next session start regardless of whether wake comes from `thrum tmux restart` or operator-initiated `thrum tmux create`.

#### 4. Check session type and your role

```bash
thrum whoami --field tmux_session
thrum whoami --field role
```

#### 5. Orchestrate the handoff

**If your role is `coordinator`** — there is no senior agent to perform the restart. Print these instructions for the operator and stop:

> Restart snapshot saved at `.thrum/restart/${AGENT}.md`. To restart me, run from another pane:
>
> ```bash
> thrum tmux restart <session-name> --force
> ```

**Else if `tmux_session` is non-empty (you are in tmux)** — notify the coordinator:

```bash
thrum send "Restart snapshot saved (extended). Please run: thrum tmux restart <session-name> --force" --to @coordinator_main
```

Then wait up to 5 minutes for the coordinator to restart you. Do not exit on your own. If no restart occurs within 5 minutes, fall back to the non-tmux instructions below.

**Else (no tmux session)** — print these instructions for the operator:

> Restart snapshot saved at `.thrum/restart/${AGENT}.md`. To continue in a new session:
>
> 1. Exit this session
> 2. Start a new session in the same directory
> 3. The snapshot will be auto-loaded by `thrum prime`

### After Restart: Session Archive

After restart, your snapshot moves to `.thrum/agents/<your-agent-id>/sessions/` and stays there as a persistent log entry. Same mechanism as standard restart — see `/thrum:restart` for archive browsing commands.
