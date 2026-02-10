---
title: "Agent Configurations"
description: "Ready-to-use Claude Code agent definitions for Thrum messaging and Beads task tracking"
category: "guides"
order: 2
tags: ["agents", "claude-code", "configuration", "setup", "toolkit"]
last_updated: "2026-02-10"
---

# Agent Configurations

Claude Code agent definitions teach Claude how to use Thrum and Beads effectively. These `.md` files with YAML frontmatter ship in `toolkit/agents/` and load into your project's `.claude/agents/` directory.

## Install the agent configs

Copy the agent definitions to your project:

```bash
mkdir -p .claude/agents
cp toolkit/agents/*.md .claude/agents/
```

Claude Code automatically detects and loads these files when you start a session.

## Available agents

### thrum-agent

Comprehensive guide for Thrum messaging. Teaches Claude how to register agents, send/receive messages, coordinate with teammates, and choose between MCP tools and CLI.

**Use when:**
- Coordinating multi-agent workflows
- Working across worktrees or machines
- Requesting code reviews or assigning tasks
- Broadcasting status updates

**Key capabilities:**
- Agent registration with roles and intents
- Direct messaging and broadcasts
- MCP server integration with async notifications
- Message priority handling (critical/high/normal/low)
- Session management and heartbeats

### beads-agent

Guide for Beads issue tracking. Teaches Claude the task lifecycle (create, claim, close), dependency management with `blocks` relationships, and context recovery after conversation compaction.

**Use when:**
- Managing feature epics with multiple subtasks
- Tracking work across agent sessions
- Coordinating parallel work with dependency graphs
- Discovering side quests during implementation

**Key capabilities:**
- Finding ready work (unblocked tasks)
- Creating tasks with dependencies
- Epic-driven work breakdown
- Git-backed sync for multi-agent coordination
- Adding notes with documentation links

### message-listener

Lightweight background polling agent. Runs on Haiku model for cost efficiency (~$0.00003/cycle). Checks inbox every 30 seconds, returns immediately when messages arrive, times out after 30 minutes.

**Use when:**
- You want async message notifications
- Working with Thrum via MCP server
- Running long sessions that need message awareness

**Key capabilities:**
- Background polling loop (60 cycles max)
- Immediate return on message arrival
- Automatic read marking via `thrum inbox`
- CLI-only (no MCP tools for simplicity)

## Configure the message listener

The message-listener needs specific setup to run on Haiku and use your Thrum identity.

Launch it as a background task at session start:

```typescript
// In Claude Code with Thrum MCP configured
Task({
  subagent_type: "message-listener",
  model: "haiku",
  run_in_background: true,
  prompt: "Listen for Thrum messages. THRUM_ROOT=/path/to/repo AGENT_NAME=your_agent_name"
});
```

**Environment variables:**
- `THRUM_NAME` or `AGENT_NAME` — Your registered agent name
- `THRUM_ROOT` — Path to your repo (optional, defaults to cwd)

The listener will check `thrum inbox --unread` every 30 seconds and return immediately when messages arrive. Re-arm it after processing messages to continue listening.

## Customize for your project

You can edit these agent definitions to match your team's workflows. Add project-specific commands, adjust priorities, or include custom context.

For the agent file format, see [Claude Code agent documentation](https://docs.anthropic.com/claude-code).

## See also

- [Quickstart](quickstart.md) — Get started with Thrum in 5 minutes
- [Agent Coordination](agent-coordination.md) — Multi-agent messaging patterns
- [Workflow Templates](../toolkit/templates/) — Complete planning and implementation workflows
