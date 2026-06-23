---
title: "Multi-Runtime Support"
description:
  "Run Claude, Codex, Aider, Gemini, and other agent runtimes in tmux-managed
  sessions"
category: "orchestration"
order: 3
tags: ["runtime", "tmux", "codex", "aider", "gemini", "claude"]
last_updated: "2026-04-19"
---

## What "Runtime" Means in Thrum

A runtime is the CLI you're running inside a tmux pane. Not the agent — the tool
the agent uses to think and type. When Thrum launches a session, it starts one
of these:

- **Claude Code** — `claude`, full hook and plugin integration
- **Open Code** — `opencode`, cheaper for parallel grunt work
- **Codex** — `codex`, OpenAI's CLI agent
- **Aider** — `aider`, terminal-based with git-native editing
- **Cursor** — `agent` (primary) / `cursor-agent` (legacy, still detected),
  Cursor's headless mode
- **Kiro** — `kiro-cli`, runtime preset only in v0.9.0; no dedicated plugin yet.
  Manual `thrum prime` required after restart.
- **Gemini** — `gemini`, Google's CLI agent
- **Amp** — `amp`
- **Shell** — plain bash, for human operators who want to be part of the team

Each is a first-class citizen. Thrum doesn't care which one you're running. The
nudge mechanism, messaging, identity system, and session lifecycle work the same
regardless.

---

## Why Multi-Runtime Matters

Different runtimes are good at different things. Claude Opus is expensive but
sharp. Open Code costs less per token and handles implementation tasks fine.
Aider is fast for targeted file edits. Gemini has a long context window.

You want to match runtime to role. Coordinator on Opus. Implementers on cheaper
runtimes. Test writers on something fast. The decision should be yours, per
worktree, not a system-wide setting you forget to change.

The team mesh works regardless of what each agent is running. A coordinator on
Claude can send a message to an implementer on Open Code, get a reply, and route
a follow-up to another implementer on Codex. The messaging layer doesn't know or
care about runtimes.

```bash
# Coordinator decides who runs what
thrum tmux launch coordinator-main --runtime claude
thrum tmux launch impl-api --runtime opencode
thrum tmux launch impl-tests --runtime codex
thrum tmux launch debug-session --runtime shell
```

The `runtime` field shows up in `thrum tmux status` and `thrum team` so you can
see at a glance what each agent is running.

---

## Runtime Resolution Order

Every tmux command that starts or restarts a runtime resolves which runtime to
use in this order:

1. **`--runtime` flag** on the command (explicit, always wins)
2. **`preferred_runtime`** in the worktree's identity file
3. **`runtime.primary`** in `.thrum/config.json`
4. **Default:** `"claude"`

This is consistent across `thrum tmux launch`, `thrum tmux start`, and
`thrum tmux restart`. The CLI resolves the runtime before sending the RPC to the
daemon — there's no ambiguity at the server side.

If you set `preferred_runtime` in the identity file once, you never need to pass
`--runtime` again for that worktree. The resolution chain just finds it.

**Note:** `thrum tmux launch <name>` reads the target worktree's identity file
directly, bypassing the caller's `THRUM_HOME` / `THRUM_NAME` env vars. This
fixes a class of bugs where launching from one worktree to another carried the
wrong identity into the new session.

---

## Setting `preferred_runtime` at Init

Pass `--runtime` when you first set up a worktree. Thrum writes the value to the
identity file as `preferred_runtime`.

```bash
# Init a worktree for an Open Code implementer
thrum init --runtime=opencode

# Or set it during quickstart
thrum quickstart \
  --name impl_api \
  --role implementer \
  --module api \
  --runtime opencode
```

> **Prefer `thrum tmux quickstart` when using tmux.** If you're launching into a
> tmux session,
> `thrum tmux quickstart <session> --name ... --role ... --module ...` creates
> the session AND registers the agent in one step. The standalone
> `thrum quickstart` is for agents that register themselves after booting.

After either command, the identity file at `.thrum/identities/<name>.json` has:

```json
{
  "version": 5,
  "preferred_runtime": "opencode",
  ...
}
```

When `thrum tmux launch` runs later with no `--runtime` flag, it reads
`preferred_runtime` from the identity file and launches Open Code, not Claude.

You can set a different runtime per launch by passing `--runtime` explicitly.
The `preferred_runtime` value doesn't change — it stays as the default for that
worktree.

---

## Process Detection Across Runtimes

When Thrum needs to find the agent process in a tmux pane, it walks the process
tree from the pane's shell upward, looking for any of the nine known runtime
binaries:

```text
claude  opencode  aider  codex  cursor-agent  agent  gemini  amp  kiro-cli
```

`cursor-agent` and `agent` both map to the `cursor` runtime name. `agent` is the
preferred binary name as of v0.9.0; `cursor-agent` is the legacy name, still
detected for backward compatibility. If another runtime ever claims the `agent`
binary name, we handle it then.

This powers two things:

**Status checks.** The `tmux:alive` vs `tmux:stale` state in `thrum team` and
`thrum tmux status` comes from checking whether the stored `agent_pid` is still
a running process. That check now works for any runtime, not just Claude. When a
runtime pauses for a permission prompt, the process is still running but the
session is effectively blocked. See [Permission Prompts](permission-prompts.md)
for the full detection workflow.

**Auto-detection at prime time.** When `thrum prime` or `thrum quickstart` runs
inside a session, it calls the process walker, finds the actual running runtime,
and writes it to the `runtime` field in the identity file. This is the _what's
actually running_ field — distinct from `preferred_runtime`, which is the _what
should run here_ field.

| Field               | Set by                           | Meaning                                  |
| ------------------- | -------------------------------- | ---------------------------------------- |
| `preferred_runtime` | `thrum init`, `thrum quickstart` | What runtime should run in this worktree |
| `runtime`           | Auto-detected at session start   | What runtime is actually running         |

The distinction matters when you manually launch a different runtime than the
one in `preferred_runtime`. Thrum tracks what's actually there.

---

## tmux Launch and Restart Per Runtime

### Launching

```bash
thrum tmux launch implementer-api
# → resolves runtime from identity/config/default
# → starts the right binary in the tmux pane
# → waits for the pane to produce output
# → sends the prime command for that runtime

thrum tmux launch implementer-api --runtime aider
# → overrides resolution, launches aider regardless of identity file
```

The prime command varies by runtime. Claude Code gets `/thrum:prime`. Open Code
and other runtimes get a shell equivalent that loads the same session context.
The right command is chosen automatically — you don't configure this.

### Restart and Context Snapshots

```bash
thrum tmux restart implementer-api
```

Restart extracts the conversation history, kills the session, creates a fresh
one, and relaunches. The new session loads the snapshot so the agent picks up
where it left off.

**This only works for Claude Code.** Claude stores conversation history in a
JSONL file that Thrum knows how to read and replay. Other runtimes don't use
that format. When you restart a non-Claude session, Thrum skips the snapshot
step and logs a note. The agent relaunches clean, without its prior
conversation.

If you need to preserve context across a non-Claude restart, the options are:

- Have the orchestrator send a summary message to the agent after restart
- Use the runtime's own session-save mechanism if it has one
- For short tasks: just re-send the task description

This is a known limitation, not a bug. Extending restart snapshots to other
runtimes is deferred until there's a clear format to work with.

See [Session Restart](session-restart.md) for the full Claude restart story.

---

## Identity File Fields (Version 5)

The identity file format is version 5 as of this feature. Two changes from
version 4:

**`preferred_runtime`** is new. It's the runtime this worktree should use by
default. Set via `thrum init --runtime=...` or `thrum quickstart --runtime=...`.
Consumed by `thrum tmux launch` and `thrum tmux start` when no `--runtime` flag
is passed.

**`agent_pid`** replaces `claude_pid`. Same semantics — the OS PID of the agent
process running in the tmux pane. The rename drops the Claude-specific name.
PIDs are ephemeral. They're repopulated on every session start, so there's no
migration path for old values — old `"claude_pid"` entries are simply ignored on
next load.

If you read an old identity file and see `"claude_pid"` but no `"agent_pid"`,
that's a v4 file. It works fine — the PID will be repopulated when the agent
next starts.

The full v5 shape, with the relevant fields:

```json
{
  "version": 5,
  "agent_pid": 12345,
  "preferred_runtime": "opencode",
  "runtime": "opencode",
  "tmux_session": "implementer-api:0.0",
  ...
}
```

See [Identity System](identity.md) for the complete field reference.

---

## Config: System-Wide Runtime Default

If you want all new agents to default to a specific runtime without passing
`--runtime` every time, set it in `.thrum/config.json`:

```json
{
  "runtime": {
    "primary": "opencode"
  }
}
```

This is step 3 in the resolution chain. Any worktree without a
`preferred_runtime` in its identity file uses this value. Any worktree with
`preferred_runtime` set ignores it.

The config value is per-repo. If you have separate repos where you want
different defaults, set it in each one's `.thrum/config.json`.

See [Configuration](configuration.md) for the full config reference.

---

## Known Limitations

**Restart snapshots are Claude-only.** When you restart a non-Claude session,
context is not preserved. The agent relaunches without its conversation history.
Use the orchestrator or manual re-briefing to restore context.

**`FindClaudeAncestor()` keeps its name.** The internal function that detects
runtimes is still named after Claude. The behavior is fully runtime-agnostic —
it detects all nine known binaries — but the name is cosmetic legacy. A future
cleanup will rename it. Don't rely on the function name as documentation.

**Cursor's `agent` binary.** The process name `agent` maps to Cursor. If a
different runtime ships a binary named `agent` in the future, detection will
misattribute. This hasn't happened yet.

**`.opencode` shim detection (v0.9.0).** The `.opencode` dot-prefix shim binary
is now detected in the ancestor walk. Before v0.9.0 it was invisible to the
process scan, and opencode panes were misattributed to the parent shell.

---

## Full Setup Example

Here's what a coordinator does to spin up a mixed-runtime team:

```bash
# 1. Create worktrees
git worktree add ../worktrees/api-feature feature/api-refactor
git worktree add ../worktrees/tests-feature feature/api-refactor-tests

# 2. Init each worktree with its preferred runtime
cd ../worktrees/api-feature
thrum init --runtime=opencode

cd ../worktrees/tests-feature
thrum init --runtime=codex

# 3. Create tmux sessions and register agents in one step
# thrum tmux quickstart is an alias for thrum tmux create with quickstart flags
thrum tmux quickstart impl-api \
  --name impl_api --role implementer --module api \
  --cwd ../worktrees/api-feature
thrum tmux quickstart impl-tests \
  --name impl_tests --role implementer --module tests \
  --cwd ../worktrees/tests-feature

# (Steps 3 and 4 are now one command — the agent is already registered when it boots.)

# 5. Launch — each session reads its own preferred_runtime
thrum tmux launch impl-api      # launches opencode (from identity file)
thrum tmux launch impl-tests    # launches codex (from identity file)

# 6. Send tasks
thrum send "Implement the API changes per thrum-zmm.1" --to @impl_api
thrum send "Write tests for the API changes" --to @impl_tests
```

Both agents run different runtimes, receive messages through the same nudge
mechanism, and report status through the same `thrum tmux status` output:

```text
SESSION        AGENT        STATE        RUNTIME    BRANCH
impl-api       impl_api     alive        opencode   feature/api-refactor
impl-tests     impl_tests   alive        codex      feature/api-refactor-tests
```

---

## Next Steps

- [Tmux-Managed Sessions](tmux-sessions.md) — how sessions are created, nudged,
  and monitored; the full lifecycle before you add multi-runtime to the picture
- [Session Restart](session-restart.md) — snapshot extraction and context
  restoration for Claude sessions
- [Identity System](identity.md) — full field reference for the v5 identity file
- [Configuration](configuration.md) — `runtime.primary` and other config keys
- [Orchestrator Role](orchestrator-role.md) — how a coordinator manages a
  mixed-runtime team end-to-end
- [CLI Reference](cli.md) — `thrum tmux launch`, `thrum quickstart`,
  `thrum init` flags
