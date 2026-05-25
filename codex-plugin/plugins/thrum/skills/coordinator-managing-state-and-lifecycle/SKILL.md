---
name: coordinator-managing-state-and-lifecycle
description:
  "Use when ending a session, when updating project state, when managing beads
  epics, or before session close. Loads coordinator-specific discipline for
  owning project state and shepherding the team's lifecycle."
# source: claude-plugin/skills/coordinator-managing-state-and-lifecycle/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Coordinator: Managing State and Lifecycle

### Project state is the coordinator's exclusive responsibility

**Why:** Project state captures session-level context (what shipped, what broke,
what's next) and feeds the next session's priming. If implementers update it,
role separation collapses: the implementer's view of state is their epic, not
the team's. (Source: findings_coordinator.md — "Project state is the
coordinator's responsibility — implementers don't update it".)

**How to apply:** Only the coordinator runs `$thrum-update-project` or edits
`.thrum/context/project_state.md`. If an implementer is about to restart and
asks how to preserve context, instruct them to send a status message to you and
wait — you update the state on their behalf. Never run `thrum context save`
manually; it overwrites accumulated session state.

### Specs and plans always go in `dev-docs/specs/` and `dev-docs/plans/`

**Why:** Worktree-local paths aren't shared with the coordinator or other
agents, and `docs/superpowers/` was added to `.gitignore` — specs written there
became invisible to anyone outside the writing worktree. (Source:
findings_coordinator.md — "Specs always go in dev-docs/specs/ — not
worktree-local paths".)

**How to apply:** When writing a spec, plan, or design doc, use an absolute path
under the main repo's `dev-docs/specs/` (specs) or `dev-docs/plans/` (plans).
Confirm the path before writing. If a doc was previously written to a
worktree-local path, move it before referencing it in dispatch messages.

### Push the coordination branch before ending a session

**Why:** Unpushed work is stranded locally and invisible to other agents and
machines. If the machine shuts down, the session crashes, or a new worktree is
created from origin, unpushed commits can be lost or inaccessible. (Source:
findings_coordinator.md — "Push before ending any session — unpushed work is
unprotected".)

**How to apply:** Session close protocol — push the coordination branch (in this
project, `thrum-dev`) every session end. Feature/fix branches follow the
project's branch-push policy: in some projects they stay local until the
coordinator merges them; in others they're pushed for backup or review. Read the
project's CLAUDE.md branch-push policy before pushing any branch other than the
coordination branch.

### Use beads dependency-direction syntax correctly

**Why:** `bd dep <blocker> --blocks <blocked>` reads naturally — the blocker
blocks the blocked task. The reversed form `bd dep add <blocked> <blocker>`
exists but flips argument order, and getting it backwards silently inverts the
dependency graph. Inverted deps make `bd ready` return wrong tasks and create
circular blocking that no UI flags.

**How to apply:** Use the verb form:
`bd dep <blocker_id> --blocks <blocked_id>`. Read it as "blocker blocks
blocked." Always verify with `bd dep tree <epic>` after creating a dependency
batch — the visual tree exposes inversion immediately. For bulk epic creation
(many tasks at once), spawn parallel sub-agents with `model: "haiku"` to issue
the `bd create` calls; bd is fully concurrent-safe.

### Use `bd close --suggest-next` to surface unblocked work

**Why:** Closing a task often unblocks downstream tasks, but you only see the
new ready set on the next `bd ready` call — and by then the context of "what
just freed up" is lost. `bd close <id> --suggest-next` prints newly-ready issues
immediately after the close, so dispatch is one decision away.

**How to apply:** When closing a task that has dependents, use
`bd close <id> --suggest-next`. For batch closes (`bd close <id1> <id2> <id3>`),
re-run `bd ready` after the batch — the cumulative effect of multiple closes is
hard to predict.

### Restart discipline — burn the runway, don't pre-empt

**Why:** Restarting at a "clean checkpoint" feels safe, but most checkpoints
aren't truly clean — there's always one more thing in flight. Pre-empting wastes
the remaining context window and re-incurs restart-cost (re-prime, re-orient,
re-load state). The right move is to burn the runway up to the configured
threshold, then restart. (Source: feedback_restart_discipline.md.)

**How to apply:** Don't restart at the first natural pause. Check the configured
restart threshold (`thrum config show restart`) and let the session run until
you actually approach it. If you're under the threshold and have a coherent task
to advance, advance it.

### Destroy an agent before tearing down its worktree

**Why:** `thrum worktree teardown <name>` does exactly what its name says — it
tears down the **worktree** — and is intentionally not coupled to runtime
lifecycle. If the agent's runtime is still running in tmux when you teardown,
the runtime keeps executing zombie-style with its cwd pointing at a deleted
directory. The next operation the user attempts in that tmux pane fails
confusingly, and they have to manually `tmux kill-session` and exit. (Source:
direct user correction — coordinator had reported an agent "removed" after
running only the worktree teardown.)

**How to apply:** When recycling an agent (epic done, agent should be removed),
run the two-command destroy sequence **in this order**:

```bash
thrum tmux kill <session>          # kills tmux + runtime first
thrum worktree teardown <name>     # then removes worktree + identity
```

Sanity-check: after teardown, `tmux list-sessions` should NOT show the agent's
session. If it does, the runtime was never killed and step 1 was skipped.

For batch recycling (multiple agents at once), kill them all first, then
teardown all worktrees — keeps the daemon's session state consistent across the
operation. For graceful shutdown of in-flight agents, send a thrum message
asking them to save state, wait for ack, then run the two-command sequence.
Done/idle agents on closed epics need no graceful shutdown.

### Project-specific rules (already loaded)

Project-local rules under `bd memories coordinator-rule-` were loaded at session
start by your preamble. If a project-local rule conflicts with a universal rule
above, the project-local rule wins; surface the conflict in your reply so the
user can decide whether to graduate or remove the override.

If you accumulate a new rule mid-session (the user corrects you), capture it via
`bd remember --key coordinator-rule-<slug> "<rule + Why + How to apply>"`.
