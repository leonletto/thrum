---
title: "Automated Plan Execution"
description:
  "Hand off a plan you wrote and let the orchestrator run the execution phase"
category: "onboarding"
order: 4
tags: ["onboarding", "orchestrator", "automation", "plans"]
last_updated: "2026-04-09"
---

## Automated Plan Execution

You still did the thinking. You researched the problem, wrote the spec, broke it
into tasks, and drafted the implementation prompt with review gates baked in.
Now you hand that prompt to an orchestrator agent and it runs the execution
phase — spinning up implementers, watching for review gates, and reporting back.
You still merge. You still decide what ships. The orchestrator just removes you
from the relay loop.

## Prerequisites

- A plan file written and approved
- Epics and tasks created in Beads
- An implementation prompt with review gates defined
- `orchestration.*` config keys set in `config.json`

## Walkthrough

Read these in order:

1. [Orchestrator Role](../orchestrator-role.md) — what the orchestrator does,
   what it doesn't do, and how it stays in its lane
2. [Workflow Templates](../workflow-templates.md) — pre-built pipelines for the
   full research → plan → implement cycle
3. [Beads and Thrum](../beads-and-thrum.md) — how epic and task tracking give
   the orchestrator persistent state across sessions
4. [Multi-Agent Setup](../multi-agent.md) — the role model the orchestrator uses
   to spawn and assign implementers
5. [Tmux Sessions](../tmux-sessions.md) — how the orchestrator launches agents
   into isolated terminal sessions
6. [Configuration](../configuration.md) — every `orchestration.*` key and what
   it controls

## When you're ready for more

If you'd rather drive execution yourself instead of delegating it, see
[Team on Your Machine](team.md) — same agents, you hold the wheel.
