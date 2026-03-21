
## What Is Thrum?

> **TL;DR:** Thrum has ~30 commands but you only need 8 for daily use. The
> tables below break down which commands are for you, which are for agents, and
> which are one-time setup. Start here, then drill into the reference pages when
> you need details.

Thrum is a messaging system for coordinating AI agents across sessions,
worktrees, and machines. It provides:

- **Persistent messaging** that survives session boundaries
- **Automatic synchronization** via Git — no extra servers
- **Real-time visibility** into what other agents are working on
- **Subscription-based notifications** for targeted communication
- **Backup & Restore** for protecting your message history and agent state

Everything is inspectable. Messages are JSONL files on a Git branch. State is a
queryable SQLite database. Sync is plain Git push/pull. If something goes wrong,
you look at files.

> **New here?** Start with [Why Thrum Exists](philosophy.md) for the philosophy
> behind the project, or the [Quickstart Guide](quickstart.md) to get running in
> 5 minutes.

## Quick Setup

After `thrum init`, run one command to generate agent coordination instructions
for your CLAUDE.md:

```bash
thrum setup claude-md --apply
```

`thrum prime` (or `thrum context prime`) checks for an existing Thrum section
and suggests this if it's missing.

## Understanding the CLI

Thrum has ~30 commands. Here's why that's not as many as it sounds.

### Daily Drivers (8 commands)

These are the commands you'll actually use. If you're directing agents and
checking on their work, this is your whole toolkit:

| Command      | What it does                                     |
| ------------ | ------------------------------------------------ |
| `quickstart` | Register + start session + set intent — one step |
| `send`       | Send a message                                   |
| `inbox`      | Check your messages                              |
| `reply`      | Reply to a message                               |
| `team`       | What's everyone working on?                      |
| `overview`   | Status + team + inbox in one view                |
| `status`     | Your current state                               |
| `who-has`    | Who's editing this file?                         |

That's it for daily use. Everything else is infrastructure.

### Designed for Agents (~16 commands)

These commands exist because agents need programmatic lifecycle control. You
rarely use them directly — `quickstart` handles the common case — but agents
call them constantly:

| Area          | Commands                         | Why agents need them                    |
| ------------- | -------------------------------- | --------------------------------------- |
| Identity      | `agent register`, `agent whoami` | Agents register on startup              |
| Sessions      | `session start/end/heartbeat`    | Track work periods, extract git state   |
| Work context  | `session set-intent/set-task`    | Declare what they're doing              |
| Notifications | `subscribe`, `wait`              | Block until relevant messages arrive    |
| Context       | `context save/show/clear`        | Persist state across compaction         |
| Messages      | `message get/edit/delete/read`   | CRUD on individual messages             |
| Groups        | `group create/add/remove/list`   | Organize teams programmatically         |
| MCP           | `mcp serve`                      | Native tool access for Claude Code etc. |

### Setup & Admin (run once)

`init`, `daemon start/stop`, `setup`, `migrate`, `agent delete/cleanup`,
`sync force/status`, `runtime list/show`, `ping`

### Aliases (because agents get creative)

AI agents are unpredictable in how they guess command names. An agent told to
"start working" might try `thrum agent start` or `thrum session start`. Both
work. These duplicates exist on purpose — they reduce friction for non-human
users:

| Alias              | Points to            | Why it exists                               |
| ------------------ | -------------------- | ------------------------------------------- |
| `agent start`      | `session start`      | Agents think "I'm an agent, I should start" |
| `agent end`        | `session end`        | Same pattern                                |
| `agent set-intent` | `session set-intent` | Natural grouping under `agent`              |
| `agent set-task`   | `session set-task`   | Same                                        |
| `agent heartbeat`  | `session heartbeat`  | Same                                        |
| `whoami`           | `agent whoami`       | Common enough to promote to top-level       |

## Design Principles

**You stay in control.** Thrum is infrastructure you can inspect, not a service
you depend on. Everything is files, Git branches, and a local daemon. See
[Why Thrum Exists](philosophy.md) for the full philosophy.

**Offline-first.** Everything works locally. Network is optional for sync.

**Git as infrastructure.** No additional servers. Uses existing Git
authentication and hosting.

**Event sourcing.** JSONL log is the source of truth. SQLite is a rebuildable
projection.

**Conflict-free.** Immutable events + unique IDs = conflict-free merging.

**Minimal dependencies.** Pure Go with minimal external packages. No CGO.

**Graceful degradation.** Network failures, missing remotes, and partial sync
all handled gracefully.

## Documentation Index

| Document                                                 | Description                                                           |
| -------------------------------------------------------- | --------------------------------------------------------------------- |
| [Philosophy](philosophy.md)                              | Why Thrum exists and how it thinks about agents                       |
| [Quickstart Guide](quickstart.md)                        | 5-minute getting started                                              |
| [Architecture](architecture.md)                          | Daemon internals, storage, sync, and packages                         |
| [Daemon Architecture](daemon.md)                         | Technical daemon internals                                            |
| [RPC API Reference](rpc-api.md)                          | All RPC methods                                                       |
| [Sync Protocol](sync.md)                                 | Git synchronization details                                           |
| [WebSocket API](api/websocket.md)                        | WebSocket-specific docs                                               |
| [Event Streaming](event-streaming.md)                    | Notifications and subscriptions                                       |
| [CLI Reference](cli.md)                                  | All CLI commands and backup & restore                                 |
| [Identity System](identity.md)                           | Agent identity and registration                                       |
| [Context Management](context.md)                         | Agent context storage and persistence                                 |
| [Multi-Agent Support](multi-agent.md)                    | Groups, runtime presets, and team coordination                        |
| [Tailscale Sync](tailscale-sync.md)                      | Cross-machine sync via Tailscale with security                        |
| [Agent Coordination](agent-coordination.md)              | Multi-agent workflows and Beads integration                           |
| [Workflow Templates](workflow-templates.md)              | Three-phase agent development templates                               |
| [Coordinate Two Agents](guides/coordinate-two-agents.md) | Walk through registering two agents and sending messages between them |
| [Sync Across Machines](guides/cross-machine-sync.md)     | Walk through enabling cross-machine sync via Git                      |
| [Code Review Workflow](guides/review-workflow.md)        | Walk through the implement-then-review cycle with worktrees           |

## Next Steps

- [Quickstart Guide](quickstart.md) — install Thrum and get your first agent
  running in 5 minutes
- [Why Thrum Exists](philosophy.md) — the philosophy behind human-directed agent
  coordination
- [Messaging](messaging.md) — send and receive messages between agents
- [Agent Coordination](agent-coordination.md) — practical workflows for running
  multiple agents in parallel
