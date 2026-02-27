---
title: "Beads UI Setup Guide"
description:
  "Set up Beads UI for a live web dashboard of your agent task board —
  installation, usage, and multi-project support"
category: "tools"
order: 2
tags: ["beads-ui", "setup", "dashboard", "kanban", "web-ui", "guide"]
last_updated: "2026-02-27"
---

## Beads UI Setup Guide

[Beads UI](https://github.com/mantoni/beads-ui) is a local web interface for
the Beads issue tracker. It gives developers a real-time browser view of what
agents are working on — without polling, refreshing, or switching to the
terminal.

### Why Use It

When agents coordinate via Thrum and track tasks with Beads, the developer
needs visibility. Running `bd list` in a terminal works, but Beads UI provides:

- **Live updates** — Issues move across the board as agents claim and close
  them, with no manual refresh needed
- **Board view** — Kanban columns (Blocked / Ready / In Progress / Closed) with
  drag-and-drop
- **Epics view** — Expand epics to see child task progress at a glance
- **Issue detail** — Edit titles, descriptions, priorities, dependencies, and
  comments directly in the browser

The UI watches the Beads SQLite database for changes and pushes updates over
WebSocket. When an agent runs `bd close <id>`, the card moves to Done in
your browser instantly.

### Installation

```bash
# Install globally from npm
npm install beads-ui -g

# Verify
bdui --help
```

Requires **Node.js 22 or later**.

### Quick Start

```bash
# Navigate to your project (where .beads/ exists)
cd your-project

# Start the UI and open the browser
bdui start --open
```

The UI is now running at `http://localhost:3000`. It auto-detects the nearest
`.beads/*.db` file by walking up from the current directory.

### Views

#### Issues View

A filterable, searchable list of all issues. Each row shows the title, status,
priority, type, and dependency counts. Click any issue to open the full detail
view for inline editing.

#### Epics View

Shows all epics with progress indicators. Expand a row to see its child tasks
and their statuses. Useful for tracking high-level project progress.

#### Board View

Kanban-style columns:

| Blocked | Ready | In Progress | Closed |
|---------|-------|-------------|--------|
| Tasks waiting on dependencies | Tasks with no blockers | Claimed by an agent | Done |

Drag cards between columns to change status. Each column shows a badge with its
card count.

### Configuration

#### CLI Options

```bash
bdui start              # Start on default port 3000
bdui start --open       # Start and open browser
bdui start --port 3001  # Use a specific port
bdui stop               # Stop the server
bdui restart             # Restart the server
bdui list               # List all running instances
```

#### Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `BD_BIN` | `bd` | Path to the Beads CLI binary |
| `PORT` | `3000` | Listen port |
| `HOST` | `127.0.0.1` | Bind address |

### Multi-Project Support

Run multiple instances simultaneously — one per project:

```bash
# Project A
cd ~/projects/thrum
bdui start --new-instance --open    # auto-selects port 3001

# Project B
cd ~/projects/other-project
bdui start --new-instance --open    # auto-selects port 3002

# See all running instances
bdui list
```

Each instance watches its own project's Beads database independently.

### Typical Developer Workflow

1. Start Beads UI in your project: `bdui start --open`
2. Start your Thrum agents (coordinator, implementers, etc.)
3. Watch the board as agents discover tasks via `bd ready`, claim them with
   `bd update --status=in_progress`, and close them with `bd close`
4. Use the issue detail view to add comments, adjust priorities, or edit
   descriptions while agents work
5. Check the epics view for high-level progress across the project

The board updates live — no need to refresh. When an agent sends a Thrum message
saying "Completed task X", you'll see the card move to the Closed column at the
same time.

### Debug Logging

```bash
# Server-side debug output
DEBUG=beads-ui:* bdui start

# Browser-side (in DevTools console)
localStorage.debug = 'beads-ui:*'
```

### Further Reading

- [Beads Setup](beads-setup.md) — Install and configure Beads itself
- [Beads UI GitHub](https://github.com/mantoni/beads-ui) — Full source and
  changelog
- [Recommended Tools](recommended-tools.md) — Overview of all recommended tools
