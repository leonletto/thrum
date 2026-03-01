# Thrum Agent Integration Files

This directory contains Claude Code agent definition files for integrating Thrum
into your AI-assisted development workflow.

## What These Are

These are **agent definition files** that teach Claude Code how to use Thrum
(agent messaging) in your project. They provide comprehensive guides on
commands, workflows, and best practices.

For **Beads** (task tracking), install the [Beads plugin](#beads-plugin) instead
of using an agent file — the plugin provides richer functionality including 30+
slash commands, resource files, and hooks.

## Installation

Copy the Thrum agent files to your project's `.claude/agents/` directory:

```bash
# From your project root
mkdir -p .claude/agents
cp toolkit/agents/thrum-agent.md .claude/agents/
cp toolkit/agents/message-listener.md .claude/agents/
```

Claude Code will automatically detect and load these agent definitions when you
start a session.

## Files

### `thrum-agent.md`

Multi-agent coordination system for persistent messaging across sessions and
worktrees. Use this when:

- Multiple agents need to communicate
- Working across different worktrees or machines
- Requesting code reviews or assigning tasks
- Sending messages to teams via groups
- Broadcasting status updates to the team

Key commands: `thrum quickstart`, `thrum send`, `thrum inbox`, `thrum group`,
`thrum status`

### `message-listener.md`

Background sub-agent that polls for incoming Thrum messages and notifies you
when they arrive. Designed to run on Haiku model for cost efficiency
(~$0.00003/cycle).

Usage: Launch as a background task at session start to enable async message
notifications.

## Beads Plugin

For Beads issue tracking, **install the Beads plugin** instead of using a local
agent file. The plugin provides:

- **SKILL.md** with session protocol, CLI reference, and resource links
- **30+ slash commands** (`/beads:ready`, `/beads:create`, `/beads:close`,
  `/beads:sync`, etc.)
- **15+ resource files** covering dependencies, workflows, troubleshooting,
  molecules, worktrees
- **Hooks** that auto-run `bd prime` on session start for workflow context

Install the plugin in Claude Code:

```bash
/install-plugin beads
```

Or visit the [Beads marketplace](https://github.com/steveyegge/beads) for manual
installation.

## Integration Pattern

When using both Beads and Thrum together:

1. **Beads** = Source of truth for task state and work breakdown
2. **Thrum** = Real-time coordination and status updates

Example workflow:

```bash
# Find work in Beads
bd ready --json

# Claim task
bd update bd-123 --status in_progress --json

# Announce via Thrum
thrum send "Starting bd-123: implementing auth" --to @coord_main

# Do the work...

# Complete in Beads
bd close bd-123 --reason "Done" --json

# Notify via Thrum
thrum send "Completed bd-123, ready for review" --to @reviewer1
```

## Requirements

- [Beads](https://github.com/steveyegge/beads) - Install the Beads plugin for
  Claude Code, or install the CLI with `cargo install beads-cli`
- [Thrum](https://github.com/leonletto/thrum) - Install with
  `go install github.com/leonletto/thrum/cmd/thrum@latest` or use binary
  releases
- Git repository with remote configured
- Claude Code environment

## Customization

You can edit the Thrum agent files to match your project's specific workflows,
add custom commands, or adjust best practices for your team.

## Learn More

- Beads documentation: See the Beads project repository
- Thrum documentation: See the Thrum project repository
- Workflow templates: See `../templates/` for complete planning and
  implementation workflows
