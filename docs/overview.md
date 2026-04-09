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

## What's New in v0.7.x

- **Orchestrator role** — a dedicated coordinator agent that reads your plan,
  claims tasks, spawns implementers, and stops at every review gate without
  touching the merge button; see [Orchestrator Role](orchestrator-role.md)
- **Multi-runtime support** — Claude Code, Codex, and Aider all work; Thrum
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
