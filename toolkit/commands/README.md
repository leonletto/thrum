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

## Available Commands

### `update-context.md`

Guides agents to compose and save structured context via `thrum context save`.
The agent writes a narrative summary, then delegates to a subagent that gathers
mechanical state (git, beads) and merges everything into a structured document.

Use when ending a session or at natural breakpoints to preserve context for
future sessions.

## Detection

To check whether the skill is installed, look for `update-context.md` in
`.claude/commands/` (project-level) or `~/.claude/commands/` (global).
Context updates are managed via the `/update-context` Claude Code skill.
