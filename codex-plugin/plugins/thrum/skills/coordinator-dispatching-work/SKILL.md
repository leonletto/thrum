---
name: coordinator-dispatching-work
description:
  "Use when starting an epic, dispatching to an implementer, creating a worktree
  for an agent, or spawning a sub-agent. Loads coordinator-specific discipline
  for kicking off implementation work."
# source: claude-plugin/skills/coordinator-dispatching-work/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Coordinator: Dispatching Work

### Use `thrum tmux launch` — not raw send-keys

**Why:** Manual `tmux send-keys 'claude'` followed by `$thrum-prime` skips
identity registration and produces silent CWD drift. The
`thrum tmux launch <name>` flow registers the agent against the worktree path
correctly and gives the daemon a real PID to track. (Source:
findings_coordinator.md — "Use thrum tmux launch — not send-keys — to start a
runtime".)

**How to apply:** Whenever you need to start a runtime in an existing tmux pane,
use `thrum tmux launch <session_name>` even if you've already typed the runtime
command yourself. If you've already started one wrong, kill the pane and
re-launch via the daemon. If the pane doesn't exist yet, use
`thrum tmux start <name>` (creates, launches, primes, attaches in one command).

### Recycling an agent: destroy then teardown

**Why:** The inverse of dispatch is removal, and it has the same "two-step
ritual or things break silently" property as dispatch via tmux launch +
thrum send. See `coordinator-managing-state-and-lifecycle` § "Destroy an agent
before tearing down its worktree" for the canonical sequence
(`thrum tmux kill` BEFORE `thrum worktree teardown`). The dispatching skill
mentions it here so you find the cross-reference when planning a wave
recycle, not just when reading the lifecycle skill end-to-end.

### Never spawn sub-agents into worktrees where Thrum agents are running

**Why:** Sub-agents (Agent tool) and Thrum agents (`thrum send`) are different
coordination mechanisms. A sub-agent spawned into a worktree where a Thrum agent
already sits competes for files, identity, and tmux state — the result is
identity drift, broken nudges, and silent message loss. (Source:
findings_coordinator.md — "No sub-agents into live worktrees".)

**How to apply:** Run `thrum team` before spawning any Agent tool call. If a
worktree shows a registered agent, communicate with it via
`thrum send "<message>" --to @<agent_name>` instead. Sub-agents are for
research/explore, code review, and message listeners running in the main repo —
never for implementation work in another agent's worktree.

### Dispatch via `thrum send` after launch — not via tmux send-keys

**Why:** After `thrum tmux launch`, all coordination flows through the inbox +
daemon nudge: `thrum send` enters the message in the daemon's state, the daemon
nudges the pane, the agent reads the inbox and starts work. Injecting the prompt
with `thrum tmux send` or `tmux send-keys` bypasses the inbox entirely, breaks
the nudge for future messages, and strands the agent without a recorded message.
(Source: findings_coordinator.md — "Dispatch via thrum send after tmux launch,
not via tmux send-keys".)

**How to apply:** Correct flow: `thrum tmux launch <name>` → agent auto-primes →
`thrum send "work prompt" --to @agent_name` → daemon nudges the pane → agent
reads inbox and begins work. Never inject the prompt as raw keystrokes.

### Prompt construction for implementers

**Why:** Implementer agents work better with a structured handoff than a
free-form request. Inconsistent dispatch produces inconsistent reports and
re-dispatch overhead.

**How to apply:** Every dispatch prompt should include:

1. The epic/task title and bead ID
2. One-paragraph scope (what to build, what NOT to build)
3. Acceptance criteria as bullets
4. The worktree path the work happens in
5. An explicit reminder to pass `model:` on any sub-agents the implementer
   spawns (haiku for mechanical, sonnet for judgment)
6. Spec/plan paths to read before starting

### Never rename an agent tied to a worktree

**Why:** Agent identity is bound to the worktree, not the epic. Re-registering
`@implementer_api` as `@implementer_billing` mid-flight creates two identity
files in the same worktree, causing persistent stop-hook misfires and routing
failures. (Source: findings_coordinator.md — "Never rename an agent tied to a
worktree".)

**How to apply:** Run `thrum team` before assigning new work to confirm the
existing identity name. Send work to that name. Do not use `thrum quickstart`
with a new name in a worktree that already has an identity. If you absolutely
need a fresh identity, kill the existing tmux session and register a new one
with a different name — do not rename in place.

### Propagate model-selection discipline downward

**Why:** Sub-agents inherit the parent model by default. A coordinator on Opus
that spawns an unspecified sub-agent gets Opus-cost work for tasks that need
Haiku. The same trap repeats inside an implementer's worktree: if the
implementer doesn't propagate the discipline, their own sub-agents silently
inherit too. Cost compounds across a session. (Source: findings_coordinator.md —
"Always pass explicit model on sub-agent spawns".)

**How to apply:** Every dispatch prompt should include the model-selection rule
explicitly: "When you spawn your own sub-agents, pass explicit `model:` —
`haiku` for mechanical work (lint, tests, find/replace), `sonnet` for judgment
work (review, complex implementation), `opus` only when justified. Default to
sonnet over opus." Audit the dispatch prompt before sending.

### The impl-prompt review stamp satisfies pre-dispatch review

**Why:** When the planning-skill review loop runs, its final gate reviews the
implementation prompt against the plan — which IS the pre-dispatch review.
Re-running a pre-dispatch prose review on a prompt that already passed the loop
double-reviews the same artifact and wastes a cycle.

**How to apply:** At dispatch, grep the impl-prompt for the loop's verdict stamp
(case-sensitive, literal):

```bash
grep -F 'THRUM-REVIEW: stage=prompt verdict=Ready:Yes' "$PROMPT_FILE" \
  || grep -F 'THRUM-REVIEW: stage=prompt verdict=OVERRIDE' "$PROMPT_FILE"
```

- **Stamp present** → the pre-dispatch review is already satisfied; SKIP
  re-running it and dispatch.
- **Stamp absent** → fall through to the normal pre-dispatch dual review. Do NOT
  block dispatch on the stamp — it is a shortcut that lets you skip a redundant
  review, not a new hard requirement (pre-feature prompts and prompts from other
  flows won't carry it).

This is the prose side of the boundary; the post-DONE dual-review (which reviews
the implementer's CODE, a different artifact) always runs separately. The prompt
is never reviewed twice. (See `coordinator-running-review-cycles` for the
post-DONE side.)

### Project-specific rules (already loaded)

Project-local rules under `bd memories coordinator-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key coordinator-rule-<slug> "<rule + Why + How to apply>"`.
