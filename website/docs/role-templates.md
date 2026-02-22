---
title: "Role-Based Preamble Templates"
description:
  "Auto-generate agent preambles from role templates with scope boundaries, task
  protocol, communication patterns, and behavioral directives"
---

## Role-Based Preamble Templates

## Overview

Role templates automatically generate agent preambles during registration.
Instead of every agent getting the same default preamble, each role gets a
customized set of behavioral directives — scope boundaries, task protocol,
communication patterns, idle behavior, and efficiency rules.

**Three layers:**

1. **Shipped examples** in `toolkit/templates/roles/` — reference material with
   strict and autonomous variants for each role
2. **Active templates** in `.thrum/role_templates/` — per-project templates that
   auto-apply on agent registration
3. **Claude skill** (`thrum:configure-roles`) — interactive environment-aware
   template generation

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

## Project-Specific Rules
```

## Shipped Examples

Reference templates in `toolkit/templates/roles/`:

| File                        | Description                                      |
| --------------------------- | ------------------------------------------------ |
| `coordinator-strict.md`     | All task assignment flows through coordinator    |
| `coordinator-autonomous.md` | Coordinator orchestrates, agents can self-assign |
| `implementer-strict.md`     | Waits for explicit task from coordinator         |
| `implementer-autonomous.md` | Can pick up ready tasks from issue tracker       |
| `planner-strict.md`         | Read-only exploration, writes plans to docs      |
| `planner-autonomous.md`     | Can create issues and break down epics           |
| `researcher-strict.md`      | Read-only, responds to research requests         |
| `researcher-autonomous.md`  | Can proactively research when idle               |

## CLI Commands

### thrum roles list

List configured templates and matching agents:

```bash
thrum roles list
# coordinator.md    (2 agents: coord_main, coord_api)
# implementer.md    (3 agents: impl_auth, impl_payments, impl_ui)
# planner.md        (0 agents)
```

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
cp toolkit/templates/roles/implementer-autonomous.md .thrum/role_templates/implementer.md
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
│   ├── impl_auth_preamble.md # Rendered output (per-agent)
│   └── coord_main_preamble.md
└── identities/
    ├── impl_auth.json
    └── coord_main.json
```
