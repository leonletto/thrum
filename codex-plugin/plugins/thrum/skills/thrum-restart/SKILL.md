---
name: thrum-restart
description:
  Save a conversation snapshot and prepare for session restart. Use when you
  need a fresh session due to context exhaustion, rate limits, or stuck state.
# source: claude-plugin/commands/restart.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Restart

Use this skill when the user explicitly wants the `restart` Thrum workflow.
Prefer the umbrella `thrum` skill when the request spans multiple commands or
needs broader coordination judgment.

## Session Restart

Compose a prose continuation, write it directly to your restart file, then
orchestrate the handoff. The standard restart uses the compact 11-section
structure; for designer/architect-grade handoffs with wire contracts +
capability matrix + design rationale, use `$thrum-restart-extended` instead.

### Steps

#### 1. Resolve identity and your worktree (run BEFORE reading the partial)

```bash
# $REPO must be YOUR worktree — the directory `thrum prime` reads the restart
# snapshot back from. Resolve it from the daemon's authoritative identity, NOT
# `git rev-parse` (which keys off the current shell CWD and would write to the
# wrong .thrum/restart/ if a bash step left your worktree). Fall back to git
# only if whoami can't answer.
REPO=$(thrum whoami --field worktree 2>/dev/null)
[ -n "$REPO" ] || REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: cannot resolve your worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"
```

(This duplicates Step 1 of the shared partial verbatim — necessary because the
partial's path depends on `$REPO`, so identity must resolve first. The Read tool
requires an absolute path.)

#### 2. Read the shared snapshot-composition partial

Read the partial at the absolute path:

```text
${REPO}/claude-plugin/commands/_snapshot-protocol.md
```

It carries the CRITICAL DISCIPLINE block, the standard 11-section structure, and
(for `$thrum-restart-extended`) the extended 16-section structure. Apply its
Step 2 (compose your continuation) per the structure guidance.

**Use the STANDARD 11-section structure.** Do NOT use the extended 16-section
structure — that lives in `$thrum-restart-extended`.

#### 3. Write the continuation

Per Step 3 of the partial, use the Write tool to save your composed continuation
to `${REPO}/.thrum/restart/${AGENT}.md`.

#### 4. Check session type and your role

```bash
thrum whoami --field tmux_session
thrum whoami --field role
```

#### 5. Orchestrate the handoff

**If your role is `coordinator`** — there is no senior agent to perform the
restart. Print these instructions for the operator and stop:

> Restart snapshot saved at `.thrum/restart/${AGENT}.md`. To restart me, run
> from another pane:
>
> ```bash
> thrum tmux restart <session-name> --force
> ```

**Else if `tmux_session` is non-empty (you are in tmux)** — notify the
coordinator:

```bash
thrum send "Restart snapshot saved. Please run: thrum tmux restart <session-name> --force" --to @coordinator_main
```

Then wait up to 5 minutes for the coordinator to restart you. Do not exit on
your own. If no restart occurs within 5 minutes, fall back to the non-tmux
instructions below.

**Else (no tmux session)** — print these instructions for the operator:

> Restart snapshot saved at `.thrum/restart/${AGENT}.md`. To continue in a new
> session:
>
> 1. Exit this session
> 2. Start a new session in the same directory
> 3. The snapshot will be auto-loaded by `thrum prime`

### When to Use

- Context window is getting full (you're seeing compaction warnings)
- You've hit rate limits and need to wait
- Your session feels stuck or unproductive
- The operator or coordinator has asked you to restart
- For designer-grade handoffs needing wire contracts + capability matrix +
  design rationale: use `$thrum-restart-extended` instead

### After Restart: Session Archive

After restart, your snapshot doesn't disappear — it moves to
`.thrum/agents/<your-agent-id>/sessions/` and stays there as a persistent log
entry. Browse the archive with:

```bash
thrum agent sessions list                    # default: this agent
thrum agent sessions list --verbose          # full §1 bodies inline
thrum agent sessions list --json             # NDJSON for scripts
thrum agent sessions list --all              # every agent, grouped
```

Permissions are user-only (`0600` for each snapshot file, `0700` for the
sessions folder). Operators on multi-user machines must copy explicitly to share
archives with another account.

Worktree-resident ephemeral agents archive into the worktree's own
`.thrum/agents/<id>/sessions/` — so the archive vanishes when the worktree is
removed. **By design.** If an ephemeral agent's history matters across the
worktree teardown, export those snapshots manually before `git worktree remove`.

The next session's `thrum prime` output includes a "Past Sessions" discovery
hint summarizing the most recent archive's §1 — so future-you gets a one-line
reminder of last session's headline without re-reading the full snapshot. That
hint is why §1 above is required.
