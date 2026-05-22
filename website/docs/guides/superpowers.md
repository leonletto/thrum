---
title: "Superpowers"
description:
  "Obra Superpowers is the upstream skills library Thrum's coordination
  workflows build on — brainstorming, writing-plans, TDD, and code review.
  Install it to run the brainstorm and project-setup pipelines."
category: "tools"
order: 4
tags:
  [
    "superpowers",
    "obra",
    "skills",
    "brainstorming",
    "writing-plans",
    "tdd",
    "recommended-tools",
    "setup",
  ]
last_updated: "2026-05-21"
---

## Superpowers

[Superpowers](https://github.com/obra/superpowers) is a skills library and
software-development methodology for coding agents, built by
[Jesse Vincent](https://blog.fsck.com) and the team at
[Prime Radiant](https://primeradiant.com). It ships a set of composable,
auto-triggering skills — brainstorming, planning, test-driven development, code
review — that turn a coding agent into a disciplined collaborator instead of one
that jumps straight to writing code.

Thrum's coordination skills are built **on top of** Superpowers. Several of them
— including the brainstorm cycle and `project-setup` — read inputs that only the
Superpowers skills produce. If Superpowers isn't installed, those workflows have
nothing to consume and the chain breaks at the first step.

> **Install Superpowers before running the brainstorm or project-setup
> pipelines.** It is a prerequisite, not an optional add-on, for the full
> coordination workflow.

## Why Thrum needs it

Thrum coordinates _who_ does the work — agents, messages, worktrees, review
gates. Superpowers supplies the _engineering discipline_ those agents apply
inside each step. Thrum's skills deliberately don't re-implement that
discipline; they complement it.

The hand-off chain looks like this:

1. **brainstorming** (Superpowers) refines a rough idea into a design document.
2. **writing-plans** (Superpowers) turns the approved design into a bite-sized
   implementation plan with exact file paths and verification steps.
3. **project-setup** (Thrum) reads that plan file and fans it out into Beads
   epics, tasks, dependencies, and per-epic implementer prompts.
4. Implementers execute under **test-driven-development** (Superpowers) and
   respond to review under **receiving-code-review** (Superpowers).

Skip Superpowers and step 3 has no plan file to read and no design doc to trace
against.

### How Thrum skills map to Superpowers skills

| Thrum skill                             | Builds on (Superpowers)                                            |
| --------------------------------------- | ------------------------------------------------------------------ |
| `coordinator-running-brainstorm-cycles` | `brainstorming`                                                    |
| `project-setup`                         | `writing-plans` (plan input), `brainstorming` (design)             |
| `verify-against-plan`                   | `writing-plans`                                                    |
| `implementer-tdd-and-quality`           | `test-driven-development`                                          |
| `implementer-receiving-review-feedback` | `receiving-code-review`                                            |
| `adversarial-critique`                  | `brainstorming`, `writing-plans`, `verification-before-completion` |
| `efficient-multi-agent-research`        | `dispatching-parallel-agents`                                      |
| `orchestrate`                           | `requesting-code-review` / code-review                             |

## Installation

Install Superpowers separately for each coding agent you use. Full per-harness
instructions live in the
[Superpowers README](https://github.com/obra/superpowers#installation).

### Claude Code

From Anthropic's official marketplace:

```bash
/plugin install superpowers@claude-plugins-official
```

Or from the Superpowers marketplace (also carries `elements-of-style`,
`episodic-memory`, and other related plugins):

```bash
/plugin marketplace add obra/superpowers-marketplace
/plugin install superpowers@superpowers-marketplace
```

### Codex (CLI and App)

Superpowers is in the official Codex plugin marketplace. In the Codex CLI, open
`/plugins`, search for `superpowers`, and select **Install Plugin**. In the
Codex app, open **Plugins** in the sidebar and click `+` next to Superpowers.

### OpenCode

OpenCode uses its own plugin install. Tell OpenCode:

```text
Fetch and follow instructions from https://raw.githubusercontent.com/obra/superpowers/refs/heads/main/.opencode/INSTALL.md
```

### Other harnesses

Superpowers also supports Factory Droid, Gemini CLI, Cursor, and GitHub Copilot
CLI. See the [README](https://github.com/obra/superpowers#installation) for the
exact command for each.

## What's inside

The skills you'll lean on most with Thrum:

- **brainstorming** — Socratic design refinement; produces the design doc.
- **writing-plans** — detailed, junior-engineer-proof implementation plans.
- **test-driven-development** — enforced RED-GREEN-REFACTOR.
- **requesting-code-review** / **receiving-code-review** — the review loop.
- **using-git-worktrees** — isolated branches for parallel work.
- **dispatching-parallel-agents** — concurrent subagent workflows.
- **verification-before-completion** — prove it's actually done.

## Related

- [Recommended Tools](recommended-tools.md) — the full list of tools that pair
  well with Thrum.
- [Beads and Thrum](../beads-and-thrum.md) — the task-tracking layer
  `project-setup` writes into.
- [Workflow Templates](../workflow-templates.md) — Thrum's pre-built skill
  pipelines.
- [Multi-Agent Coordination](../multi-agent.md) — how the coordinated workflow
  fits together end to end.
