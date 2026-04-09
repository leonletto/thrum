---
title: "Tmux-Managed Sessions"
description:
  "Run agent teams in daemon-managed tmux sessions with instant message
  delivery, zero background listeners, and coordinator-managed lifecycle"
category: "orchestration"
order: 2
tags: ["tmux", "sessions", "multi-agent", "nudge", "coordinator"]
last_updated: "2026-04-07"
---

## What This Is

Tmux-managed sessions are how multi-agent orchestration frameworks run their
agents. The daemon creates tmux sessions, launches AI tools inside them, and
delivers message notifications directly into the pane. No background process, no
polling, no token cost. The coordinator manages the whole team from one place.

Thrum's original approach — a background listener sub-agent watching for
messages — worked well and got us this far. But tmux sessions are a better fit
for how people actually run agent teams. The daemon handles delivery directly,
agents don't need to manage their own listeners, and the whole setup is simpler
to understand and operate.

This gives you the same daemon-driven session management you'd expect from
production agent orchestration tools — without the complexity.

---

## Prerequisites

**tmux** must be installed:

```bash
# macOS
brew install tmux

# Ubuntu/Debian
sudo apt install tmux
```

**Critical:** Create a `~/.tmux.conf` with mouse support enabled. Without this,
scrolling doesn't work and the experience feels broken compared to a normal
terminal:

```bash
# ~/.tmux.conf
set -g mouse on
```

That one line makes tmux behave like a regular terminal — scroll with your
trackpad, click to select panes, resize by dragging. You won't notice you're in
tmux.

The Thrum daemon must be running (`thrum daemon start`).

---

## How It Works

When a coordinator creates a tmux session for an agent, three things happen:

1. The daemon creates a tmux session with a clean environment
2. The agent's identity file gets a `tmux_session` field — the full pane target
   like `implementer-api:0.0`
3. When someone sends a message to that agent, the daemon writes the
   notification directly into the tmux pane via `tmux send-keys`

The notification looks like this:

```text
New message from @coordinator_main -- run `thrum inbox --unread` to read
```

The agent sees it as typed input, checks its inbox, and processes the message.
Delivery is instant — no polling loop, no background process, no tokens spent
waiting.

### Safety Measures

The daemon handles edge cases so you don't have to:

- **Literal mode** (`send-keys -l`) prevents shell interpretation of special
  characters
- **ESC before Enter** exits vim INSERT mode or tmux copy mode safely
- **600ms pause** between ESC and Enter exceeds readline's keyseq-timeout
- **Chunking** splits messages >512 bytes with 10ms delays between chunks
- **Per-session mutex** prevents interleaved keystrokes when multiple messages
  arrive simultaneously

### Self-Detection

Agents figure out they're in tmux automatically. When `thrum prime` detects the
`$TMUX` environment variable, it:

- Writes the `tmux_session` field to the identity file
- Switches to tmux-mode instructions (no listener spawn, no cron watchdog)
- Tells the agent: "You are in a tmux-managed session. Notifications are
  delivered directly — do NOT spawn a background listener."

No configuration needed. If the agent is in tmux, it knows.

### Fallback

Claude Code agents have a belt-and-suspenders safety net: the existing
`UserPromptSubmit` hook still checks for unread messages at every tool boundary.
If a nudge somehow gets missed, the hook catches it. Other runtimes rely solely
on the tmux nudge — which is fine, because the nudge is the reliable path.

---

## Session Lifecycle

The `thrum tmux` commands give the coordinator full control over agent sessions.

### Create a Session

```bash
thrum tmux create implementer-api --cwd /path/to/worktree
```

This creates a detached tmux session with a clean environment (no inherited env
vars). It sets up `monitor-silence` hooks for permission detection and returns
the agent's identity if one exists at that path.

### Launch a Runtime

```bash
thrum tmux launch implementer-api
```

This starts Claude Code inside the session. The agent boots, startup hooks run,
`thrum prime` detects tmux, and the agent is ready to work.

Want a different runtime?

```bash
thrum tmux launch implementer-api --runtime opencode
thrum tmux launch implementer-api --runtime shell
```

### Check Status

```bash
thrum tmux status
```

```text
SESSION                   AGENT                STATE        RUNTIME    BRANCH
coordinator-main          coordinator_main     alive        claude     thrum-dev
implementer-api           impl_api             alive        opencode   feature/api
implementer-website-dev   impl_website_dev     stale        claude     website-dev
```

`thrum tmux list` is an alias for the same output.

### Kill a Session

```bash
thrum tmux kill implementer-api
```

Tears down the tmux session and clears `tmux_session` from the identity file.

### Restart with Context

```bash
thrum tmux restart implementer-api
```

Extracts the agent's conversation history, kills the session, creates a new one,
and relaunches. The new session loads the snapshot via `thrum prime`. See
[Session Restart](session-restart.md) for the full story.

### The Full Flow

Here's what a coordinator does to spin up an agent from scratch:

```bash
# 1. Create a worktree for the agent
git worktree add ../worktrees/api-feature feature/api-refactor

# 2. Initialize thrum + beads in the worktree
cd ../worktrees/api-feature
thrum init
# Beads doesn't auto-detect worktrees, so set up the redirect manually:
mkdir -p .beads && echo /path/to/main/repo/.beads > .beads/redirect

# 3. Create the tmux session
thrum tmux create implementer-api --cwd ../worktrees/api-feature

# 4. Register the agent identity (must happen before launch)
thrum tmux send implementer-api "thrum quickstart --name impl_api --role implementer --module api --intent 'API refactor'"

# 5. Launch the runtime
thrum tmux launch implementer-api

# 6. Agent boots → prime detects tmux → agent checks inbox → starts working
# 7. Send it a task
thrum send "Your epic is thrum-abc. Run bd show thrum-abc and start working." --to @impl_api
```

**Important:** `thrum quickstart` must run before `thrum tmux launch`. It
creates the identity file that `thrum prime` reads on startup. Without it, the
agent doesn't know who it is.

The agent is now running, receiving messages instantly, and you can monitor it
with `thrum team` or `thrum tmux status`.

---

## Session States

The daemon determines agent state from two checks: does the tmux session exist,
and is the Claude PID alive?

| Session exists | PID alive | State        | What it means                     |
| -------------- | --------- | ------------ | --------------------------------- |
| yes            | yes       | `tmux:alive` | Agent is running                  |
| yes            | no        | `tmux:stale` | Session exists but Claude exited  |
| no             | —         | `tmux:dead`  | Session is gone                   |
| —              | —         | `no-tmux`    | Agent not in tmux (legacy/remote) |

Two additional states come from silence monitoring:

| State          | What it means              |
| -------------- | -------------------------- |
| `tmux:blocked` | Permission prompt detected |
| `tmux:idle`    | No output for >2 minutes   |

These show up in `thrum team` output so you can see at a glance which agents
need attention:

```text
@coordinator_main  coordinator  main         tmux:alive    thrum-dev
@impl_api          implementer  api          tmux:blocked  feature/api
@impl_website_dev  implementer  website-dev  tmux:idle     website-dev
@remote_sf         implementer  mock-sf      no-tmux       thrum-dev
```

States are always queried live — no caching, nothing to get stale.

---

## Permission Delegation

Running agents in unrestricted mode is dangerous. But agents that need
permission approval block on the prompt until a human notices. Tmux sessions
solve this.

The daemon uses tmux's native `monitor-silence` hooks — event-driven, not
polling:

1. When the session produces no output for 60 seconds, tmux fires an
   `alert-silence` hook
2. The hook runs `thrum tmux check-pane`, which captures the last 5 lines of the
   pane
3. If those lines match a permission prompt pattern, the daemon notifies the
   coordinator: "Agent @impl_api needs permission: Write to src/handler.go"
4. The coordinator reviews and responds

If the pane has been silent for >2 minutes with no permission prompt, the
coordinator gets an idle notification instead: "Agent @impl_api has been idle
for 3m42s"

The daemon deduplicates notifications — same reason doesn't trigger a repeat.
After 5 minutes with no change, it escalates ("agent still blocked/idle").
Notifications clear when the session produces output again.

> **Note:** Automated approve/deny is deferred. v0.7.1 surfaces blocked agents
> to the coordinator; approval is manual. Programmatic approval will follow once
> Claude Code's permission prompt format is stable.

---

## Mixed-Runtime Teams

The tmux nudge mechanism is plain text into a pane. Any CLI-based AI tool that
runs in tmux and can execute `thrum` commands can participate as a full team
member:

- **Claude Code** — full plugin/hook integration, richest experience
- **OpenCode** — cheaper, good for implementation tasks
- **Aider**, **Cursor CLI**, or other terminal-based AI tools
- **Plain shell** — for human operators who want to be part of the team

This lets you build mixed-runtime teams: coordinator on Claude Code (Opus for
decision-making), implementers on cheaper runtimes for parallel grunt work — all
coordinated through the same daemon and messaging system.

```bash
# Coordinator on Claude (Opus)
thrum tmux launch coordinator-main --runtime claude

# Implementers on cheaper runtimes
thrum tmux launch impl-api --runtime opencode
thrum tmux launch impl-tests --runtime claude  # Haiku for test writing

# Human operator
thrum tmux launch debug-session --runtime shell
```

The `runtime` field is stored in the identity file and visible in `thrum team`
output. The nudge mechanism is identical regardless of runtime.

> **Note:** Only Claude Code has the `UserPromptSubmit` hook as a fallback
> safety net. Other runtimes rely solely on the tmux nudge for notification.
> This is fine — the nudge is the primary and reliable delivery path.

---

## Remote Transparency

Tmux sessions work transparently over Tailscale. Each daemon manages its own
local tmux sessions. When a message arrives via WebSocket sync from a remote
machine, the local daemon looks up the local tmux session and nudges it. No
cross-machine tmux operations needed.

You don't need to think about this. If you have two machines paired via
`thrum peer`, messages route to the right daemon, and the right daemon nudges
the right tmux pane. It just works.

---

## Migration from Listeners

If you're already running agents with background listeners, switching is
painless:

1. Create a tmux session for your agent: `thrum tmux create <name> --cwd <path>`
2. Launch Claude Code inside: `thrum tmux launch <name>`
3. Agent's `thrum prime` detects `$TMUX`, writes `tmux_session`, switches to
   tmux-mode

If a background listener is still running from the old setup, it times out
harmlessly. No conflict.

No flag day required. Some agents can run in tmux-mode while others use legacy
listeners. The daemon nudges tmux agents and ignores legacy agents (who still
use their own listeners).

---

## What's Eliminated

| Before                               | After                              |
| ------------------------------------ | ---------------------------------- |
| Background listener (Haiku subagent) | Daemon nudge via `send-keys`       |
| 500ms polling loop                   | Event-driven                       |
| Cron watchdog                        | Built into tmux                    |
| PID file heartbeat                   | Session + PID liveness checks      |
| Listener re-arm boilerplate          | Nothing — it's automatic           |
| "Forgot to launch listener"          | Impossible — no listener to forget |
| Token burn on idle agents            | Zero — daemon does the work        |
| Permission prompt stalls             | Surfaced to coordinator            |

---

## Next Steps

- [Session Restart](session-restart.md) — save conversation history and resume
  where you left off, from daily shutdown to automated mid-task restarts
- [CLI Reference](cli.md#tmux-session-management) — full command reference for
  `thrum tmux`
- [Multi-Agent Support](multi-agent.md) — groups, coordination tools, and team
  workflows
- [Identity System](identity.md) — how `tmux_session` and `runtime` fields work
  in the identity file
