## Thrum

I built Thrum so you can run several AI coding agents in parallel without
becoming the message relay yourself. You do the thinking — research, plan,
approve. Agents do the typing. Thrum routes messages between them, keeps
sessions alive, and stays out of your way. It doesn't plan your work or make
decisions for you. [That's a deliberate choice.](philosophy.md)

## What You Can Build With It

Here are the four main shapes of how people use Thrum today. Each one is a
complete workflow, not a feature list. Pick the one that matches where you are
right now — solo with one agent, a local team of agents, agents spread across
repos or machines, or fully automated plan execution. They're not mutually
exclusive. Most people start with the first and add complexity only when they
need it.

<div class="scenario-card-grid">
  <a href="scenarios/solo-dev.html" class="card scenario-card">
    <div class="feature-icon">&gt;_</div>
    <h3>Solo Dev with One Agent</h3>
    <p>One agent, your machine, no hand-holding. Thrum keeps the session
    alive in tmux, tracks identity across context resets, and lets you
    check in from your phone via Telegram.</p>
    <span class="scenario-cta">Walk through the setup →</span>
  </a>

  <a href="scenarios/team.html" class="card scenario-card">
    <div class="feature-icon">@</div>
    <h3>Team on Your Machine</h3>
    <p>Two, three, or ten agents in parallel worktrees. A coordinator
    plans, implementers build, a tester verifies. You review and merge.
    Messaging runs locally; Telegram works here too.</p>
    <span class="scenario-cta">Walk through the setup →</span>
  </a>

  <a href="scenarios/across-boundaries.html" class="card scenario-card">
    <div class="feature-icon">&#x21C4;</div>
    <h3>Agents Across Repos/Machines</h3>
    <p>Backend agents in one repo talk to frontend agents in another.
    Your home desktop and your work laptop participate in one team mesh.
    Same-subnet or Tailscale — your pick.</p>
    <span class="scenario-cta">Walk through the setup →</span>
  </a>

  <a href="scenarios/orchestration.html" class="card scenario-card">
    <div class="feature-icon">&#x26A1;</div>
    <h3>Automated Plan Execution</h3>
    <p>You wrote a plan. Hand it to the orchestrator. It spins up
    implementer agents, runs them epic by epic, stops at review gates,
    and hands you a merge report. You still merge.</p>
    <span class="scenario-cta">Walk through the setup →</span>
  </a>
</div>

## What's New in v0.9.0

- **Permission-prompt detection** — the daemon detects when a tmux-managed agent
  hits a blocking permission prompt and routes an actionable nudge to configured
  supervisors. Approve or deny from the CLI, web UI, or Telegram. See
  [Permission Prompt Detection](permission-prompts.md).
- **Identity guards** — cross-worktree CWD enforcement hard-errors on CWD drift
  instead of silently misattributing actions to the wrong agent. See
  [Troubleshooting: Identity](troubleshooting-identity.md).
- **CLI hints (Phase B)** — contextual guidance printed before and after
  destructive or multi-step commands. See [CLI Hints](cli-hints.md).
- **Drift reconciliation** — peers self-heal address drift automatically without
  re-pairing. See [Peers](peers.md).
- **`thrum tmux quickstart`** — alias for `thrum tmux create`. Same command,
  clearer name. `thrum tmux create` now requires `--name`, `--role`, `--module`
  (or `--no-agent`) and runs quickstart inside the new pane automatically.
- **`thrum worktree setup`** — alias for `thrum worktree create`. Both commands
  now accept optional quickstart flags (`--name`, `--role`, `--module`,
  `--intent`, `--runtime`). Provide all three required flags and it creates a
  real tmux session, runs quickstart inside it, and prints the next-step
  `thrum tmux launch <name>` command. The agent identity is registered, but the
  runtime is not started until `tmux launch` runs — a clean two-step pattern
  with no manual `thrum quickstart`.
- **Single identity per worktree** — quickstart cleans up old identity files
  after writing the new one. You can't end up with a stale identity causing
  auto-select errors.
- **`thrum tmux launch` hard-errors on missing identity** — launch needs an
  agent identity to determine the runtime. Sessions created with `--no-agent`
  (or worktrees with no identity file) cannot be launched until you register an
  agent first.
- **Next-step guard messages** — `agent register`, `worktree create` (no agent),
  `purge --confirm`, `daemon stop`, `tmux restart`, and `tmux launch` now print
  explicit hints about what to do next (or what just broke).
- **Monitor Jobs v1** — `thrum monitor start/list/show/stop/logs/restart`.
  Attach a monitor to any long-running process and it emits matches as synthetic
  Thrum messages. Leading-edge debounce (default 60s, min 30s), auto-persist,
  local-socket-only.

## Breaking Changes (v0.9.0)

If you're upgrading from v0.8.x, read this section before starting the daemon.

- **Forged `caller_agent_id` rejected** — pre-v0.9.0 callers could pass any
  `caller_agent_id` and it was accepted at face value. Now cross-checked against
  kernel-verified PID resolution. Mismatches get "identity mismatch" and are
  dropped.
- **WebSocket non-localhost origin → HTTP 403** — pre-v0.9.0, `CheckOrigin`
  returned `true` for all origins. Browser pages on non-loopback origins can no
  longer upgrade to WebSocket.
- **`message.delete` by non-author rejected** — only the original author can
  soft-delete their own messages.
- **`message.deleteByAgent` requires caller == target** — agents can only
  bulk-delete their own messages.
- **`message.deleteByScope` is daemon-internal only** — no longer callable from
  any external client (CLI, browser, or unix socket).
- **Legacy daemon_id auto-rotated** — first v0.9.0 daemon start rotates
  pre-existing hostname-derived daemon IDs to ULID format. Existing peer pairs
  must be re-paired: `thrum peer remove <name>` then
  `thrum peer add --type tailscale <name>` on each peer.
- **Downgrade blocked by migration guard** — running a v0.8.x binary against a
  database migrated to schema v24 fails with a clear error. Recovery: stop
  daemon → restore `thrum.db.pre-migration-v<N>-bak` → run the older binary.
- **`~/.thrum/runtimes.json` replaces the old platform config path** — silently
  dropped on upgrade; custom runtimes disappear. Move the file manually: Linux:
  `~/.config/thrum/runtimes.json` → `~/.thrum/runtimes.json`; macOS:
  `~/Library/Application Support/thrum/runtimes.json` →
  `~/.thrum/runtimes.json`.
- **`peer add --type` and `peer join --type` are now mandatory** — the
  previously implicit `tailscale` default is gone. Add `--type tailscale` to any
  existing scripts that omit it.
- **`alert-silence` hook no longer triggers permission-prompt detection** —
  daemon-side poller replaces it (~20s detection latency). Existing
  `alert-silence` config in `.tmux.conf` is inert for this purpose.
- **Identity guards hard-error on CWD drift** — running thrum from the wrong CWD
  fails with `identity guard "cross_worktree" fired: pid_mismatch` instead of
  silently misattributing. Use `THRUM_HOME` to pin repo path. See
  [Troubleshooting: Identity](troubleshooting-identity.md).
- **`thrum daemon start` and `thrum init` refuse non-git directories** — pass
  `--force` for non-anchored use.
- **`thrum quickstart` refuses self-rename / name collision** — without
  `--force`. Previously a silent overwrite.
- **Telegram fresh-DM `y` resolves a pending nudge instead of routing to
  `--target`** — if the sender has a pending permission nudge, a bare
  `y`/`n`/`yes`/`no`/`allow`/`deny` DM resolves that nudge rather than
  delivering to the configured target agent.

## What's New in v0.7.x

- **Orchestrator role** — a dedicated coordinator agent that reads your plan,
  claims tasks, spawns implementers, and stops at every review gate without
  touching the merge button; see [Orchestrator Role](orchestrator-role.md)
- **Multi-runtime support** — Claude Code, Codex, OpenCode, etc. all work; Thrum
  picks the right tmux launch command for each; see
  [Multi-Runtime](multi-runtime.md)
- **Peer mesh** — agents on different machines join one team over Tailscale or
  local network with no extra servers; see [Peers](peers.md)
- **Single-agent mode** — Thrum's context management and session tracking work
  without any messaging layer; it's now the default for new installs; see
  [Single-Agent Mode](single-agent-mode.md)
- **Daemon-managed tmux sessions** — the daemon owns the session lifecycle,
  delivers messages the moment they arrive, and runs zero background listeners
  in the agent process; see [Tmux Sessions](tmux-sessions.md)
- **Command queue dispatch** — coordinators submit commands to agent panes via
  `thrum tmux queue`, with completion tracking, `@system` notifications, and
  restart recovery; see
  [Tmux Sessions — Queue Dispatch](tmux-sessions.md#command-queue-dispatch)
- **Worktree management** — `thrum worktree create/teardown/list` (alias:
  `thrum worktree setup`) handles git worktree setup with automatic Thrum and
  Beads redirect wiring
- **Daemon logging** — structured slog output with lumberjack rotation; view
  with `thrum daemon logs`; configurable via `daemon.log_level`
- **Orchestrator role** — a dedicated role for plan execution with review gates,
  worktree lifecycle, and agent spawning; see
  [Orchestrator Role](orchestrator-role.md)

## Further Reading

- [Why Thrum Exists](philosophy.md) — the reasoning behind human-directed agent
  coordination and what Thrum deliberately doesn't do
- [Quickstart Guide](quickstart.md) — install Thrum, start the daemon, and get
  your first agent running in under five minutes
- [CLI Reference](cli.md) — every command, flag, and alias; what's for you,
  what's for agents, and what you run once at setup
- [Architecture](architecture.md) — daemon internals, JSONL event log, SQLite
  projection, sync protocol, and peer transport
- [Agent Coordination](agent-coordination.md) — practical patterns for running
  multiple agents in parallel and integrating with Beads for task tracking
- [Permission Prompt Detection](permission-prompts.md) — how the daemon detects
  blocked agents, routes supervisor nudges, and accepts approvals from CLI, web,
  or Telegram
- [Security Model](security-model.md) — local trust stack, identity guards,
  WebSocket origin enforcement, and message author controls
- [CLI Hints](cli-hints.md) — contextual guidance printed around destructive and
  multi-step commands; how to suppress them
- [Troubleshooting: Identity](troubleshooting-identity.md) — diagnosing and
  recovering from identity guard errors, CWD drift, and name collision failures
