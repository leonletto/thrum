---
name: verify-against-plan
description: Use after implementation is complete to verify the code covers every requirement from the plan / design spec — runs alongside code-review as the second pass in the Code Review Protocol. Outputs structured findings (BLOCKING/IMPORTANT/MINOR) with file:line evidence, not code-quality opinions.
---

# Verify Against Plan

## Inputs

Two inputs are required. Missing either is a pre-flight bail.

### 1. Plan or spec path (required)

A markdown plan or spec file — the authoritative reference the implementation is checked against.

- Preferred: `dev-docs/plans/YYYY-MM-DD-<topic>-plan.md` (produced by `superpowers:writing-plans`)
- Fallback: `dev-docs/specs/YYYY-MM-DD-<topic>-design.md` if no plan exists

If both exist, prefer the plan — plans are tighter and already contain the file-structure and acceptance-criteria anchors the skill compares against.

### 2. Implementation scope (required; one of three forms)

- **Branch name** — compare branch `HEAD` vs. merge-base with `main` (the common case). E.g. `feat/verify-against-plan`.
- **Commit range** — `start..end` for custom ranges. E.g. `d4943ce5..HEAD`.
- **Worktree path** — infer the branch and diff from the worktree's current state. E.g. `/Users/leon/.workspaces/thrum/plugin-skills-slate`.

### Context-inferred defaults

When the caller supplies no explicit scope, infer:

- **Branch** from `git rev-parse --abbrev-ref HEAD`
- **File scope** from the plan's **File Structure** table — only files the plan claims to touch are in scope for verification

### Invocation examples

**Explicit args:**

```
/verify-against-plan plan=dev-docs/plans/2026-04-19-verify-against-plan-skill-plan.md branch=feat/plugin-skills-slate
```

**Context-inferred (from current worktree):**

```
/verify-against-plan plan=dev-docs/plans/2026-04-19-verify-against-plan-skill-plan.md
```

The second form uses the current branch as the implementation scope.

## Pre-flight checks

Before producing any findings, run these three checks. If any fails, bail with a clear error — do not proceed with partial inputs.

1. **Plan/spec file exists and is readable.** Verify the path resolves and the file is non-empty markdown. Error message on failure names the path and the issue (missing / empty / unreadable).

2. **Implementation scope resolves to a non-empty diff.** For branch or commit-range inputs, `git diff` must produce at least one changed file. An empty diff means either the branch is at merge-base or the range is mis-specified — either way, there is nothing to verify.

3. **Plan's File Structure and Acceptance/Test-plan sections are extractable.** Parse the plan for a `## File Structure` (or equivalent) table and an `## Acceptance` / `## Test plan` section. These are the comparison anchors for the actual verification pass. If neither is present, bail — a plan with no acceptance criteria and no file table cannot be verified against.

Only when all three checks pass should the comparison pass begin.

## Output format

Findings go to stdout in this exact shape so the coordinator can consolidate the dual-review batch without reformatting.

```markdown
## Verify-Against-Plan Findings

**Plan:** <path>
**Implementation:** <branch> (<N> commits, <M> files changed)
**Summary:** <N> BLOCKING, <N> IMPORTANT, <N> MINOR

---

### BLOCKING #1 — <short descriptor>
- **Plan reference:** <plan file>:<line> — "<verbatim requirement>"
- **Implementation state:** <code path>:<line> or "absent"
- **Why it matters:** <link back to acceptance criterion / invariant>
- **Suggested resolution:** <add code, update plan, or flag for coordinator>

### IMPORTANT #1 — …
### MINOR #1 — …
```

Every finding must include all four fields:

- **Plan reference** — exact `<file>:<line>` with the verbatim quote from the plan. No paraphrase.
- **Implementation state** — what's in the code now, or the literal string `absent` if the requirement has no corresponding code.
- **Why it matters** — the acceptance criterion or invariant this finding ties back to. Prevents findings drifting into style opinions.
- **Suggested resolution** — concrete next action (add the missing code, update the plan, or flag for coordinator judgment). "Review this" is not a resolution.

If there are no findings in a severity bucket, omit that bucket entirely — do not write "### BLOCKING #0 — none".

## Severity criteria

- **BLOCKING** — a named acceptance criterion is unmet, or the implementation contradicts a stated invariant. Must be fixed before merge. Examples: AC #3 says "all writes go through the store" but a direct-file-write path exists; plan's File Structure claims `internal/auth/session.go` exists but no such file was added.
- **IMPORTANT** — a plan requirement is implemented but with silent deviation (different file path, different function name, different public shape) that is likely intentional but unverified. Should be fixed, or the deviation explicitly acknowledged by the implementer. Example: plan says `ResolveAgentID()` but code has `GetAgentID()` with the same behavior — probably fine, but nobody said so.
- **MINOR** — missing documentation reference, commit-message format drift, or stylistic plan deviation that does not affect behavior. Example: plan calls for `Refs thrum-s9q9.3` in commit body, commit just has the title.

When choosing between BLOCKING and IMPORTANT, apply the test: *would a reader of the merged code be surprised by this?* BLOCKING = yes, definitely; IMPORTANT = maybe, needs explanation.

## Invariant: stdout only, no inline edits

Findings go to stdout only. Do NOT:

- Create TodoWrite entries, beads issues, or worklog files inline — consolidation into the dual-review batch is the coordinator's job.
- Edit the code or the plan in response to findings — that is the implementer's next step after receiving the consolidated review.
- Modify git state (no commits, no stashes, no checkouts).

The skill reports; the coordinator decides; the implementer fixes. Keep those three roles separate or the dual-review batch stops composing cleanly with `feature-dev:code-reviewer`.

<!-- Body filled in tasks 3-4 -->
