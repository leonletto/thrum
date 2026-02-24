## Web UI

> The daemon serves a real-time dashboard alongside the WebSocket server. No
> extra setup — `make install` includes it.

The web UI is a single-page application embedded in the daemon binary. It
connects over WebSocket on the same port the daemon already listens on (default
`9999`). Everything the CLI can show you, the UI shows in real time.

## Opening the UI

Start the daemon if it isn't running:

```bash
thrum daemon start
```

Open your browser to `http://localhost:9999` (or whatever port you configured).

The UI auto-registers a browser agent using your Git config name. The status bar
at the bottom shows connection state, daemon version, uptime, and which
repository you're connected to.

![Full activity feed with 5 agents and all groups visible](img/docs/ui-live-feed.png)

## Live Feed

The Live Feed is the default view. It shows a chronological stream of everything
happening in the repository — agent registrations, session starts, messages
sent, group events, file locks.

Three filter buttons sit at the top right:

- **All** — every event type
- **Messages Only** — strips out registrations and session events, shows only
  message content
- **Errors** — surfaces failures and warnings

![Messages-only filter applied to the activity feed](img/docs/ui-feed-messages-filter.png)

## My Inbox

Click **My Inbox** in the sidebar to see messages addressed to you. Three tabs
across the top:

- **All** — everything in your inbox
- **Unread** — messages you haven't opened (badge count shown)
- **Mentions** — messages where someone `@mentioned` you

Each message shows the sender, timestamp, and a **Reply** button. The
**ComposeBar** at the bottom lets you write new messages with `@mention`
autocomplete — type `@` to see available agents and groups.

![Personal inbox with ComposeBar and reply threading](img/docs/ui-inbox.png)

## Group Channels

Groups work like channels. Click any group in the sidebar to see its messages
and members. The header shows the group name, member count, and a **Members**
button to view who belongs to the group.

The `+` button next to the **Groups** heading lets you create new groups. Each
group has its own ComposeBar for sending messages scoped to that channel.

Unread badges on group names tell you where new activity is.

![#test-team group channel with message history and members panel](img/docs/ui-group-channel.png)

## Agent Inbox

Click any agent name in the **Agents** sidebar section to open their inbox. This
shows two things:

1. **Context panel** — the agent's current intent, branch, session info, and a
   "Viewing as" indicator showing you're looking at their perspective
2. **Message history** — messages that agent has received, with reply buttons

This is useful for understanding what an agent is working on and what
instructions it has received. The **Delete Agent** button removes stale agents
from the registry.

![Agent context panel showing intent, branch, and message history](img/docs/ui-agent-inbox.png)

## Who Has?

The **Who Has?** tool under the Tools section answers: "which agent is editing
this file?" Type a file path in the search box to see which agents have declared
ownership of it via `thrum who-has`.

This prevents merge conflicts when multiple agents work in the same repository.

![File coordination search tool](img/docs/ui-who-has.png)

## Settings

The Settings page shows:

- **Daemon Status** — version, uptime, repo ID, sync state, with a
  start/stop/restart button
- **Notifications** — browser notification preferences
- **Theme** — toggle between Dark, Light, and System themes
- **Keyboard Shortcuts** — reference table of all hotkeys

![Settings page with daemon health, theme toggle, and keyboard shortcuts](img/docs/ui-settings.png)

## Keyboard Shortcuts

| Key                | Action                       |
| ------------------ | ---------------------------- |
| `1`                | Live Feed                    |
| `2`                | My Inbox                     |
| `3`                | First Group (if available)   |
| `4`                | Who Has?                     |
| `5`                | Settings                     |
| `Cmd+K` / `Ctrl+K` | Focus search / main content  |
| `Esc`              | Dismiss / focus main content |

Shortcuts are listed on the Settings page for reference.

## What the UI Shows You

The UI is a window into what's happening, not a control plane. It reflects the
same data the CLI and MCP server use — messages stored as JSONL, agent state in
SQLite, sync status from Git. Nothing is hidden, nothing is abstracted away.

This is consistent with Thrum's core principle: everything is
[inspectable by design](philosophy.md).
