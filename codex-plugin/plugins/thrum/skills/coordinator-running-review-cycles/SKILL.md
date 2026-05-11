---
name: coordinator-running-review-cycles
description:
  "Use when an implementer reports DONE, when consolidating review findings,
  when handling implementer pushback on a finding, or when arriving at a review
  gate. Loads coordinator-specific discipline for running review cycles cleanly."
# source: claude-plugin/skills/coordinator-running-review-cycles/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Coordinator: Running Review Cycles

### Run code-quality + spec-compliance review in parallel

**Why:** Reviewers serve different purposes. `feature-dev:code-reviewer` catches
security, error-handling, idiom, and dead-code issues. `verify-against-plan`
catches missing scope, unmet acceptance criteria, and silent deviations from the
plan. Running them sequentially doubles review-cycle latency for no reason —
they're independent reads of the same diff. (Source: project Code Review
Protocol — "Run both review types in parallel for every agent branch".)

**How to apply:** When an implementer reports DONE on a branch, dispatch both
reviewers as parallel sub-agents in the same message. Pass each a clear scope
("review the diff at <BASE>...<HEAD> for <criteria>"). Use `model: "sonnet"` for
both — review needs judgment, not Opus reasoning.

### Wait for both reviewers before sending anything to the implementer

**Why:** Sending findings from reviewer A while reviewer B is still running
causes the implementer to fix batch 1 and miss batch 2 entirely, extending the
review cycle by a full extra round. (Source: findings_coordinator.md — "Wait for
both reviewers before sending findings".)

**How to apply:** Block until both review sub-agents return their full output.
Do not send partial findings, even if the user is asking for an update. If one
reviewer takes substantially longer, the right move is a status update to the
implementer ("review running, hold"), not a partial batch.

### Consolidate findings into ONE numbered list across all severities

**Why:** A single numbered list with mixed severities lets the implementer work
top-to-bottom without context-switching across "minor batch" and "critical
batch" calls. Severity labels live next to each finding; ordering is by
severity, then by file path. Two separate lists invite the implementer to skip
one.

**How to apply:** Merge both reviewers' output. Number sequentially from

1. Each item: `<severity> · <file:line> · <one-line summary>` followed by the
   detail and suggested fix. Keep the numbering stable across review rounds —
   when re-reviewing fixes, refer to "finding #4" and the implementer can locate
   it instantly.

### Verify reviewer claims against actual code before forwarding

**Why:** Reviewers reading a large diff sometimes cite wrong line numbers or
describe behavior that doesn't match the current code. Forwarding a misread
finding causes the implementer to waste time investigating a non-issue and
erodes trust in the review process. (Source: findings_coordinator.md — "Verify
reviewer claims before forwarding".)

**How to apply:** For any finding citing a specific file and line, read that
file at that range before forwarding. For API-shape claims, grep for the symbol
and read the signature. For behavior claims, trace the call path. Drop or
correct findings that don't survive verification — silently forwarding wrong
findings poisons the cycle.

### Treat implementer pushback as a signal to verify, not dismiss

**Why:** When an implementer disputes a finding ("you said X, but the actual
shape is Y — here's the trace"), that is the correct behavior, not
insubordination. The coordinator may have stated expectations that drift from
runtime reality. Treating pushback as a challenge breaks the feedback loop and
trains implementers to swallow corrections. (Source: findings_coordinator.md —
"Implementer pushback is a signal to verify, not dismiss".)

**How to apply:** When an implementer pushes back: read the cited file at the
cited lines, run the cited command, or `bd show <id>` for beads state. If
they're right, acknowledge the correction explicitly and update session context.
If the finding was based on a misread, drop it from the batch and tell them so.
If you're still right after verification, restate with the new evidence — but
never just reassert without verification.

### Re-review after fixes land — repeat until both pass

**Why:** A single review pass rarely catches everything, and fix commits can
introduce new regressions. Skipping the re-review accepts an unknown risk that
scales with diff size.

**How to apply:** When the implementer reports fixes complete, re-dispatch both
reviewers (in parallel) on the new diff. Most of the time, round 2 returns clean
or with one minor finding. If round 3 still has Critical or Important findings,
escalate to the user — repeated failure to land fixes signals a deeper scope or
design issue, not just sloppy review.

### Use structural evidence when a green test is blocked upstream

**Why:** A test that cannot run (blocked upstream build, unrelated flake, CI
environment issue) cannot validate behavior. Saying "no errors seen" is weaker
than enumerating what a regression would produce and showing none of those
signatures occurred. (Source: findings_coordinator.md — "Verify the cited code
before closing a regression as 'green'".)

**How to apply:** When a green-test check is blocked, prove absence of
regression by: (1) listing specific failure modes a regression would produce
(error types, wrong status codes, missing state changes); (2) showing none of
those modes occurred (wire-level status codes, specific log assertions, expected
state present); (3) documenting the upstream blocker in a tracking issue so the
green-test check can be re-run when it clears.

### Project-specific rules (already loaded)

Project-local rules under `bd memories coordinator-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key coordinator-rule-<slug> "<rule + Why + How to apply>"`.
