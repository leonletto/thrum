---
name: verify-against-plan
description: Use after implementation is complete to verify the code covers every requirement from the plan / design spec — runs alongside code-review as the second pass in the Code Review Protocol. Outputs structured findings: missing scope, unmet acceptance criteria, silent deviations from the spec, newly-introduced surprises.
---

# Verify Against Plan

## Inputs

Two inputs are required. Missing either is a pre-flight bail.

### 1. Plan or spec path (required)

A markdown plan or spec file — the authoritative reference the implementation is
checked against.

- Preferred: `dev-docs/plans/YYYY-MM-DD-<topic>-plan.md` (produced by
  `superpowers:writing-plans`)
- Fallback: `dev-docs/specs/YYYY-MM-DD-<topic>-design.md` if no plan exists

If both exist, prefer the plan — plans are tighter and already contain the
file-structure and acceptance-criteria anchors the skill compares against.

### 2. Implementation scope (required; one of three forms)

- **Branch name** — compare branch `HEAD` vs. merge-base with `main` (the common
  case). E.g. `feat/verify-against-plan`.
- **Commit range** — `start..end` for custom ranges. E.g. `d4943ce5..HEAD`.
- **Worktree path** — infer the branch and diff from the worktree's current
  state. E.g. `/Users/leon/.workspaces/thrum/plugin-skills-slate`.

### Context-inferred defaults

When the caller supplies no explicit scope, infer:

- **Branch** from `git rev-parse --abbrev-ref HEAD`
- **File scope** from the plan's **File Structure** table — only files the plan
  claims to touch are in scope for verification

### Invocation examples

**Explicit args:**

```
/verify-against-plan plan=dev-docs/plans/YYYY-MM-DD-topic-plan.md branch=feat/plugin-skills-slate
```

**Context-inferred (from current worktree):**

```
/verify-against-plan plan=dev-docs/plans/YYYY-MM-DD-topic-plan.md
```

The second form uses the current branch as the implementation scope.

## Pre-flight checks

Before producing any findings, run these three checks. If any fails, bail with a
clear error — do not proceed with partial inputs.

1. **Plan/spec file exists and is readable.** Verify the path resolves and the
   file is non-empty markdown. Error message on failure names the path and the
   issue (missing / empty / unreadable).

2. **Implementation scope resolves to a non-empty diff.** For branch or
   commit-range inputs, `git diff` must produce at least one changed file. An
   empty diff means either the branch is at merge-base or the range is
   mis-specified — either way, there is nothing to verify.

3. **Plan's File Structure and Acceptance/Test-plan sections are extractable.**
   Parse the plan for a `## File Structure` (or equivalent) table and an
   `## Acceptance` / `## Test plan` section. These are the comparison anchors
   for the actual verification pass. If neither is present, bail — a plan with
   no acceptance criteria and no file table cannot be verified against.

Only when all three checks pass should the comparison pass begin.

## Output format

Findings go to stdout in this exact shape so the coordinator can consolidate the
dual-review batch without reformatting.

```markdown
## Verify-Against-Plan Findings

**Plan:** <path> **Implementation:** <branch> (<N> commits, <M> files changed)
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

- **Plan reference** — exact `<file>:<line>` with the verbatim quote from the
  plan. No paraphrase.
- **Implementation state** — what's in the code now, or the literal string
  `absent` if the requirement has no corresponding code.
- **Why it matters** — the acceptance criterion or invariant this finding ties
  back to. Prevents findings drifting into style opinions.
- **Suggested resolution** — concrete next action (add the missing code, update
  the plan, or flag for coordinator judgment). "Review this" is not a
  resolution.

If there are no findings in a severity bucket, omit that bucket entirely — do
not write "### BLOCKING #0 — none".

## Severity criteria

- **BLOCKING** — a named acceptance criterion is unmet, or the implementation
  contradicts a stated invariant. Must be fixed before merge. Example: plan's
  File Structure claims `internal/auth/session.go` exists but no such file was
  added; plan's Test plan names a test that isn't present in the diff.
- **IMPORTANT** — a plan requirement is implemented but with silent deviation
  (different file path, different function name, different public shape) that is
  likely intentional but unverified. Should be fixed, or the deviation
  explicitly acknowledged by the implementer. Example: plan says
  `ResolveAgentID()` but code has `GetAgentID()` with the same behavior —
  probably fine, but nobody said so. Also: files or behavior present in the
  implementation diff with no corresponding entry in the plan's File Structure
  table — unplanned additions the coordinator should review for scope creep.
- **MINOR** — missing documentation reference, commit-message format drift, or
  stylistic plan deviation that does not affect behavior. Example: plan calls
  for `Refs thrum-s9q9.3` in commit body, commit just has the title.

When choosing between BLOCKING and IMPORTANT, apply the test: _would a reader of
the merged code be surprised by this?_ BLOCKING = yes, definitely; IMPORTANT =
maybe, needs explanation.

## Invariant: stdout only, no inline edits

Findings go to stdout only. Do NOT:

- Create TodoWrite entries, beads issues, or worklog files inline —
  consolidation into the dual-review batch is the coordinator's job.
- Edit the code or the plan in response to findings — that is the implementer's
  next step after receiving the consolidated review.
- Modify git state (no commits, no stashes, no checkouts).

The skill reports; the coordinator decides; the implementer fixes. Keep those
three roles separate or the dual-review batch stops composing cleanly with
`feature-dev:code-reviewer`.

## Integration

When an implementer pushes back on a finding from this skill, apply the
pushback-and-verify protocol from
`~/.claude/CLAUDE.md § Verification Discipline` — re-read the cited file at the
cited lines, confirm the quoted plan requirement is verbatim, and confirm the
implementation state claim is accurate. Findings that don't survive that check
must be withdrawn or downgraded before the coordinator consolidates the
dual-review batch. Trace-corrections from the implementer are welcome signal,
not insubordination.

## Scope discipline

This skill has ONE job: does the code match what the plan said it would do?

It does NOT:

- **Judge code quality** — that's `feature-dev:code-reviewer`. Error handling,
  idioms, dead code, security patterns — not this skill's problem.
- **Perform security review** — that's `security-review`. SQL injection, XSS,
  auth bypass, secret handling — not this skill's problem.
- **Analyze test coverage** — coverage tools exist for that; this skill reports
  whether the plan's named test paths exist and pass, not whether they hit every
  branch.
- **Review the plan itself** — pre-implementation plan review is handled by
  `superpowers:writing-plans` (built-in reviewer). If the plan is wrong, that's
  a separate workflow.

If a finding would belong in any of the above categories, omit it from
verify-against-plan output. Coordinator will pick up quality / security /
coverage gaps via the other skills running in parallel.
