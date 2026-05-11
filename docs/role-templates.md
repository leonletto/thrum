## Role-Based Preamble Templates

## Overview

Role templates automatically generate agent preambles during registration.
Instead of every agent getting the same default preamble, each role gets a
customized set of behavioral directives — scope boundaries, task protocol,
communication patterns, idle behavior, and efficiency rules.

**Three layers:**

1. **Shipped examples** in `internal/context/roleconfig/templates/roles/` —
   reference material with strict and autonomous variants for each role
2. **Active templates** in `.thrum/role_templates/` — per-project templates that
   auto-apply on agent registration
3. **Claude skill** (`thrum:configure-roles`) — interactive environment-aware
   template generation

## Role Discipline Lives in Two Places

The preamble is always loaded, but it can't carry every situational rule without
becoming a wall of text nobody reads. Thrum splits role discipline across two
surfaces.

**Always-loaded (preambles).** Identity, scope boundaries, communication
protocol, idle behavior, anti-patterns. Things the agent must know before it
does anything.

**Description-triggered (skills).** Situational deepening that loads only when
relevant. Each role has its own set:

| Role        | Skills                                                                                                                                     |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Coordinator | `coordinator-dispatching-work`, `coordinator-running-review-cycles`, `coordinator-managing-state-and-lifecycle`                            |
| Implementer | `implementer-receiving-dispatch`, `implementer-tdd-and-quality`, `implementer-status-and-handoff`, `implementer-receiving-review-feedback` |
| Researcher  | `researcher-investigating`, `researcher-answering-queries`, `researcher-maintaining-memory`                                                |

The preamble points at these by name. The runtime loads the skill body when its
description matches what the agent is doing, so you get focused guidance when
you need it without paying for it the rest of the time.

**Project-local rules.** Anything you tell an agent mid-session — "stop doing
X", "always do Y here" — captured via `bd remember --key <role>-rule-<slug>`
persists across restarts. Each preamble loads project-local rules with
`bd memories <role>-rule-`. They take precedence over universal rules on
conflict, so a project-specific correction beats a generic shipped instruction.
Module-installed rules reserve the `<role>-rule-mod-<module>-<slug>`
sub-segment.

## How It Works

When an agent registers via `thrum quickstart --role implementer`, the system
checks for `.thrum/role_templates/implementer.md`. If found, it renders the
template with the agent's identity data and saves it as the agent's preamble.

**Precedence:**

1. `--preamble-file` flag (highest — explicit user choice)
2. Role template in `.thrum/role_templates/{role}.md`
3. Default preamble (fallback)

## Template Variables

Templates use Go `text/template` syntax. Available variables:

| Variable               | Source                                              |
| ---------------------- | --------------------------------------------------- |
| `{{.AgentName}}`       | Agent identity (e.g., "impl_auth")                  |
| `{{.Role}}`            | Agent role (e.g., "implementer")                    |
| `{{.Module}}`          | Agent module (e.g., "auth")                         |
| `{{.WorktreePath}}`    | Resolved from identity file                         |
| `{{.RepoRoot}}`        | Parent of .thrum/ directory                         |
| `{{.CoordinatorName}}` | First agent with role=coordinator, or "coordinator" |

## Template Structure

Every role template follows the same section structure:

```markdown
# Agent: {{.AgentName}}

**Role:** {{.Role}} **Module:** {{.Module}} **Worktree:** {{.WorktreePath}}

## Identity & Authority

## Scope Boundaries

## Task Protocol

## Communication Protocol

## Message Listener

## Task Tracking

## Efficiency & Context Management

## Idle Behavior

## Anti-Patterns

## Project-Specific Rules
```

## Shipped Examples

Reference templates in `internal/context/roleconfig/templates/roles/`:

| File                                 | Description                                                                                                       |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| `coordinator-strict.md`              | All task assignment flows through coordinator                                                                     |
| `coordinator-autonomous.md`          | Coordinator orchestrates, agents can self-assign                                                                  |
| `implementer-strict.md`              | Waits for explicit task from coordinator                                                                          |
| `implementer-autonomous.md`          | Can pick up ready tasks from issue tracker                                                                        |
| `implementer-worktree-write-only.md` | Pins implementer writes to their own worktree; forbids drive-by edits to the main repo (wizard "enhanced" choice) |
| `planner-strict.md`                  | Read-only exploration, writes plans to docs                                                                       |
| `planner-autonomous.md`              | Can create issues and break down epics                                                                            |
| `researcher-strict.md`               | Read-only, responds to research requests                                                                          |
| `researcher-autonomous.md`           | Can proactively research when idle                                                                                |
| `reviewer-strict.md`                 | Reviews only assigned PRs/changes                                                                                 |
| `reviewer-autonomous.md`             | Can pick up review requests proactively                                                                           |
| `tester-strict.md`                   | Runs tests on request, reports results                                                                            |
| `tester-autonomous.md`               | Can proactively run tests on changed files                                                                        |
| `deployer-strict.md`                 | Deploys only on explicit coordinator approval                                                                     |
| `deployer-autonomous.md`             | Can deploy to non-production environments freely                                                                  |
| `documenter-strict.md`               | Documents only assigned areas                                                                                     |
| `documenter-autonomous.md`           | Can proactively update docs when code changes                                                                     |
| `monitor-strict.md`                  | Reports alerts, takes no remediation action                                                                       |
| `monitor-autonomous.md`              | Can restart services and open issues on alerts                                                                    |
| `orchestrator.md`                    | Launches agents, manages worktrees, runs review-gated epics (single variant)                                      |

> **`monitor-*.md` vs `thrum monitor`:** These role templates configure _agent
> behavior_ for agents whose job is monitoring (e.g., watching logs, reporting
> alerts). They're unrelated to
> `thrum monitor start/list/show/stop/logs/restart`, which is a separate daemon
> feature for running long-lived process monitors that emit synthetic thrum
> messages. See [Monitor Jobs](monitor-jobs.md) for the process monitor feature.

## CLI Commands

### thrum roles list

List configured templates and matching agents:

```bash
thrum roles list
# coordinator.md    (2 agents: coord_main, coord_api)
# implementer.md    (3 agents: impl_auth, impl_payments, impl_ui)
# planner.md        (0 agents)
```

### thrum roles refresh

Re-render `.thrum/role_templates/<role>.md` from saved `role_config` answers
(set by `/thrum:configure-roles`) without running interactive prompts. Uses the
embedded shipped templates plus the saved `autonomy` and `scope` per role.
Updates `rendered_hash` to the current shipped `body_hash` so drift hints from
`thrum prime` clear after the next run.

Per-agent template tokens (`{{.AgentName}}` etc.) are kept literal — the
existing per-agent deploy pass substitutes them when you run
`thrum roles deploy`.

```bash
thrum roles refresh
```

Run this after upgrading Thrum when `thrum prime` emits a
`roles.config.schema-bump` or `roles.config.body-diff` hint.

### thrum roles deploy

Re-render preambles for registered agents from role templates:

```bash
thrum roles deploy              # Deploy for all agents
thrum roles deploy --agent foo  # Deploy for a specific agent
thrum roles deploy --dry-run    # Preview what would change
```

Deploy is a full overwrite — templates are the source of truth.

## Setting Up

### Manual setup

1. Copy a shipped example to `.thrum/role_templates/`:

```bash
cp internal/context/roleconfig/templates/roles/implementer-autonomous.md .thrum/role_templates/implementer.md
```

1. Edit to customize for your project.

1. Register agents — templates auto-apply:

```bash
thrum quickstart --name impl_auth --role implementer --module auth
```

### Interactive setup (Claude Code)

Run the configure-roles skill:

```bash
/thrum:configure-roles
```

The skill detects your environment (runtimes, worktrees, beads, MCP servers) and
generates customized templates interactively.

## File Layout

```text
.thrum/
├── role_templates/           # Active templates (per-project)
│   ├── coordinator.md
│   ├── implementer.md
│   └── planner.md
├── context/
│   ├── impl_auth.md          # User overlay (hand-authored; appended to preamble)
│   ├── impl_auth_preamble.md # Rendered output (per-agent)
│   └── coord_main_preamble.md
└── identities/
    ├── impl_auth.json
    └── coord_main.json
```

`.thrum/context/<agent>.md` has a dual purpose: it stores volatile session state
written by `thrum context save`, and it acts as a **user overlay** that
`RenderRoleTemplate` appends to the rendered preamble after a `---` separator.
`thrum quickstart` auto-creates the file empty so it's ready for hand-written
customization. Whitespace-only files are treated as absent (no stray separator
is added). See [Agent Context Management](context.md) for details.

## Next Steps

- [Context Management](context.md) — per-agent context and preamble files that
  role templates generate into
- [Identity System](identity.md) — how agents are registered and how role
  templates are applied during `thrum quickstart`
- [Claude Code Plugin](claude-code-plugin.md) — the `/thrum:configure-roles`
  slash command for interactive template generation
- [Workflow Templates](workflow-templates.md) — the broader skill pipeline that
  role templates plug into for implementation agents
