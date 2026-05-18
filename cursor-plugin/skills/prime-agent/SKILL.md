---
name: prime-agent
description:
  Use on scheduled-agent wake or lean prime restart — replaces the full
  `thrum prime` briefing with a lean two-step warmup tuned for short-lived
  scheduled-agent invocations. Step 1 reads the inbox literally (NOT a
  discipline assumption); step 2 surfaces newly-shipped skills since the
  agent last ran.
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

**Run this exact diff against the last-seen skill set, then write
the current set back so the NEXT wake compares against THIS wake.**

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

# Update last_seen_skills.txt — this wake's skill set becomes the
# next wake's baseline. Do this AT BOOT (not at end-of-session) so
# the next wake's diff surfaces only what's truly NEW since this
# boot, not since end-of-session (which might be drift-free if no
# new skills shipped during the session).
#
# Atomic write via temp-file + mv to match the project's
# shared-state-file convention (portfile.go, peer_registry.go,
# scheduler/reload.go). A crash mid-cp would leave a partial
# last_seen_skills.txt; the next wake's diff would then emit
# "all skills new" (false-positive drift). Temp+mv eliminates the
# partial-write window.
mkdir -p ".thrum/agents/${AGENT_NAME}"
cp /tmp/current_skills.txt "${LAST_SEEN}.tmp" && \
  mv "${LAST_SEEN}.tmp" "${LAST_SEEN}"
```

The skill registry mirrors C-B1's auto-discovery (canonical §8.5): when a
new skill ships to `.claude/skills/`, the next wake's lean-prime surfaces
it. Writing `last_seen_skills.txt` at the END of Step 2 (after the diff)
means each wake updates its own baseline — the diff at wake N+1 reflects
what's NEW since wake N's boot, regardless of mid-session skill drift.

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
record what happened in `.thrum/agents/<agent_id>/state.md`. The
`last_seen_skills.txt` baseline is already bumped by Step 2 above —
`/thrum:update-agent-state` owns the state.md half of the wake-loop
bookkeeping, not the skill-library half.

If the prior session crashed (no clean state.md write), run
`/thrum:recover-agent-state` instead — it validates the existing
state.md structurally and routes the spec §6.5 corruption flow if the
parse fails (moves to `state.md.broken`, sets the auto-respawn gate
flag, pages the operator).
