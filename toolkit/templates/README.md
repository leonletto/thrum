# Workflow Templates for AI Agent Development

Thrum ships reusable template sets for common agent workflows. Each subfolder is
a self-contained template set with its own CLAUDE.md explaining usage.

## Available Template Sets

| Name               | Folder                                       | Description                                                                                                                                                        |
| ------------------ | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Agent Dev Workflow | [`agent-dev-workflow/`](agent-dev-workflow/) | Four-phase skill pipeline (Design → Plan → Setup → Implement) for feature development with AI agents using Beads for task tracking and git worktrees for isolation |
| Role Templates     | [`roles/`](roles/)                           | Behavioral preamble templates for 9 agent roles × 2 autonomy levels (18 templates total). Auto-applied on agent registration via `thrum quickstart`.               |

## How to Use

1. **Browse template sets** — Each subfolder contains a complete workflow
   template set
2. **Read the CLAUDE.md** — Each set's CLAUDE.md explains the workflow,
   placeholders, and how templates fit together
3. **Copy into your project** — Copy a template set into your project's docs/ or
   reference directly
4. **Fill in placeholders** — All templates use `{{PLACEHOLDER}}` syntax for
   project-specific values
5. **Hand off to agents** — Give filled-in templates to planning or
   implementation agents

## What's Inside Each Template Set

Template sets typically include:

- **CLAUDE.md** — Overview of the workflow, how to fill placeholders, and the
  relationship between templates
- **Phase templates** — Individual markdown files for each phase of the workflow
  (e.g., planning, preparation, implementation)
- **Supporting files** — Additional templates for coordination, preambles, or
  specialized tasks

## Agent Dev Workflow

The `agent-dev-workflow/` template set implements a four-phase skill pipeline:

1. **Design** — Explore codebase, brainstorm approaches, write design spec
   interactively (brainstorming skill)
2. **Plan** — Structure the design into phased implementation steps with task-ID
   anchors (writing-plans skill)
3. **Setup** — Decompose plan into Beads epics/tasks, select worktrees, generate
   filled implementation prompts (project-setup skill)
4. **Implement** — Execute tasks autonomously with support for resuming after
   context loss (implementation-agent.md template)

This workflow is designed for:

- Feature development requiring multiple implementation sessions
- Work that benefits from isolation (separate branches per epic)
- Teams using Beads for issue tracking and Thrum for agent coordination
- Scenarios where agents need to resume work after hitting context limits

See [`agent-dev-workflow/CLAUDE.md`](agent-dev-workflow/CLAUDE.md) for complete
documentation.

## Role Templates

The `roles/` directory contains behavioral preamble templates for AI agents.
Each role has two variants:

- **Strict** — Agent waits for coordinator instruction, limited scope, reports
  everything
- **Autonomous** — Agent can self-assign tasks, broader scope, independent
  decision-making

### Available Roles

| Role        | Purpose                                             | Worktree Pattern          |
| ----------- | --------------------------------------------------- | ------------------------- |
| coordinator | Orchestrates team, dispatches tasks, reviews/merges | Main repo (not detached)  |
| implementer | Writes code in assigned worktree                    | Own feature branch        |
| planner     | Creates plans, designs architecture, writes specs   | Own branch or main        |
| researcher  | Investigates codebases, produces research reports   | Detached HEAD (read-only) |
| reviewer    | Reviews code for quality, security, correctness     | Detached HEAD (read-only) |
| tester      | Writes and runs tests, verifies acceptance criteria | Own feature branch        |
| deployer    | Handles builds, releases, deployment operations     | Main repo or ops branch   |
| documenter  | Creates and maintains documentation                 | Own branch or main        |
| monitor     | Watches system health, reports anomalies            | Main repo (read-only)     |

### Template Structure

Every role template includes these sections:

1. **Operating Principle** — Core behavioral anchor with role-specific traps
2. **Anti-Patterns** — Universal (Deaf Agent, Silent Agent, Context Hog) plus
   role-specific anti-patterns
3. **Startup Protocol** — Mandatory ordered checklist
4. **Identity & Authority** — CAN/CANNOT lists
5. **Scope Boundaries** — Worktree restrictions
6. **Task Protocol** — Role-specific workflow steps
7. **Communication Protocol** — Messaging rules and examples
8. **Message Listener** — Background listener requirement
9. **Idle Behavior** — What to do when waiting
10. **Critical Reminders** — Top 5 rules repeated for emphasis

Templates use Go template variables: `{{.AgentName}}`, `{{.Role}}`,
`{{.Module}}`, `{{.WorktreePath}}`, `{{.RepoRoot}}`, `{{.CoordinatorName}}`.

### How to Use

```bash
# Generate role templates for your project
/thrum:configure-roles

# Or copy manually
cp toolkit/templates/roles/implementer-strict.md .thrum/role_templates/implementer.md

# Deploy to registered agents
thrum roles deploy
```

## Creating Custom Template Sets

To contribute a new template set:

1. Create a new subfolder in `toolkit/templates/`
2. Include a CLAUDE.md that explains the workflow
3. Use `{{PLACEHOLDER}}` syntax for all project-specific values
4. Keep templates generic (no hardcoded paths, names, or credentials)
5. Document the workflow phases, placeholder meanings, and typical usage
6. Update this README with a new table entry

## Learn More

- See `toolkit/agents/` for Beads and Thrum agent integration guides
- See the Beads project repository for task tracking documentation
- See the Thrum project repository for multi-agent coordination documentation
