---
title: "Recommended Tools"
description:
  "Tools, extensions, and integrations that work well with Thrum for AI agent
  coordination workflows"
category: "tools"
order: 0
tags: ["tools", "beads", "beads-ui", "playwright", "ecosystem", "integrations"]
last_updated: "2026-02-27"
---

## Recommended Tools

A curated list of tools and integrations that complement Thrum for AI agent
coordination. Each tool addresses a specific gap in the agent workflow — task
tracking, visual oversight, or browser automation.

### Task Tracking

| Tool                          | What it does                                   | Install               |
| ----------------------------- | ---------------------------------------------- | --------------------- |
| [Beads](beads-setup.md)       | Git-backed issue tracker with dependencies     | `go install` / `brew` |
| [Beads UI](beads-ui-setup.md) | Live web dashboard for Beads issues and boards | `npm i beads-ui -g`   |

**Beads** is the task-tracking counterpart to Thrum's messaging. Where Thrum
answers "what did my teammates tell me?", Beads answers "what should I work on?"
Both persist to Git, require no external services, and survive session
boundaries.

**Beads UI** gives developers a browser-based view of what agents are working on
— a Kanban board, epics view, and issue list that update in real time as agents
claim and close tasks.

### Agent Workflow & Discipline

| Tool                          | What it does                                         | Install           |
| ----------------------------- | ---------------------------------------------------- | ----------------- |
| [Superpowers](superpowers.md) | Skills library: brainstorming, planning, TDD, review | `/plugin install` |

**Superpowers** is the upstream skills library Thrum's coordination workflows
build on. Its `brainstorming` and `writing-plans` skills produce the design doc
and plan file that Thrum's `project-setup` reads to fan work out into Beads
epics — so it's a prerequisite for the brainstorm and project-setup pipelines,
not an optional extra. See the [Superpowers guide](superpowers.md) for the full
skill mapping and per-harness install.

### Browser Automation

| Tool                                      | What it does                              | Install           |
| ----------------------------------------- | ----------------------------------------- | ----------------- |
| [Playwright CLI](playwright-cli-setup.md) | Browser automation for Claude Code agents | Claude Code skill |

**Playwright CLI** lets agents interact with web pages during their workflow —
capture screenshots for context, fill forms, extract data, or verify that a web
UI renders correctly. It runs as a Claude Code skill using the `playwright-cli`
command.

### How These Tools Work Together

A typical multi-agent workflow:

1. **Superpowers** supplies the engineering discipline — brainstorm a design,
   write a plan, and run TDD and code review inside each task
2. **Beads** tracks the work — epics, tasks, dependencies, and priorities
3. **Thrum** coordinates the agents — messaging, presence, and session context
4. **Beads UI** gives the developer a live dashboard of agent progress
5. **Playwright CLI** lets agents capture browser context or verify web UI
   changes

```text
Developer                 Agents                    Tools
    │                        │                        │
    ├─ Opens beads-ui ──────►│                        │
    │  (sees board view)     │                        │
    │                        ├─ bd ready ────────────►│ Beads
    │                        ├─ bd update --status ──►│
    │                        ├─ thrum send ──────────►│ Thrum
    │                        ├─ playwright-cli ──────►│ Playwright
    │                        │  screenshot            │
    │                        ├─ bd close ────────────►│ Beads
    │◄─ Live board update ───┤                        │
    │  (task moves to Done)  │                        │
```

### Further Reading

- [Beads and Thrum](../beads-and-thrum.md) — How persistent task tracking and
  messaging solve context loss together
- [Multi-Agent Coordination](../multi-agent.md) — Patterns for multi-agent
  workflows
- [Claude Code Plugin](../claude-code-plugin.md) — Thrum's Claude Code plugin
  reference
