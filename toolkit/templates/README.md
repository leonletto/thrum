# Workflow Templates for AI Agent Development

Thrum ships reusable template sets for common agent workflows. Each subfolder is a self-contained template set with its own CLAUDE.md explaining usage.

## Available Template Sets

| Name | Folder | Description |
|------|--------|-------------|
| Agent Dev Workflow | [`agent-dev-workflow/`](agent-dev-workflow/) | Three-phase workflow (Plan → Prepare → Implement) for feature development with AI agents using Beads for task tracking and git worktrees for isolation |

## How to Use

1. **Browse template sets** — Each subfolder contains a complete workflow template set
2. **Read the CLAUDE.md** — Each set's CLAUDE.md explains the workflow, placeholders, and how templates fit together
3. **Copy into your project** — Copy a template set into your project's docs/ or reference directly
4. **Fill in placeholders** — All templates use `{{PLACEHOLDER}}` syntax for project-specific values
5. **Hand off to agents** — Give filled-in templates to planning or implementation agents

## What's Inside Each Template Set

Template sets typically include:

- **CLAUDE.md** — Overview of the workflow, how to fill placeholders, and the relationship between templates
- **Phase templates** — Individual markdown files for each phase of the workflow (e.g., planning, preparation, implementation)
- **Supporting files** — Additional templates for coordination, preambles, or specialized tasks

## Agent Dev Workflow

The `agent-dev-workflow/` template set implements a proven three-phase process:

1. **Plan** — Brainstorm, write design specs, create Beads epics and tasks with detailed descriptions
2. **Prepare** — Set up isolated git worktrees with shared issue tracking via Beads redirects
3. **Implement** — Execute tasks autonomously with support for resuming after context loss

This workflow is designed for:
- Feature development requiring multiple implementation sessions
- Work that benefits from isolation (separate branches per epic)
- Teams using Beads for issue tracking and Thrum for agent coordination
- Scenarios where agents need to resume work after hitting context limits

See [`agent-dev-workflow/CLAUDE.md`](agent-dev-workflow/CLAUDE.md) for complete documentation.

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
