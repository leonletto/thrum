---
name: implementer-receiving-dispatch
description:
  "Use when receiving a new task from the coordinator, starting implementation,
  scoping a fresh task, or receiving dispatch. Loads implementer-specific
  discipline for kicking off work cleanly."
# source: claude-plugin/skills/implementer-receiving-dispatch/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Implementer: Receiving Dispatch

### Read the full implementation prompt before any tool call

**Why:** The dispatch message is the source of truth for scope, acceptance
criteria, file paths, and the worktree to work in. Skimming the first paragraph
and starting work means you'll miss either a constraint (the "don't do X" lines)
or a piece of context (the spec/plan paths). Both lead to wasted work that has
to be redone after review. (Source: findings_teamfix.md — "Read the full
implementation prompt before any tool call".)

**How to apply:** When a dispatch lands in your inbox, read it end-to-end before
opening any file or running any command. Read the linked spec and plan paths to
the section relevant to your starting task. Only after that read should you
claim the task and begin tool calls. If anything in the prompt is unclear or
contradictory, reply `NEEDS_CONTEXT` rather than guessing.

### Search for existing abstractions before writing new helpers

**Why:** A common failure mode is hand-rolling a helper for something the
codebase already has. (Source: findings_implementer.md — virtual-supervisor
2026-04-17: implementer wrote new `exec.CommandContext` blocks with custom
timeout/`#nosec` annotations when `safecmd.GitConfig` already existed and was
used in two other call sites. The reinvented code was deleted in review.) The
cost of the grep is seconds; the cost of reinvention is a review round-trip plus
a delete-and-replace patch.

**How to apply:** Before writing any new helper (path resolution, exec wrappers,
config loading, string sanitization, validation), run a targeted grep.
`grep -rn "FunctionNameHint\|relatedPattern" internal/` takes 2 seconds. If
something similar exists, use it. If it needs extension, extend it. Only create
a new function when nothing exists.

### Don't refactor for free — log opportunities, don't implement them

**Why:** A bug fix doesn't need surrounding cleanup. A one-shot operation
doesn't need a helper. Inlining "improvements" into a scoped task expands the
diff, complicates review, and risks introducing bugs unrelated to the work.
Three similar lines is better than a premature abstraction. (Source:
findings_teamfix.md — "File refactoring opportunities, do not implement them";
thrum-pxz dispatch named `thrum-xir` as the filing destination.)

**How to apply:** When you spot duplicated patterns, hardcoded values that
should be shared, or missed abstractions during implementation, log them to the
project's refactor backlog (typically a beads epic like `thrum-xir`):

```bash
bd create --title="Refactor: <short description>" --type=task \
  --parent=<refactor-epic-id> --priority=3 \
  --description="**Discovered during:** <task-id>
**Files:** <paths>
**Opportunity:** <what could be improved>
**Effort:** small/medium/large"
```

Then continue with the assigned work. Do not implement the refactoring.

### Flag scope deviations immediately — not in the final report

**Why:** If the assigned scope diverges from what you actually need to do
(missing context, wrong file paths, requirement implies more work than
described), the right time to surface it is the moment you notice — not buried
in a `DONE_WITH_CONCERNS` after hours of work in the wrong direction. Late
surfacing wastes the time between noticing and reporting. (Source:
findings_teamfix.md — "Scope deviations are flagged immediately, not buried";
spec deliberately tightened the rule from team-fix's "flag at the same message
that reports completion" to "the moment you notice".)

**How to apply:** The moment you spot a scope deviation, send a `NEEDS_CONTEXT`
or `BLOCKED` message to the coordinator with the specific question and your
proposed alternative. Wait for confirmation before expanding scope unilaterally.
Coordinator-confirmed scope expansions become the new acceptance criteria;
un-confirmed expansions risk being unwound in review.

### Verify spec/plan paths exist before starting

**Why:** Dispatch prompts reference spec and plan files in `dev-docs/specs/` and
`dev-docs/plans/`. If the paths don't exist (older session, mistyped path, file
moved), you'll either start without the authoritative source of truth or burn
time hunting for it. (Source: findings_implementer.md — "Specs always live in
dev-docs/specs/, plans in dev-docs/plans/".)

**How to apply:** First tool call after reading a dispatch should verify the
referenced paths: `ls /path/to/dev-docs/specs/<file>.md`. If anything is
missing, reply `NEEDS_CONTEXT` with the missing path. Don't try to infer from
related files.

### Project-specific rules (already loaded)

Project-local rules under `bd memories implementer-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key implementer-rule-<slug> "<rule + Why + How to apply>"`.

### Pattern D self-write — set `agent_status=working` on dispatch ACK (thrum-9neg)

Immediately after sending the dispatch ACK, write `agent_status="working"` to
your local identity file. This is the Pattern D self-write that makes
`agent_status` carry signal across the fleet — the sweep script +
coordinator-context-monitoring skill use it to flag agents that go tmux-quiet
despite claiming `working` (STUCK-WORKING classification per thrum-9neg L5).

```bash
# Step 1: ACK the dispatch within 2 minutes
thrum reply <MSG_ID> "Received. Starting <scope>. ETA <rough>."

# Step 2: Mark yourself working (writes local identity file directly; no daemon round-trip)
thrum agent set-status working
```

The local-write path at `cmd/thrum/agent.go:671-690` updates your own identity
file only — coord overrides remote agents via `--agent <name>` per L1. The
companion `set-status idle` call on DONE handoff lives in the
`implementer-status-and-handoff` skill.
