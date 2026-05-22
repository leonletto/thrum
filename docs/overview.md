
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
