---
name: coordinator-post-restart-sweep
description: "Use immediately after the coordinator returns from a restart, compaction, or extended absence — as the first deliberate action post-prime. Detects agents whose latest assistant message indicates they are blocked waiting for a coordinator decision the coordinator may not have seen (question surfaced in pane, not in inbox). Safe to run any time the session feels \"we've been gone a while\"; not just post-restart. Pairs with the coordinator-context-monitoring sweep (sibling sweep, different lens)."
# source: claude-plugin/skills/coordinator-post-restart-sweep/SKILL.md
# generated-by: scripts/sync-skills.sh
---


## Coordinator: Post-Restart Sweep for Waiting-on-Coord Agents

### When to invoke

The canonical trigger: **post-prime, as your first deliberate coordinator
action.** Restarts and compactions wipe in-flight context; an agent who surfaced
a question 20 minutes before your restart will still be standing at-pane with
their question visible — but the question lives in the agent's JSONL transcript,
not in your inbox. The sweep reads each agent's latest assistant message body,
pattern-matches against a library of empirically- observed waiting-on-coord
phrasings (mined from the project's conversation archive per thrum-e1n0), and
flags hits.

Other reasonable triggers:

- After any extended coord absence (cron firing for the first time after a
  multi-hour gap)
- When you suspect you missed a question — gut-check sweep
- At an epic merge gate where multiple agents may be standing by
- Anytime the inbox feels suspiciously quiet given the fleet size

### Step 1 — Run the sweep

```bash
bash scripts/waiting-on-coord-agent-sweep.sh --out /tmp/waiting-on-coord.txt
cat /tmp/waiting-on-coord.txt
```

The script enumerates alive Claude agents (Codex/Cursor/OpenCode runtimes are
skipped at v1 — pa34 epic backlog), extracts each agent's latest assistant
message body from the JSONL transcript at
`~/.claude/projects/<encoded-worktree>/<session>.jsonl`, and pattern-matches
against the regex library. Exit code is `0` if zero flagged, `1` if any flagged
— safe to chain into cron / hooks.

> **Edge case:** only the last 200 JSONL entries are scanned. An agent whose
> latest assistant message is older than the last 200 entries (e.g., a very
> active tool-call burst since the last assistant turn) is silently skipped.
> Rare in practice; raise the tail window in the script if it bites.

Output per flagged agent: agent identity + tmux session + worktree + idle time +
matched pattern labels + the last ~15 lines of the assistant message body as an
excerpt.

### Step 2 — Read each flagged agent's excerpt

For each `===== @<agent_id> · ... =====` block in the report:

1. **Read the excerpt verbatim.** Pattern-match is the trigger; the excerpt is
   the evidence. Do not act on the pattern label alone — the excerpt may reveal
   the agent is asking something already resolved, asking the wrong coord, or
   actually fine (false positive).
2. **Cross-reference against your own context.** Have you already answered this
   in a thrum message they haven't seen yet? Is this carried-forward from a
   prior session whose decision is recorded in `bd remember` or `dev-docs/`?
3. **Classify the blocker.**

### Step 3 — Decision tree per flagged agent

| Situation                                     | Action                                                                                                 |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| You know the answer / direction               | Respond directly via `thrum send "..." --to @<agent>`                                                  |
| Needs Leon's judgment (escalation)            | Add to PENDING LEON section; surface in next user turn (see `feedback_surface_pending_leon_questions`) |
| Needs investigation before you can answer     | Ack via `thrum send "investigating — back in <N>min" --to @<agent>` so they know they're seen          |
| False positive — agent isn't actually blocked | No action. Optionally `bd remember` the false-positive pattern for future tuning                       |
| Agent's question is stale (already resolved)  | Reply briefly with the resolution + pointer to where it was decided                                    |

The decision tree is judgment work, not script automation. The sweep does
**not** auto-respond, auto-nudge, or auto-escalate — coord IS the recipient of
the report, and the right next action is context-dependent (per thrum-e1n0
scope-clarification with @impl_v0105: report-only, no auto-action).

### Step 4 — Re-run the sweep after acting

After responding to all flagged agents:

```bash
bash scripts/waiting-on-coord-agent-sweep.sh
```

Convergence check: ideally zero flagged. If the same agent is still flagged,
either:

- Your reply hasn't propagated yet — wait a turn and re-sweep
- The agent has a second blocker the original reply didn't address — read the
  excerpt again
- False positive standing — note it; consider updating the regex library in
  `scripts/waiting-on-coord-agent-sweep.sh` (file a P3 bd if the false positive
  is structural)

### Reference

- **Sweep script**: `scripts/waiting-on-coord-agent-sweep.sh` (Claude-only at
  v1; non-Claude runtimes via thrum-pa34 epoch adapters when available)
- **Pattern library source**: empirical mining of project conversation archive
  via the episodic-memory plugin (thrum-e1n0, 2026-05-20). Patterns observed at
  least twice in real waiting-on-coord situations; literal examples include
  PENDING LEON banners, "your call", "awaiting your <X>", "Standing by for
  coordinator's <X>", "Stopping here to surface". See the comment header in the
  script for the full list with specificity ratings.
- **Test fixtures**: `tests/scripts/fixtures/waiting-on-coord/` — 6 positive and
  3 negative real-conversation excerpts, validated by
  `tests/scripts/waiting_on_coord_patterns_test.sh`.
- **Sibling sweep**: `scripts/error-and-context-agent-sweep.sh` — same
  enumeration substrate, different lens (ctx % + api_errors instead of
  waiting-on-coord). Future refactor opportunity: extract a shared
  identity-enumeration + JSONL-resolution helper when a third sweep variant
  lands.
- **Motivating incident**: 2026-05-20 researcher_thrum_memory sat blocked ~10min
  with question fully visible at-pane but not in coord's inbox.
- **Related discipline**:
  - `feedback_surface_pending_leon_questions` — escalations to Leon go in a
    top-level PENDING LEON section and re-surface every turn until answered
  - `feedback_byte_equality_pane_detection` — when in doubt about an agent's
    state, prefer JSONL substrate over pane diffs
  - `reference_claude_jsonl_state_source` — the JSONL transcript is the
    authoritative state source for Claude agents

### Iteration framing

The pattern library is v1, mined from a finite slice of project history. Expect
false positives + false negatives in the early weeks. When you see either:

- **False positive** (sweep flagged an agent who wasn't actually waiting): note
  the regex label + the excerpt that fired it; consider tightening the pattern.
- **False negative** (an agent WAS waiting and the sweep missed them): extract
  the exact phrasing, add a test fixture under
  `tests/scripts/fixtures/waiting-on-coord/`, add a regex rule to the pattern
  library in `scripts/waiting-on-coord-agent-sweep.sh`, and run the test
  harness.

Refinements land as small `feat(scripts)` commits. Track structural improvements
(beyond regex) in a future bd ticket.
