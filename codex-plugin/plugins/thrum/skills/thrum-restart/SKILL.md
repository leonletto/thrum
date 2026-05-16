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
orchestrate the handoff.

### Steps

#### 1. Resolve your identity and repo root

```bash
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
mkdir -p "${REPO}/.thrum/restart"
```

#### 2. Compose your continuation

Your context is high and we want to restart without losing the decisions we've
made. Write a rich continuation that future-you will read as the first action
after restart.

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

#### 3. Write the continuation directly to your restart file

Use your Write tool to save the composed continuation to:

```text
${REPO}/.thrum/restart/${AGENT}.md
```

`thrum prime` will auto-inject this file at next session start. No bash heredoc
or `cat <<EOF` redirection is needed — write the file directly.

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
