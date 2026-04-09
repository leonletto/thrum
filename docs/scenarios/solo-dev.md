## Solo Dev with One Agent

This is where most people should start. One machine, one agent, you in control.
You do the research and planning, your agent does the typing. Thrum keeps the
session alive, routes messages to your phone when you're away from the terminal,
and gets out of your way the rest of the time. If you've never used Thrum
before, start here.

## Prerequisites

- Thrum installed and on your `$PATH`
- Thrum daemon running (`thrum daemon start`)
- A runtime ready — Claude Code, Codex, Aider, or anything that runs in a
  terminal

## Walkthrough

1. **[Quickstart](../quickstart.md)** — install Thrum, start the daemon, and
   register your first agent
2. **[Single Agent Mode](../single-agent-mode.md)** — use Thrum without any
   messaging overhead; just you and one agent
3. **[Tmux Sessions](../tmux-sessions.md)** — run your agent in a persistent
   tmux session so it keeps working when you close the terminal
4. **[Multi-Runtime Support](../multi-runtime.md)** — pick the runtime that fits
   your workflow; Claude Code, Codex, OpenCode, etc. all work
5. **[Session Restart](../session-restart.md)** — recover cleanly after context
   loss without losing your place
6. **[Identity](../identity.md)** — how Thrum knows who your agent is and how
   that persists across restarts

## Control from Your Phone

Connect the [Telegram Bridge](../telegram-bridge.md) and you don't need to stay
at your desk. Thrum pings you when your agent hits a decision point or needs
input. You reply from Telegram and the conversation threads back into the
session — no terminal required. Close your laptop and your agent keeps running
in tmux.

## When You're Ready for More

- [Team on Your Machine](team.md) — run two or more agents in parallel on
  separate worktrees
- [Automated Plan Execution](orchestration.md) — hand your agent a spec and let
  it claim tasks and commit autonomously
