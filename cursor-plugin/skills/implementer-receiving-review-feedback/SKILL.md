---
name: implementer-receiving-review-feedback
description: Use when receiving review findings, when a reviewer flagged an issue, during a review cycle, or when responding to review. Loads implementer-specific discipline that complements superpowers:receiving-code-review.
---

# Implementer: Receiving Review Feedback

This skill **complements** `superpowers:receiving-code-review` — it does
not restate the technical-rigor and no-performative-agreement content
from that skill. Layer the rules below on top of those universals.

## The review gate is a hard stop, not a suggestion

**Why:** Review gates exist because unreviewed code from one epic may
require substantial changes before the next epic can build on it.
Starting the next epic before the current one is reviewed creates
compounding rework — when round-2 fixes land in epic N, the early work
on epic N+1 may need to be redone or reverted. (Source:
findings_implementer.md — "Treat the review gate as a hard stop, not a
suggestion".)

**How to apply:** After sending DONE/DONE_WITH_CONCERNS for an epic:
close beads tasks for that epic, ensure work is durably preserved per
project policy, then enter idle. Do not claim tasks in the next epic. Do
not pre-read its spec section. Do not "just start the trivial first
task". Wait for the coordinator's explicit GREENLIT or APPROVED message.

## Batch all review fixes into ONE commit

**Why:** A single fix commit makes re-review straightforward — the
coordinator inspects one diff against the numbered finding list.
(Source: findings_implementer.md — virtual-supervisor 2026-04-17
implementer handled five findings in a single commit `da48c70`, which
listed each finding in the message body.) Per-finding commits multiply
the surface the re-review has to traverse and increase the chance that a
finding fixed in commit A is regressed by commit B.

**How to apply:** Read the full numbered finding list before writing any
code. Fix all BLOCKING and IMPORTANT findings in scope. Run tests once
they're all addressed. Commit with a message that lists each finding
number and its resolution:

```bash
git commit -m "$(cat <<'EOF'
fix: address review findings #1-#5 for <task-id>

- #1 (BLOCKING) — <one-line resolution>
- #2 (IMPORTANT) — <one-line resolution>
- #3 (LOW) — <one-line resolution>
- #4 (LOW) — verified false positive: <one-line evidence>
- #5 (LOW) — deferred per <decision/rationale>

make test PASS (race), make lint PASS.

Refs: <task-id>
EOF
)"
```

If a finding is truly independent and the coordinator explicitly
requested separate commits, that's the only time to fragment.

## Don't reflexively bucket findings as "follow-ups"

**Why:** When the file you're already modifying contains a small
adjacent issue, deferring it to a follow-up issue is often the wrong
trade. The marginal cost of fixing it now is small (the file is
already in your context, you already understand the surrounding code).
The cost of a future follow-up is real (re-load context, re-read
neighbors, redo verification). Default to fix-now when the file is
already being touched. (Source: feedback_no_lazy_deferral.md.)

**How to apply:** For each finding categorized as "could defer", run
the trade-off explicitly:

- Is the file already in the diff? Default to fix-now
- Is the fix a few lines? Default to fix-now
- Does verification require running the same tests as the main fix?
  Default to fix-now
- Is the fix a meaningful refactor or design change? Defer is
  legitimate; file the bd issue and link it in the commit message

Don't reach for "deferred" as a shortcut to ship faster.

## Push back with file:line evidence when the reviewer is wrong

**Why:** Reviewers reading large diffs sometimes cite wrong line numbers,
describe behavior that doesn't match the current code, or apply a
universal rule to a project-specific exception. Pushback is welcome —
it's how the feedback loop stays calibrated. (Source: project Code
Review Protocol — "trace-corrections are welcome".) But pushback without
verification is just opinion against opinion.

**How to apply:** When you disagree with a finding, verify against the
source first:

- For file/line claims: read the cited file at the cited lines, paste
  the actual content
- For API claims: grep for the symbol, paste the signature
- For behavior claims: trace the call path and cite the relevant
  function

Format pushback as: "Finding #N — verified false positive. <one-line
restatement of what reviewer said>. Actual: <what the code does>,
evidence at `<file>:<line>`." Then ask the coordinator to drop or
restate. If after verification you're still convinced the finding is
right, go fix it.

## Re-review after fixes — don't ship the round-2 fix as DONE on round 1

**Why:** When the coordinator dispatches re-review on the round-2
commit, they expect to find a clean diff from your fix work.
Accidentally bundling unrelated changes into the fix commit muddies the
review and either gets sent back or merges work the coordinator hasn't
seen.

**How to apply:** Between round-1 review feedback and round-2 ping:

1. Fix only what the findings ask for
2. Don't refactor, rename, or "improve" surrounding code
3. Verify tests pass with `-race`
4. Commit and ping with the per-finding disposition format from
   `implementer-status-and-handoff`

If you see additional issues during the fix work that weren't in the
findings list, file them as bd issues — don't slip them into the fix
commit.

## Project-specific rules (already loaded)

Project-local rules under `bd memories implementer-rule-` were loaded at
session start by your preamble. If a project-local rule conflicts with a
universal rule above, the project-local rule wins; surface the conflict
in your reply so the user can decide whether to graduate or remove the
override.

If you accumulate a new rule mid-session (the user corrects you), capture
it via `bd remember --key implementer-rule-<slug> "<rule + Why + How to
apply>"`.
