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

## Session Lifecycle

The `thrum tmux` commands give the coordinator full control over agent sessions.

**Auto-create:** all `thrum tmux` subcommands auto-create a session if the
daemon has a stored `cwd` from a prior `thrum tmux create` call. So after the
first create, `thrum tmux launch <name>` works even if the session was killed —
the daemon recreates it first.

### Create a Session

```bash
thrum tmux create implementer-api --cwd /path/to/worktree \
  --name impl_api --role implementer --module api
```

This creates a detached tmux session with a clean environment (no inherited env
vars), runs `thrum quickstart` inside the pane to register the agent identity,
and sets up `monitor-silence` hooks for permission detection.

**Environment scrubbing:** `THRUM_*` variables and `CLAUDE_PROJECT_DIR` are
stripped from new tmux sessions. `CLAUDE_PROJECT_DIR` is removed to prevent it
leaking across worktrees on a shared tmux server, which caused phantom self-echo
behavior (the kfn3 incident) when the wrong worktree's project dir was visible
to a newly-launched agent.

You must pass `--name`, `--role`, and `--module` — or `--no-agent` for a bare
session. Bare `thrum tmux create` without either errors out.

**`thrum tmux quickstart` is an alias** for the same command:

```bash
thrum tmux quickstart implementer-api --cwd /path/to/worktree \
  --name impl_api --role implementer --module api
```

Use whichever reads better to you. Same behavior.

**`--no-agent`** creates a bare session with no identity registration — useful
for debugging, tooling, or running non-agent processes in a managed tmux pane:

```bash
thrum tmux create debug-session --cwd /path/to/dir --no-agent
```

**`--force`** kills and recreates an existing session with the same name:

```bash
thrum tmux create implementer-api --cwd /path/to/worktree \
  --name impl_api --role implementer --module api --force
```

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

**Hard-errors on `--no-agent` or missing identity.** Launch needs an agent
identity in the target worktree to determine which runtime to start. If the
session was created with `--no-agent`, or if there's no identity file, launch
returns an error and tells you to run `thrum quickstart` first (or recreate the
session with `--name`/`--role`/`--module`). Launching a runtime without an
identity is a no-op.

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

> **Note (v0.9.2):** `thrum tmux status` and `thrum tmux connect` only show
> sessions tagged with the current daemon's `@thrum-thrum-dir`. Sessions created
> before v0.9.2 were not stamped with this tag and will not appear in the status
> output or the `connect` picker. They are not lost — just un-scoped. Recreate
> them via `thrum tmux create` to restore visibility.

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
# 1. Create the worktree, register the agent, and create the tmux session
#    in one step. The agent is NOT running yet.
thrum worktree create api-feature -b feature/api-refactor \
  --name impl_api --role implementer --module api --intent 'API refactor'

# 2. Launch the runtime
thrum tmux launch api-feature

# 3. Agent boots → prime detects tmux → agent checks inbox → starts working
# 4. Send it a task
thrum send "Your epic is thrum-abc. Run bd show thrum-abc and start working." --to @impl_api
```

For an existing worktree (one that wasn't created with `thrum worktree create`),
use `thrum tmux create` directly:

```bash
thrum tmux create implementer-api --cwd ~/.workspaces/myproject/api-feature \
  --name impl_api --role implementer --module api --intent 'API refactor'
thrum tmux launch implementer-api
```

The old pattern (create first, then `thrum tmux send` to run quickstart) is
gone. Passing `--name`, `--role`, and `--module` to `thrum tmux create` runs
quickstart inside the pane automatically. You can also use the
`thrum tmux quickstart` alias — same flags, same behavior.

**Single identity per worktree:** after quickstart runs, any old identity files
in the worktree are cleaned up. Each worktree has exactly one identity.

The agent is now running, receiving messages instantly, and you can monitor it
with `thrum team` or `thrum tmux status`.

## Session States

The daemon determines agent state from two checks: does the tmux session exist,
and is the Claude PID alive?

| Session exists | PID alive | State        | What it means                     |
| -------------- | --------- | ------------ | --------------------------------- |
| yes            | yes       | `tmux:alive` | Agent is running                  |
| yes            | no        | `tmux:stale` | Session exists but Claude exited  |
| no             | —         | `tmux:dead`  | Session is gone                   |
| —              | —         | `no-tmux`    | Agent not in tmux (legacy/remote) |

Two additional states come from the daemon's pane poller:

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

When the process dies (crash, compaction, manual kill), the daemon notices and
can restart it with a snapshot of what it was doing. See
[Session Restart](session-restart.md) for the snapshot flow.

## Permission Delegation

Running agents in unrestricted mode is dangerous. But agents that need
permission approval block on the prompt until a human notices. Tmux sessions
solve this.

### Daemon-Side SessionPoller (v0.9.0)

Detection runs entirely inside the daemon via a `SessionPoller`
(`internal/daemon/permission/poller.go`). There is no reliance on tmux's
`alert-silence` hook — that hook does not fire reliably on detached sessions
(tmux issue #1384: alerts are processed per-session-per-client; detached
sessions typically have no attached client). If you have `alert-silence`
configured in your `.tmux.conf`, it's harmless but inert for permission
detection.

Here's how the poller works:

1. **Enrollment** — `HandleLaunch` and `HandleRestart` enroll each session with
   the poller. `HandleKill` unenrolls it.
2. **Polling** — every 10 seconds, the daemon captures pane content, strips
   volatile lines specific to each runtime (spinners, statuslines, progress
   timers), and SHA-256 hashes the result.
3. **Stability threshold** — two consecutive identical hashes trigger
   `OnStable`. That's roughly 20 seconds of detection latency from when the
   prompt appears.
4. **`OnStable`** synthesizes a `CheckPaneRequest → HandleCheckPane` call. You
   can also invoke `thrum tmux check-pane` directly from the CLI, but that's
   unusual — the poller handles it automatically.
5. **`ReconcilePoller`** — at daemon boot, all active sessions are re-enrolled.
   In-flight detection survives a daemon restart cleanly.

When `HandleCheckPane` detects a permission prompt, the daemon routes a nudge to
the configured `permission_supervisors`, deduplicates repeat fires, and
escalates on backoff if the prompt goes unanswered.

The full detection and response workflow — supervisor configuration, nudge
format, reply channels (CLI / web UI / Telegram), and stuck-state recovery — is
documented in [Permission Prompt Detection](permission-prompts.md).

### CWD-Pinned Session Binding

The daemon's `writeTmuxToIdentity` uses the worktree CWD — not the tmux server
CWD — to locate the correct identity file when binding a pane to an agent. This
fixed a class of bugs where panes were attached to the wrong agent identity. The
G4 guard gates the write: if the CWD doesn't resolve to a known worktree, the
bind is refused. `team.list` self-heals dead sessions on every call, so stale
bindings clear automatically without manual cleanup.

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

## Command Queue Dispatch

Beyond message delivery, the daemon can dispatch commands to agent panes and
track their execution. This is the `thrum tmux queue` system — a per-session
FIFO that serializes commands, detects completion, captures output, and notifies
the requester.

### How It Works

1. A coordinator (or script) submits a command via
   `thrum tmux queue <session> <command>`
2. The daemon adds it to the session's queue and assigns a `cmd_` ID
3. When the pane goes silent, the daemon sends the next queued command via
   `send-keys`
4. The daemon monitors for the next silence event to detect completion
5. On completion, the daemon captures the pane output and sends an `@system`
   inbox message to the requester

Commands progress through states: `queued` → `sent` → `completed` (or
`timeout_waiting` → `cancelled`/`interrupted`).

### Example

```bash
# Fire and forget — get notified via inbox when done
thrum tmux queue implementer-api "make test"
# → Queued cmd_01KNTF2A9... (position 1)

# Block until done — get output directly
thrum tmux queue implementer-api "git log --oneline -5" --wait
# → State: completed
# → Elapsed: 1200ms
# → (output here)

# Check what's running
thrum tmux queue-status implementer-api

# Cancel a stuck command
thrum tmux cancel cmd_01KNTF2A9
```

### `@system` Notifications

When `notify_on_complete` is `true` (the default), the daemon sends inbox
messages from agent `"system"` for these events:

- **Completion** — includes command ID, session, elapsed time, and captured
  output (last 500 lines)
- **Timeout** — the command exceeded its timeout but is still running. The
  message includes a `thrum tmux cancel` hint.
- **Cancellation** — includes partial captured output
- **Interruption** — on daemon restart or session kill. Always sent regardless
  of `notify_on_complete`, since the caller's long-poll lost its connection.

Using `--wait` sets `notify_on_complete` to `false` — the CLI gets the result
directly, so inbox notifications are suppressed.

### Restart Recovery

Queued commands survive daemon restarts. On startup, the daemon:

1. Interrupts any in-flight commands (`sent`/`active`/`timeout_waiting`) and
   sends `@system` notifications
2. Reloads `queued` commands back into memory for dispatch on the next silence
   event

### Configuration

Per-command settings (no global config needed):

| Parameter | CLI Flag    | Default | Description                                |
| --------- | ----------- | ------- | ------------------------------------------ |
| timeout   | `--timeout` | 120s    | Max time before the command times out      |
| silence   | `--silence` | 5.0s    | Silence threshold for completion detection |

See [CLI Reference](cli.md#thrum-tmux-queue) for full flag details and
[RPC API](rpc-api.md#tmuxqueue) for the underlying RPC methods.

## Auto-Nudge

The daemon watches for a status mismatch: if an agent's `agent_status` is
`"working"` but its tmux pane has been silent, the daemon fires a nudge on the
next silence event. This catches agents that are stuck — waiting on something
without producing output — and wakes them up.

Set agent status with `thrum agent set-status working|idle|blocked`. See
[CLI Reference](cli.md#thrum-agent-set-status).

## Remote Transparency

Tmux sessions work transparently over Tailscale. Each daemon manages its own
local tmux sessions. When a message arrives via WebSocket sync from a remote
machine, the local daemon looks up the local tmux session and nudges it. No
cross-machine tmux operations needed.

You don't need to think about this. If you have two machines paired via
`thrum peer`, messages route to the right daemon, and the right daemon nudges
the right tmux pane. It just works.

## Running different runtimes

Tmux sessions work with any supported runtime — Claude Code, Codex, Cursor
(`agent`), Aider, Gemini, Open Code, Auggie, or Amp. Set the runtime with
`--runtime` on `thrum tmux launch`, or set `preferred_runtime` in the identity
file so every launch in that worktree uses the runtime you picked.

For the full runtime resolution order, setup flags, and known limitations, see
[Multi-Runtime Support](multi-runtime.md).

## Migration from Listeners

If you're already running agents with background listeners, switching is
painless:

1. Create a tmux session and register the agent identity:
   `thrum tmux create <name> --cwd <path> --name <agent> --role <role> --module <mod>`
2. Launch Claude Code inside: `thrum tmux launch <name>`
3. Agent's `thrum prime` detects `$TMUX`, writes `tmux_session`, switches to
   tmux-mode

If a background listener is still running from the old setup, it times out
harmlessly. No conflict.

No flag day required. Some agents can run in tmux-mode while others use legacy
listeners. The daemon nudges tmux agents and ignores legacy agents (who still
use their own listeners).

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

## Next Steps

- [Session Restart](session-restart.md) — save conversation history and resume
  where you left off, from daily shutdown to automated mid-task restarts
- [CLI Reference](cli.md#tmux-session-management) — full command reference for
  `thrum tmux`
- [Multi-Agent Support](multi-agent.md) — groups, coordination tools, and team
  workflows
- [Identity System](identity.md) — how `tmux_session` and `runtime` fields work
  in the identity file
