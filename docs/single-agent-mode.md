## What Changed

Thrum started as a messaging system. You run multiple agents, they need to talk
to each other, Thrum handles that. But over the past few months I've watched
people install Thrum for a single agent in a single repo — and immediately get
hit with listener warnings, cron watchdog prompts, stop hooks blocking their
session exit to check for unread messages. All of that infrastructure exists to
support multi-agent coordination. If you're running one agent, it's pure
overhead.

So v0.7.0 changes the default. `thrum init` now sets `single_agent_mode: true`.
You get Thrum's context management — project state tracking, session persistence
across compaction, `thrum prime` for orientation — without the messaging layer.
When you need multi-agent coordination, toggle it on:
`thrum single-agent-mode false`. Everything else activates automatically.

The other half of this release is the three-tier context model. If you've been
manually maintaining a continuation prompt document to give your agent project
context across sessions, that pattern is now built into Thrum. Three files,
three purposes, clean separation. And `thrum prime` now delivers all of it in
one call — no more two-step "run prime, then run context show."

## Single-Agent Mode

### The Problem

Every Thrum installation used to assume you were running multiple agents. That
meant:

- A background message-listener agent running on Haiku (~$0.00003/cycle, but it
  adds up)
- A cron watchdog checking every 30 minutes to respawn the listener if it died
- A stop hook that blocked session exit to warn about unread messages
- ~1,500 tokens of messaging protocol instructions injected into every session
  preamble

For a single agent, none of that does anything useful.

### What Changed

`thrum init` now defaults to `single_agent_mode: true` in `.thrum/config.json`.
In this mode:

- No background listener spawned
- No cron watchdog created
- No stop hook checks for unread messages
- No messaging protocol in the preamble
- `thrum inbox`, `thrum send`, `thrum reply`, `thrum wait` — all still work if
  you call them, but nothing prompts you to use them

What you still get:

- `thrum prime` — full session briefing with identity, daemon health, project
  state, and session context
- Project state tracking via `/thrum:update-project`
- PostCompact recovery — if the agent's context gets compacted mid-session,
  Thrum auto-saves state beforehand and restores it after
- Agent registration and sessions

### When to Switch to Multi-Agent

Single-agent mode is the right default for most repos. But some features only
make sense when the messaging layer is active. You'll know it's time to switch
when you want to:

- **Run multiple agents in parallel** — a coordinator on `main` and implementers
  on worktree branches, messaging each other as they work
- **Enable the Telegram bridge** — Telegram integration relays messages between
  your phone and your agents, which requires the messaging layer to be running
- **Use cross-machine sync** — agents on different machines communicating
  through Git-synced messages
- **Have agents coordinate on dependencies** — one agent finishing a task and
  notifying another that its blocker is resolved

**Recommended:** When you're ready for multi-agent, use
[tmux-managed sessions](tmux-sessions.md) instead of the legacy background
listener approach. The coordinator creates and manages agent sessions
automatically — no listeners, no token burn.

Switching is one command:

```bash
# Check current mode
thrum single-agent-mode

# Switch to multi-agent
thrum single-agent-mode false
```

That's it. Next time `thrum prime` runs, it includes the messaging protocol and
listener spawn instructions. The cron watchdog and stop hook activate
automatically. You don't need to re-init or restart the daemon.

If you later decide you don't need coordination anymore:

```bash
thrum single-agent-mode true
```

The listener stops getting spawned, the stop hook exits early, and the messaging
protocol drops out of your preamble. Everything is read at runtime — no files
get rewritten in either direction.

## Context That Survives Between Sessions

### Why This Matters

If you've used AI coding agents for any serious project, you know the problem.
You close a session, open a new one, and the agent has no idea what you were
doing. It doesn't know the architecture. It doesn't remember the decisions you
made yesterday. It doesn't know which epic is half-finished. So you spend the
first ten minutes of every session re-explaining your project.

The workaround most people land on is a "continuation prompt" — a document you
maintain by hand that tells the agent about the project. I used one for months.
It works, but it's tedious. You forget to update it. Session-level stuff ("I'm
halfway through the auth refactor") gets mixed in with project-level stuff ("we
use SQLite for the event store") and you end up with a document that's either
stale or bloated.

Thrum v0.7.0 builds this pattern in. Your agent gets full project context on
every session start, automatically. No manual document maintenance. No
copy-pasting. Run `thrum prime` and your agent knows where it is, what the
project looks like, and what it was doing before.

### What You Get

**Your agent remembers the project.** `thrum init` generates a project state
file with auto-detected language, framework, version, and branch. As you work,
the `/thrum:update-project` skill keeps it current — architecture decisions,
open epics, session history. You don't maintain this by hand. The agent updates
it at session boundaries from git and beads data plus a brief narrative of what
happened.

**Your agent remembers what it was doing.** Session context — in-progress work,
blockers, next steps — is saved automatically before context compaction and
restored after. If your session gets compacted mid-task, the agent picks up
where it left off.

**Your agent knows how to behave.** Role instructions, behavioral rules, which
commands to use — these are set once when the agent is created and rarely
change. They're not mixed in with the stuff that changes every session.

**One command gets everything.** `thrum prime` used to give you identity and
daemon status, then tell you to run `thrum context show` as a separate step. Now
it delivers the complete briefing — identity, role instructions, project state,
and session context — in a single call. The agent is oriented and ready to work
immediately.

### How It's Structured

Under the hood, this is three files with distinct purposes and lifecycles. The
separation is what makes it work — project knowledge doesn't get overwritten
when session state updates, and role instructions don't drift when the project
evolves. For the full technical details on the three-tier model, file paths, and
update mechanics, see the [Context Management](context.md) docs.

## Listener Improvements

If you're using multi-agent mode, the listener infrastructure is more reliable
in v0.7.0. If you're in single-agent mode, you can skip this section entirely.

### Heartbeat-Gated Spawning

The main problem with the old listener was accumulation. Three different code
paths could spawn a listener: the cron watchdog, the PostCompact hook, and the
parent agent responding to the listener's "RE-ARM" completion message. None of
them checked whether a listener was already running. Sessions routinely ended up
with 3+ duplicate listeners burning Haiku tokens for no reason.

Now every spawn path checks the heartbeat file first. The heartbeat is a
timestamp in the agent's identity JSON, updated every cycle by the running
listener. Rule: if the heartbeat is less than 10 minutes old, a listener is
alive — don't spawn another one.

| Spawn Path          | Before                                         | After                                           |
| ------------------- | ---------------------------------------------- | ----------------------------------------------- |
| Cron watchdog       | Prompt said "if no listener" — no actual check | Reads heartbeat age, skips if < 10 min          |
| PostCompact         | Didn't exist                                   | Checks heartbeat, spawns if stale + multi-agent |
| Listener completion | "RE-ARM NOW" triggered reflexive spawn         | Non-urgent message referencing cron watchdog    |

### Non-Urgent Completion

When a listener finishes its cycle, it used to print:

```
RE-ARM: This listener has stopped. Spawn a new message-listener
agent to continue listening.
```

That message caused the parent agent to immediately spawn a new listener —
whether one was needed or not. Now it prints:

```
Listener cycle complete. Cron watchdog monitors heartbeat and
will re-arm if needed.
```

The cron watchdog handles re-arming. The parent agent doesn't need to react.

### PostCompact Hook

New hook. When context compaction happens:

- **Both modes:** Emits an orientation prompt telling the agent to run
  `thrum prime` to restore context.
- **Multi-agent mode:** Also checks the heartbeat and respawns the listener if
  it's stale.

This pairs with the existing PreCompact hook that saves session context before
compaction. PreCompact saves, PostCompact recovers.

## Migration

**Existing multi-agent repos:** Nothing breaks. Your `.thrum/config.json`
already exists and doesn't have `single_agent_mode` set, so the daemon treats it
as `false` (multi-agent). Everything works exactly as before. The listener
improvements apply automatically.

**New repos:** `thrum init` defaults to single-agent mode. If you want
multi-agent coordination, run `thrum single-agent-mode false` after init.

**Upgrading the binary:** Standard process — pull the latest, rebuild, restart
your daemon. The new context files are created on first `thrum init` or
`thrum prime`. They don't interfere with existing files.

**Already have a continuation prompt?** If you've been maintaining a project
state document manually, you don't lose that work. Ask your agent to look at the
new project state format (`/thrum:update-project`) and import your existing
content into it. From then on, Thrum maintains it for you.

## What's Next

The cross-repo problem I mentioned above? It shipped in this release. v0.7.0
includes **peer transport** — you pair two Thrum daemons via Tailscale
(`thrum peer add` on one machine, `thrum peer join` on the other), configure
which agents should be visible across repos (`thrum peer configure`), and
messages route between them automatically. No Telegram relay, no manual
coordination.

The Telegram group approach turned out to have a real limitation — bots can't
see other bots' messages in groups, so agent-to-agent communication didn't work
there. The peer system replaces that entirely for cross-repo agent coordination.

[Telegram groups](telegram-groups.md) are still useful for human-to-agent
interaction — a shared group where your whole team can talk to agents. The peer
system handles the agent-to-agent side.

See
[Architecture — Cross-Repo Peer System](architecture.md#cross-repo-peer-system)
for how it works under the hood and [CLI Reference](cli.md#peer-management) for
the commands.
