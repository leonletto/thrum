## Team on Your Machine

You run a coordinator agent that holds the plan, a few implementers working
separate worktrees, and a tester checking their output. They message each other
through Thrum. You don't relay anything — you read the summaries, review the
diffs, and merge what passes. It's a team of agents directed by you, not an
autonomous system running loose.

## Prerequisites

- Thrum installed and the daemon running
- Git worktrees: know how to create and switch between them
- Two or more runtime sessions available (tmux panes, terminal tabs, or similar)

## Walkthrough

1. **[Quickstart](../quickstart.md)** — install Thrum, register your first
   agent, and send a message to confirm the daemon is live.
2. **[Multi-Agent Setup](../multi-agent.md)** — create multiple agent identities
   and wire them into separate worktrees.
3. **[Coordinate Two Agents](../guides/coordinate-two-agents.md)** — run a
   coordinator and an implementer together; see how messages flow between them.
4. **[Role Templates](../role-templates.md)** — load predefined coordinator,
   implementer, and tester roles so each agent starts with the right context.
5. **[Messaging](../messaging.md)** — send targeted messages, check inboxes, and
   reply; the full messaging API your agents use to stay in sync.
6. **[Review Workflow](../guides/review-workflow.md)** — route finished work
   back to the coordinator for code review before you merge.
7. **[Tmux Sessions](../tmux-sessions.md)** — keep each agent in its own named
   pane so you can watch them all at once without losing track.

## Control from your phone

Running a long session and stepping away? The
[Telegram Bridge](../telegram-bridge.md) puts your agent inbox in your pocket.
Send instructions, read status updates, and approve work from anywhere.

## When you're ready for more

- [Agents Across Repos/Machines](across-boundaries.md) — spread your team across
  multiple repositories or remote machines.
- [Automated Plan Execution](orchestration.md) — hand a full spec to a
  coordinator and let it break down and dispatch the work.
