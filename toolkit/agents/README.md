# Thrum Agent Integration Files

This directory contains Claude Code agent definition files for integrating Beads and Thrum into your AI-assisted development workflow.

## What These Are

These are **agent definition files** that teach Claude Code how to use Beads (task tracking) and Thrum (agent messaging) in your project. They provide comprehensive guides on commands, workflows, and best practices.

## Installation

Copy these files to your project's `.claude/agents/` directory:

```bash
# From your project root
mkdir -p .claude/agents
cp toolkit/agents/*.md .claude/agents/
```

Claude Code will automatically detect and load these agent definitions when you start a session.

## Files

### `beads-agent.md`
Git-backed issue tracker integration for managing complex multi-step tasks with dependencies. Use this when:
- Managing feature epics with multiple subtasks
- Tracking work across agent sessions
- Coordinating parallel work with dependency graphs
- Recovering context after session compaction

Key commands: `bd ready`, `bd create`, `bd update`, `bd close`, `bd sync`

### `thrum-agent.md`
Multi-agent coordination system for persistent messaging across sessions and worktrees. Use this when:
- Multiple agents need to communicate
- Working across different worktrees or machines
- Requesting code reviews or assigning tasks
- Sending messages to teams via groups
- Broadcasting status updates to the team

Key commands: `thrum quickstart`, `thrum send`, `thrum inbox`, `thrum group`, `thrum status`

### `message-listener.md`
Background sub-agent that polls for incoming Thrum messages and notifies you when they arrive. Designed to run on Haiku model for cost efficiency (~$0.00003/cycle).

Usage: Launch as a background task at session start to enable async message notifications.

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
thrum send "Starting bd-123: implementing auth" --to @coordinator

# Do the work...

# Complete in Beads
bd close bd-123 --reason "Done" --json

# Notify via Thrum
thrum send "Completed bd-123, ready for review" --to @reviewer
```

## Requirements

- [Beads](https://github.com/beadifyio/beads) - Install with `cargo install beads-cli` or use binary releases
- [Thrum](https://github.com/thrumdev/thrum) - Install with `go install github.com/thrumdev/thrum/cmd/thrum@latest` or use binary releases
- Git repository with remote configured
- Claude Code environment

## Customization

You can edit these files to match your project's specific workflows, add custom commands, or adjust best practices for your team.

## Learn More

- Beads documentation: See the Beads project repository
- Thrum documentation: See the Thrum project repository
- Workflow templates: See `../templates/` for complete planning and implementation workflows
