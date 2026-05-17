---
description:
  Save a conversation snapshot and prepare for session restart. Use when you
  need a fresh session due to context exhaustion, rate limits, or stuck state.
---

# Session Restart

Compose a prose continuation, write it directly to your restart file, then
orchestrate the handoff.

## Steps

### 1. Resolve your identity and repo root

```bash
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"
```

### 2. Compose your continuation

Your context is high and we want to restart without losing the decisions we've
made. Write a rich continuation that future-you will read as the first action
after restart.

**§1 Big picture — REQUIRED FIRST SECTION (write this BEFORE anything else)**

Every restart snapshot MUST begin with a section using this exact heading:

```
## 1. Big picture — what shipped this session
```

Followed by 1-3 sentences (a single paragraph or up to 3 short ones)
summarizing what the session accomplished. Be specific: name the artifacts,
the decisions, the cycles closed. Examples:

> Locked the session-archive spec v2 with §1 Big picture requirement, five
> Q-Spec approvals, and Q-Spec-5 deferred to impl-time. Hand-off pending
> coordinator final review.

> Investigated rc.9 inbox-race against impl_inbox_race's hypothesis: confirmed
> the lock-substrate fence is the right fix. Filed thrum-XXX with 4 BLOCKING
> evidence points.

> Closed B-B1 E6.0 brainstormer-third pass. 2 BLOCKING + 5 IMPORTANT + 10
> MINOR. All three load-bearing traps PASSed. Standing by for E6.1 next batch.

This section becomes YOUR OWN log entry, visible in
`thrum agent sessions list` alongside the archives of every other session
you've ever restarted from. Future-you (and other agents inspecting your
history) skim §1 to decide which sessions are worth re-reading. Write it
first — before the Resume Plan, before file paths, before patterns —
because composing the §1 summary forces you to identify what was actually
load-bearing about this session, and that priority shapes everything else
you write below it.

After the §1 block:

CRITICAL DISCIPLINE — compose from your own working context only. To preserve
the remaining runway:

- Do NOT dispatch sub-agents (Agent, Explore, etc.)
- Do NOT re-read files you've already read this session
- Do NOT spawn web fetches or external lookups
- Do NOT run lengthy investigations (git log spelunking, codebase searches,
  multi-file grep walks)

Each of those costs context you don't have to spend, and the cost compounds — a
sub-agent that returns 6K tokens of summary doesn't just cost the dispatch, it
pollutes the dying session further. If a fact isn't already in your working
context, label it "unknown" or "verify post-restart" rather than fetching it
now. Trust your in-context state.

Write for a competent stranger in your role — someone who has the runtime
briefing (`thrum prime`, role preamble, project state) but none of this
session's conversation context. Refer to the previous session in third person.

Cover whatever matters most: the big picture, where every artifact stands, who
the players are and what they're contributing, decisions made (with the context
that drove each), questions awaiting human input, outstanding work you owe,
patterns that worked or burned us, file paths future-you will reopen, and a
numbered resume plan. Skip sections that don't apply. An honest "N/A" beats
fabrication.

### 3. Write the continuation directly to your restart file

Use your Write tool to save the composed continuation to:

```text
${REPO}/.thrum/restart/${AGENT}.md
```

`thrum prime` will auto-inject this file at next session start. No bash heredoc
or `cat <<EOF` redirection is needed — write the file directly.

### 4. Check session type and your role

```bash
thrum whoami --field tmux_session
thrum whoami --field role
```

### 5. Orchestrate the handoff

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

## When to Use

- Context window is getting full (you're seeing compaction warnings)
- You've hit rate limits and need to wait
- Your session feels stuck or unproductive
- The operator or coordinator has asked you to restart

## After Restart: Session Archive

After restart, your snapshot doesn't disappear — it moves to
`.thrum/agents/<your-agent-id>/sessions/` and stays there as a persistent
log entry. Browse the archive with:

```bash
thrum agent sessions list                    # default: this agent
thrum agent sessions list --verbose          # full §1 bodies inline
thrum agent sessions list --json             # NDJSON for scripts
thrum agent sessions list --all              # every agent, grouped
```

Permissions are user-only (`0600` for each snapshot file, `0700` for the
sessions folder). Operators on multi-user machines must copy explicitly
to share archives with another account.

Worktree-resident ephemeral agents archive into the worktree's own
`.thrum/agents/<id>/sessions/` — so the archive vanishes when the
worktree is removed. **By design.** If an ephemeral agent's history
matters across the worktree teardown, export those snapshots manually
before `git worktree remove`.

The next session's `thrum prime` output includes a "Past Sessions"
discovery hint summarizing the most recent archive's §1 — so future-you
gets a one-line reminder of last session's headline without re-reading
the full snapshot. That hint is why §1 above is required.
