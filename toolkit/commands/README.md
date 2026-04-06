# Thrum Toolkit Commands

Slash-command skills for Claude Code agents working with Thrum.

## What Are Commands?

Commands (also called "skills" or "slash commands") are markdown files that
Claude Code loads as `/command-name` actions. They provide structured guidance
for common workflows.

## Installation

Copy command files to `.claude/commands/` (project-level) or
`~/.claude/commands/` (global):

```bash
# Project-level (this repo only)
mkdir -p .claude/commands
cp toolkit/commands/update-context.md .claude/commands/

# Global (all projects)
mkdir -p ~/.claude/commands
cp toolkit/commands/update-context.md ~/.claude/commands/
```

After installation, invoke with `/update-context` in Claude Code.

> **Note:** If you have the thrum Claude Code plugin installed, use
> `/thrum:update-project` instead for guided durable project state updates.

## Available Commands

### `update-context.md`

Guides agents to compose and save structured ephemeral session context via
`thrum context save`. The agent writes a narrative summary, then delegates to a
subagent that gathers mechanical state (git, beads) and merges everything into a
structured document.

Use when ending a session or at natural breakpoints to preserve session context
for future sessions. For durable project-wide state, use `/thrum:update-project`
from the thrum plugin instead.

## Detection

To check whether the skill is installed, look for `update-context.md` in
`.claude/commands/` (project-level) or `~/.claude/commands/` (global). Ephemeral
session context is managed via the `/update-context` Claude Code skill. Durable
project state is managed via `/thrum:update-project` (thrum plugin).
