---
name: update-agent-state
description:
  Use at end of work / wrap up session / save agent state — records the
  just-completed session into the scheduled agent's state.md history. The 4
  verbatim + 3 blocks of 5 = 19-session sliding window is enforced by the
  daemon-side parser; the agent's job is to author a one-line summary that will
  be the verbatim entry.
---

# Thrum: Update Agent State

Record the just-completed session into `.thrum/agents/<agent-id>/state.md` so
the NEXT wake's `/thrum:prime-agent` invocation has continuity.

This skill is the wake-loop counterpart to `/thrum:prime-agent`. Run this at the
END of every scheduled-agent session, before exiting.

## When to use this skill

- You're a SCHEDULED AGENT (woken by an `agent.wake` message)
- You're about to exit / end the session
- You have a coherent one-line summary of what shipped

If you're an operator-spawned agent (coordinator, on-demand task), use
`/thrum:update-project` instead — that updates the project- wide state.md, not
the per-agent state.md this skill targets.

## Step 1: Compose your one-line summary

Write a single sentence that names what shipped this session. Examples (drawn
from prior session-archive epic's §1 mandate, same specificity standard):

> Locked the session-archive spec v2 with §1 Big picture requirement, five
> Q-Spec approvals, and Q-Spec-5 deferred to impl-time.

> Closed B-B1 E6.0 brainstormer-third pass. 2 BLOCKING + 5 IMPORTANT + 10 MINOR.
> All three load-bearing traps PASSed.

> Investigated rc.9 inbox-race; confirmed lock-substrate fence is the right fix.
> Filed thrum-XXX with 4 BLOCKING evidence points.

Be specific. Future-you skimming the verbatim queue will skim these one-liners
first — vague entries ("worked on B-B1") waste the verbatim slot.

## Step 2: Run the update command

```bash
SESSION_ID=$(thrum whoami --field session_id)
thrum agent state update \
  --session-id "${SESSION_ID}" \
  --summary "<your one-line summary from Step 1>"
```

The CLI:

1. Reads `.thrum/agents/<your-agent-id>/state.md` (or creates an empty one on
   first wake).
2. Prepends your new entry to the verbatim queue (slot #1).
3. Promotes the displaced #4 entry into the most-recent summary block (per spec
   §7.5 sliding-window rules), opening a new block + dropping the oldest if the
   cap is reached.
4. Writes the updated `state.md` atomically (temp-file + rename).

The strict 4-verbatim / 3-block / 5-per-block invariants live in the daemon-side
`agentstate` package — you can't accidentally break the format by running this
command. Hand-editing `state.md` yourself is NOT supported (the parser will
reject a manually- mangled file at the next recovery cycle).

## Step 3: Optional — replace narrative sections

If this session changed your "Last worked on" or "Planning next" paragraphs
(e.g., closed an open thread, identified a new follow-up), pass them as flags:

```bash
thrum agent state update \
  --session-id "${SESSION_ID}" \
  --summary "<one-liner>" \
  --last-worked-on "I closed E6.2 update-state. Open thread: recovery skill needs the RouteEscalation wiring (Task 26)." \
  --planning-next "Next wake should pick up Task 26 — the parse-validation pre-condition is the load-bearing piece per spec §6.5."
```

Without these flags, the previous "Last worked on" / "Planning next" paragraphs
are preserved. Skill `/thrum:prime-agent` will surface them on next wake
regardless.

## Step 4: Verify the write

The CLI prints a one-line confirmation:

```
Updated /path/to/.thrum/agents/<id>/state.md (verbatim: N, summary blocks: M)
```

Where N ∈ [1, 4] and M ∈ [0, 3]. If N or M fall outside those ranges, something
is wrong — the format invariants are enforced in code; an out-of-range count
indicates a code bug worth filing under thrum-xir.

## What this skill does NOT do

- Does NOT update `last_seen_skills.txt` (that's `/thrum:update-agent-state`'s
  sibling responsibility, wired by Task 27 of B-B1 E6.2; current implementation
  does not bump the file).
- Does NOT trigger the next wake (cron / scheduler dispatch is daemon-driven;
  this skill just records what already shipped).
- Does NOT recover from a malformed `state.md` — if the parser rejects the
  existing file, run `/thrum:recover-agent-state` first to clear the corruption
  flag via the spec §6.5 flow.
