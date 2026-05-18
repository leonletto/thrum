---
name: prime-agent
description:
  Use on scheduled-agent wake or lean prime restart — replaces the full
  `thrum prime` briefing with a lean two-step warmup tuned for short-lived
  scheduled-agent invocations. Step 1 reads the inbox literally (NOT a
  discipline assumption); step 2 surfaces newly-shipped skills since the
  agent last ran.
allowed-tools: "Bash(thrum:*),Bash(ls:*),Bash(diff:*),Bash(cat:*),Read"
---

# Thrum: Prime Scheduled Agent (Lean)

This skill is the wake-time variant of `thrum prime` for scheduled agents
(per canonical §8.5 + spec §7.4). Full `thrum prime` is too heavy when the
agent is going to run for a few minutes and then exit — but the two pieces
of state that matter MUST run literally, not as a discipline.

## Step 1: Read the inbox

**Run this exact command. Do not skip it. Do not defer it.**

```bash
thrum inbox --unread
```

This is a hard-required step (not a discipline assumption). The
`agent.wake` message that triggered this session lives in the inbox; the
wake-primer payload + any operator escalations land there too. Reading the
inbox is how the agent learns WHY it was woken and WHAT the primer said.

If `thrum inbox --unread` returns no messages, the agent was likely woken
without a wake message (manual restart, daemon error). In that case,
continue to Step 2 — the skill-library check is still useful — but flag
the missing wake message in the status report at end of session.

## Step 2: Skill-library check

**Run this exact diff against the last-seen skill set.**

```bash
ls .claude/skills/ > /tmp/current_skills.txt
AGENT_NAME=$(thrum whoami --field agent_id)
LAST_SEEN=".thrum/agents/${AGENT_NAME}/last_seen_skills.txt"
if [ -f "${LAST_SEEN}" ]; then
  diff "${LAST_SEEN}" /tmp/current_skills.txt || true
else
  echo "First wake for this agent — full skill library:"
  cat /tmp/current_skills.txt
fi
```

The skill registry mirrors C-B1's auto-discovery (canonical §8.5): when a
new skill ships to `.claude/skills/`, the next wake's lean-prime surfaces
it. The agent's `last_seen_skills.txt` is updated at end of session (by
`/thrum:update-agent-state`) so the next wake sees the diff against TODAY's
skill set, not the first-ever one.

Do not skip this step even if Step 1's inbox was empty. Skill drift between
wakes is a real failure mode — agents can miss a critical new skill if
the lean-prime suppresses the diff during quiet sessions.

## When NOT to use this skill

This is the SCHEDULED-AGENT wake variant. If you're a coordinator or a
human-operator-spawned agent, use `thrum prime` instead — the full
briefing covers team state, project state, work context, and recent commits
that the lean variant skips. The lean variant exists because scheduled
agents run for a few minutes and exit; the full briefing's setup cost
isn't recouped over the session lifetime.

If your session is going to exceed ~30 minutes or you need to coordinate
with other agents, defer to `thrum prime`.

## After Step 2

Once both steps complete, proceed with the work the wake-primer asked for.
At end of session, before exiting, run `/thrum:update-agent-state` to
record what happened in `.thrum/agents/<agent_id>/state.md` and bump
`last_seen_skills.txt` so the NEXT wake's Step 2 shows the right diff.

If the prior session crashed (no clean state.md write), run
`/thrum:recover-agent-state` instead — it reconstructs from the previous
transcript and validates parseability before writing.
