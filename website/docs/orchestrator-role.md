---
title: "Orchestrator Role"
description:
  "Hand off a plan you wrote and let the orchestrator run the execution phase"
category: "orchestration"
order: 1
tags: ["orchestrator", "roles", "automation", "plans"]
last_updated: "2026-04-19"
---

## What the Orchestrator Is

The orchestrator is a Thrum role that takes a plan you already wrote, spins up
implementer agents in tmux sessions, runs execution epic-by-epic with review
gates, and hands back a merge report when it's done. It doesn't write code. It
doesn't plan work. It runs plans you approved. You write the spec, the
coordinator turns it into epics and an implementation prompt, and then you hand
that work to the orchestrator — which manages every agent session, monitors
progress, dispatches code reviews, and stops at the gates you defined. One agent
doing the dispatch loop so you don't have to.

The `thrum:orchestrate` Claude Code skill is the implementation of this
playbook. It guides the orchestrator through each phase — from accepting the
handoff to delivering the final merge report.

---

## When to Use It

You've got a plan. Beads has the epics. The implementation prompt exists with
review gates between each epic. What's left is pure babysitting: create
worktrees, launch sessions, send prompts, watch for completion, run reviews,
repeat for each epic.

That's the sweet spot for the orchestrator. It doesn't help with research or
planning — that's the coordinator's job. It starts where the coordinator stops:
at "plan approved, implementation not started."

If you're running a single epic with one agent and you want to watch it closely,
manual management is probably fine. If you've got three epics, two parallel
workstreams, and you'd rather not be the message relay, hand it to the
orchestrator.

The orchestrator is a **pre-configured, human-launched agent** — you set it up
in its own tmux session ahead of time. The coordinator doesn't launch it. You
do. That's intentional. See [Tmux-Managed Sessions](tmux-sessions.md) for how to
get it running.

---

## Autonomy Levels

When you hand the orchestrator a plan, it asks you one question before touching
anything: how much autonomy do you want?

| Level      | What it does                                                                        |
| ---------- | ----------------------------------------------------------------------------------- |
| `per_epic` | Stops after each epic, presents findings, waits for your go-ahead before continuing |
| `end_only` | Runs all epics, stops only at the final merge report                                |

There's no `full_auto`. The orchestrator never merges without your approval. The
whole point of working this way is that you understand what got built — you
can't do that if code appears on main while you were away.

`per_epic` is for when you want to stay close. Each epic gate is a checkpoint:
you see what passed review, you decide whether to continue. `end_only` is for
when you trust the plan and want to come back to a finished report.

The default is `end_only`. You can change that in config, or just answer
differently when the orchestrator asks. It negotiates per-run, not once
globally.

---

## Handoff: Giving the Orchestrator a Plan

The orchestrator validates the handoff before it does anything. It won't start
with a half-ready plan — it sends back a specific list of what's missing and
stops.

**What the orchestrator expects:**

1. **A plan file** — the markdown file from `dev-docs/plans/` that describes the
   work
2. **Beads epics with tasks** — the coordinator creates these with `bd create`;
   the orchestrator checks them with `bd show <epic>` and `bd dep tree <epic>`
3. **An implementation prompt** — the file from
   `claude-plugin/skills/project-setup/`, with `## Review Gate:` sections
   between epics. No review gates → rejected handoff
4. **Merge target** — confirmed in `.thrum/config.json` under
   `orchestration.merge_target`

**Before handing off, verify:**

```bash
# Epics exist and have tasks
bd show <epic-id>
bd dep tree <epic-id>

# Review gates are present in the prompt
grep "## Review Gate:" path/to/implementation-prompt.md

# Merge target is set
cat .thrum/config.json | grep merge_target
```

If any of those fail, fix them before sending the plan path to the orchestrator.
The coordinator's `project-setup` skill generates review-gate-ready prompts
automatically — if you used it, you're probably good.

The coordinator sends the plan to the orchestrator via Thrum messaging:

```bash
thrum send "Plan ready at dev-docs/plans/2026-04-09-my-feature.md" --to @orchestrator_main
```

---

## Execution Lifecycle

The orchestrator runs five phases. Here's what each phase does and what you see.

### Phase 1 — Validate Handoff

Reads the plan file and design doc. Checks that beads epics exist, that the
implementation prompt has `## Review Gate:` headings between epics, that
dependencies are configured, and that the merge target is set.

If anything is missing, it sends a specific feedback message to the coordinator
and stops. You don't see a half-started execution.

### Phase 2 — Configure Execution

Asks two questions (or one, if config has a default):

1. **Autonomy level** — `per_epic` or `end_only`?
2. **Runtime selection** — if your team runs a single runtime it uses it
   silently; if you've got mixed runtimes registered, it asks which to use per
   workstream

It also presents the worktree plan: which epics run in parallel, how many agents
it needs to create, what their names will be. You see this before any session
gets created.

### Phase 3 — Launch Agents

For each agent it needs:

```bash
# creates worktree + beads/thrum setup
thrum worktree create <name>

# creates the tmux session + registers agent identity in one step
thrum tmux create <name> --cwd <path> \
  --name <agent_name> --role implementer --module <mod>
# alias: thrum tmux quickstart <name> --cwd <path> --name <agent_name> ...

# boots the runtime
thrum tmux launch <name>

# marks it ready
thrum agent set-status idle
```

The old pattern — create the session, then separately run `thrum tmux send` to
execute quickstart inside — is gone. Passing `--name`, `--role`, and `--module`
to `thrum tmux create` handles registration automatically.

After launch it verifies every session is alive via `thrum tmux status` and
reports back. No surprises — you know all agents are running before work starts.

### Phase 4 — Execute (Epic Loop)

For each epic batch (which may run in parallel if epics are independent):

1. Sends the implementation prompt section to each agent via `thrum send`
2. Marks agents `working`
3. Sets its own status to `idle` and waits for inbox messages
4. As agents complete epics, dispatches a code review sub-agent for each
5. If review passes → closes beads tasks, checks `bd ready` for the next batch
6. If autonomy is `per_epic` → stops here, presents the epic report, waits for
   your go-ahead

Review blockers loop back to the agent (up to 3 rounds). If after 3 rounds
review still fails, the orchestrator escalates to you.

### Phase 5 — Finalize

All epics complete. The orchestrator:

1. Runs a final cross-branch review — diffs each agent branch against the merge
   target, checks for conflicts between branches touching the same files
2. Prepares the merge report: changes per branch, all review results, test
   results the agents reported during their gates, configured merge target
3. Presents the report to you

You approve → it merges to the merge target, pushes, and asks if you want
worktrees cleaned up. Then it sets itself to `idle`.

---

## Review Gates

Review gates are `## Review Gate:` headings in the implementation prompt. The
`project-setup` skill inserts one after each epic's task block. They look like
this:

```markdown
## Review Gate: thrum-abc

Before proceeding to the next epic:

1. Commit all work for this epic
2. Run tests: verify all tests pass for changes in this epic
3. Report completion via Thrum:
   `thrum send "Epic thrum-abc complete. Ready for review." --to @orchestrator_main`
4. Set status: `thrum agent set-status idle`
5. **STOP.** Wait for review approval before continuing.
```

When an agent hits a review gate, it sends the orchestrator a completion message
and stops. The orchestrator dispatches a code review sub-agent. The sub-agent
diffs the work against the spec, reports findings, and the orchestrator either
clears the agent to continue or sends findings back for fixes.

At `per_epic` autonomy, the orchestrator also surfaces the review results to you
before proceeding. At `end_only`, it continues automatically if review passes.

The orchestrator validates that `## Review Gate:` headings exist during Phase 1.
A prompt without gates won't run — the orchestrator bounces it back.

---

## Implementer Status Vocabulary

Implementers use a four-value status vocabulary when reporting back to the
coordinator or orchestrator. Without it, every "done-ish" status reads the same
and coordinators miss latent issues. With it, you can triage a batch of
completion messages at a glance.

| Status               | Meaning                                               | Coordinator action                                                                   |
| -------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `DONE`               | Work complete, no concerns.                           | Mark closed and continue.                                                            |
| `DONE_WITH_CONCERNS` | Work complete, but specific issues need attention.    | Read the listed concerns. Close, file a follow-up, or push back — don't ignore them. |
| `NEEDS_CONTEXT`      | Blocked on coordinator clarification.                 | Answer the specific questions before the agent resumes.                              |
| `BLOCKED`            | Cannot proceed until an external dependency resolves. | Decide whether to redirect work or wait out the blocker.                             |

**DONE_WITH_CONCERNS** requires the implementer to list each concern explicitly.
The orchestrator treats an unaddressed `DONE_WITH_CONCERNS` the same as an
unresolved review blocker — it doesn't clear the agent to continue until the
coordinator has responded.

**BLOCKED** requires the implementer to name the blocker (for example: "waiting
on team-fix sec.8 to land"). The orchestrator surfaces this to you and pauses
that workstream rather than letting the agent spin.

---

## Two-Stage Review

When you review implementer work — whether as the orchestrator dispatching a
code review sub-agent or as a coordinator reviewing directly — run the two
stages in this order:

**Stage 1 — Spec Compliance Review**: did the implementation cover every
requirement in the plan or spec? Run this first. Catching a scope miss early
costs nothing. Catching it after a clean code quality pass means the implementer
has to go back anyway.

**Stage 2 — Code Quality Review**: standards, error handling, idioms, dead code.
Only meaningful once compliance is confirmed.

Running both reviewers in parallel is fine for speed — but consolidate all
findings into **one numbered list** before forwarding to the implementer.
Sending partial findings is one of the most reliable ways to leave issues
permanently unfixed: the implementer fixes batch 1, reports `DONE`, and batch 2
disappears into the conversation history.

The orchestrator follows this discipline when dispatching code review sub-agents
at each review gate. If you're running reviews manually, it applies equally.

---

## Escalation

The orchestrator stops and messages you when it hits something it can't resolve:

- **Agent stuck after nudge** — agent status is `working`, pane is idle, a nudge
  was sent, and there's still no message after the next check. The orchestrator
  reports what the agent was doing, what the tmux pane shows, and asks what to
  do.
- **Session dies** — it attempts `thrum tmux restart` once; if that fails, it
  escalates with the session name and last-known state.
- **Review blockers after 3 rounds** — sends you the review findings and the
  agent's three attempts to fix them, asks whether to continue with exceptions
  or stop.
- **Dependency conflict between epics** — two parallel epics touch overlapping
  code in a way that will conflict at merge time.
- **Judgment call in the plan** — any ambiguity it can't resolve from the plan
  file alone.

Escalation messages include: what happened, what was tried, what specific
decision you need to make. Not "there was a problem." Something you can act on.

If you don't respond at a review gate, the orchestrator waits and sends a
reminder after a configured timeout. It doesn't proceed without your answer.

---

## Configuration

Two keys in `.thrum/config.json` control orchestrator behavior:

```json
{
  "orchestration": {
    "default_autonomy": "end_only",
    "merge_target": "main"
  }
}
```

**`default_autonomy`** — pre-selects the answer to the autonomy question at plan
start. Values: `per_epic`, `end_only`. Default: `end_only`. You can override at
runtime — the orchestrator always confirms before executing.

**`merge_target`** — the branch all agent branches merge to when you approve the
final report. Default: `main`. The orchestrator confirms this branch with you on
first activation. Change it here if you're working on a release branch or a
long-lived integration branch.

The `worktrees` key is also relevant — the orchestrator uses it to know where to
create agent worktrees:

```json
{
  "worktrees": {
    "base_path": "/Users/you/.workspaces/project",
    "beads_enabled": true,
    "thrum_enabled": true
  }
}
```

`thrum init` sets defaults for all of these. See
[Configuration](configuration.md) for the full config reference.

### Role Template

The orchestrator preamble lives at
`internal/context/roleconfig/templates/roles/orchestrator.md` in the Thrum repo.
It's the context file the orchestrator loads at startup via `thrum prime`. You
don't need to edit it. If you register an orchestrator agent with
`thrum quickstart --role orchestrator`, the right template loads automatically.

---

## Commands

**Register an orchestrator agent:**

```bash
thrum quickstart --role orchestrator --name orchestrator_main --module main
```

Run this in the orchestrator's worktree (a detached HEAD worktree under
`~/.workspaces/<project>/orchestrator/`). The orchestrator stays on detached
HEAD — it never commits. All code work happens in the agent worktrees it
creates.

**Set agent status:**

```bash
thrum agent set-status working    # mark yourself working
thrum agent set-status idle       # mark yourself idle
thrum agent set-status blocked    # mark yourself blocked

# Orchestrator sets a remote agent's status via daemon RPC:
thrum agent set-status working --agent impl_api
```

**Check team status:**

```bash
thrum tmux status    # session state + runtime + branch for all agents
thrum team           # registered agents with roles and statuses
```

See [CLI Reference](cli.md) for the full command reference, including
`thrum tmux` subcommands for session lifecycle management.

---

## Limitations

**The orchestrator doesn't create epics or tasks.** That's the coordinator's
job. If the beads structure isn't there when the orchestrator validates the
handoff, it sends back a specific list of what's missing and waits. Fix it in
the coordinator and re-send.

**The orchestrator doesn't write code or investigate codebases.** It manages
agents that do that work. If it needs information from the codebase, it
dispatches a sub-agent rather than reading source files itself.

**The orchestrator doesn't merge without your approval.** Phase 5 ends with a
merge report and a question. You answer it. This isn't configurable — there's no
autonomy level that skips the merge gate. The human always controls what lands
on the merge target.

**The orchestrator doesn't create the merge target branch.** If `merge_target`
is set to a branch that doesn't exist, the merge will fail. Confirm the branch
exists before handing off a plan.

---

## Next Steps

- [Tmux-Managed Sessions](tmux-sessions.md) — how to create and launch the
  orchestrator's tmux session, and how the nudge mechanism keeps agents
  responsive
- [Multi-Agent Support](multi-agent.md) — groups, team coordination, and the
  patterns the orchestrator relies on
- [CLI Reference](cli.md) — full reference for `thrum tmux`,
  `thrum agent set-status`, `thrum worktree`, and other commands
- [Configuration](configuration.md) — `orchestration` and `worktrees` config
  keys in full
