---
title: "Three Days, Four Foundations"
slug: "v0-7-three-days-four-foundations"
date: "2026-04-08"
author: "Leon Letto"
description:
  "The plan for April 6 was to figure out how a Claude Code agent could come
  back from a context compaction without losing its place. By April 8 the line
  had grown into four pieces of foundation, and most of the listener scaffolding
  I'd been carrying since v0.5 had become optional."
tags: ["release", "v0.7", "restart", "tmux", "transport"]
draft: false
---

The plan for April 6 was simple. I wanted to figure out how a Claude Code agent
could come back from a context compaction without losing its place. By the end
of April 8, v0.7.2 had shipped, and the line had grown into four pieces of
foundation underneath the messaging system: a session restart feature, a
tmux-first session model, a PID-based identity resolver, and a cross-repo
transport architecture. Three of those four weren't in the plan when April 6
started.

This is the walk through how that happened.

## The Conversation That Designed Restart

I sat down with the coordinator on the morning of April 6 thinking about pane
captures. If an agent was about to hit a context-window compaction, I figured we
could capture the visible pane state and pass it forward as a summary into the
next session. The coordinator started designing around that. About halfway
through, I had a different thought.

> "I'm pretty sure there's a way to find the session.jsonl files as well from
> the path where the Claude Code agent is running. This would allow looking at
> the actual conversation that has happened in the session and then deciding how
> we want to summarize it and just doing it that way."

Claude Code already keeps a JSONL of the entire conversation on disk. There was
no reason to scrape a pane when the source of truth was right there. The
coordinator pivoted to that idea, and we spent the next hour working out how to
truncate the file usefully. The key call was scope:

> "I'm pretty sure we don't need the output of tool uses. If there is a way to
> just dump the conversation, then that only becomes at most a thousand or two
> thousand lines, which can be fed back into the next session as 'here is what
> was done in the previous session' and we are picking up from there. That
> actually doesn't use too much context."

That's the design call that made the feature small enough to ship. Exclude tool
output, keep user and assistant turns, truncate from the top, preserve recent
context.

To verify the approach we needed to actually look at a JSONL from a live
session. The coordinator tried `thrum tmux create test-jsonl` and got
`unknown command "tmux"`. The handler didn't exist yet in the running daemon. We
hadn't built it yet that session, or we had and the daemon needed a restart. I
gave the obvious answer:

> "Go ahead and make install; then it's available to you."

So the next ten minutes were: build the missing tmux subcommands, install them,
restart the daemon to pick up the new RPC handler, spin up a fresh Claude
session in a test worktree, ask it a question that required tool use, examine
the resulting JSONL. The feature was being designed, built, and tested in the
same session, in tools that were also being built in the same session.

## What `thrum tmux start` Replaced

Late on April 6, around quarter to eleven, the three-command launch sequence
we'd been using to start an agent in a tmux session was annoying me. Create the
session. Launch the runtime in it. Send `/thrum:prime` once the runtime was
ready. Three commands every time I wanted a fresh agent.

> "A. if we're going to use Thrum Tmux Start Then that's very specific to
> launching in the current directory and replacing those three commands. Use
> whatever the default agent is configured in the repo. In this case, it's
> Claude, but it could be Codex."

The coordinator built it in a few minutes. Single command: create the session,
launch the configured runtime (Claude, or Codex, or whatever the repo
specifies), send prime, attach the terminal. We tested it once.

> "Worked perfectly."

The bigger thing this unlocked was less obvious. The daemon already owns those
sessions. It can send a message directly into the pane the agent is running in.
There's no reason for the agent to spawn a background `thrum wait` process to
poll for messages, which is what the listener pattern requires. v0.7.1 made that
explicit: the plugin's SKILL.md got rewritten to recommend tmux sessions as the
primary delivery mechanism, with the listener demoted to a fallback for
environments where tmux isn't available. The Stop hook and the post-compact hook
both learned to skip the listener checks for tmux-managed agents. The cron
watchdog from v0.6.3 still works. It just isn't the path I'd point a new user at
anymore.

## The Morning the Identity Wouldn't Stick

April 7 morning. I was testing PID identity resolution against the website-dev
implementer session, which was live in tmux at the time. The idea was that every
identity should carry the PID of the Claude process that owned it.
`thrum quickstart` should walk up the process tree, find the Claude PID, and
write it into the identity file. `thrum team` would then show `[live]` next to
identities whose owning process was still around, and `[stale]` next to
identities whose process had gone away.

I ran quickstart in the website-dev pane. The `claude_pid` field was empty. The
coordinator pushed a binary fix. I ran quickstart again. Still empty. Another
fix. Still empty. Three iterations in, the coordinator started asking me to dump
environment variables.

> "echo TMUX=$TMUX"

TMUX was set, the way it should be. The PID walk should have been working. The
fourth iteration found the actual problem, which had nothing to do with the
process tree walk. It was the JSONL session lookup. Claude Code encodes the
working directory into the JSONL filename by replacing certain characters in the
path. The encoding I'd implemented replaced `/` with `-`, which matched Claude
Code's encoding for normal paths. But the website-dev worktree was under
`.workspaces`, and Claude Code also encodes `.` as `-`. My code didn't. So the
session lookup was reading from a file path that didn't exist, the PID lookup
failed silently, and `claude_pid` came out empty every time.

The fix was a one-character change. Add `.` to the set of characters that get
replaced. That's the kind of bug you find because you tried to use the feature
you built, and the kind of bug that doesn't surface in unit tests because unit
tests pick their own paths.

## What Got Consumed by the Hook

The session restart feature shipped in v0.7.1 with the supporting
infrastructure: snapshot save and restore commands, the `thrum tmux restart` RPC
for coordinator-initiated restarts, the `/thrum:restart` skill for
agent-initiated ones, an auto-restart threshold, and automatic inclusion of the
snapshot in the next session's `thrum prime` output.

Then the Claude Code plugin needed a SessionStart hook to actually fire on agent
restart. v0.7.1 added one. The hook ran `thrum prime` directly. It didn't work.

> "I don't quite understand why we're running thrum prime inside the hook. That
> goes into the system reminder, but it does not hit the context, and the agent
> does not see it like it does when you run thrum prime from within the session.
> That's an invalid flow."

Claude Code's hook output gets injected into the agent's context as a
system-reminder. That's a read-only context. The agent sees it but can't take
the next step from it. So the hook was technically running prime, the snapshot
was technically being loaded, and the restored session-context was technically
being delivered, all in a channel the agent couldn't actually act on. The
snapshot was getting marked as consumed without the agent ever seeing it as
something to act on.

A minute later, the deeper reframe:

> "This is the problem, I think. Why wouldn't it just tell the agent to run
> thrum prime so it appears in context? That's what a system reminder normally
> does: reminding it to do something so it does it in context after starting."

The fix in v0.7.2 was a one-line change in the hook. Replace
`thrum prime 2>/dev/null` with
`echo 'Run /thrum:prime to load your session context, identity, and any restart snapshots.'`.
The reminder tells the agent to run prime as a real action. The agent runs it.
The snapshot loads into the context where the agent can use it. Same data,
different channel, working feature.

## The Plumbing That Didn't Have a Story

v0.7.0 also shipped the cross-repo transport architecture, and there's no clean
narrative for it because none of the conversations from this window were about
it. The decisions had been made earlier. The three days of April 6 through 8
were about wiring it together.

The shape that landed: four layers, with a Network layer at the bottom, a
Transport Bridge above it, then Routing, then the Application layer that knows
about messages and agents. The interesting layer was the Transport Bridge.
Lifting it into a shared `internal/bridge/` package meant the same primitives
could carry sync, peer-to-peer traffic, and the Telegram bridge. The Tailscale
sync code, which had been on raw TCP and NDJSON since v0.5.9, got migrated onto
the new WebSocket transport. That was a breaking change for anyone with paired
Tailscale peers. It required re-pairing, and there was no clean way around it
without keeping two parallel transports forever.

Telegram groups landed in the same release, on the same architecture, with
`@mention` routing for human-to-agent messaging in group chats and an IsBot
trust gate so a random bot in the group couldn't talk to your agents.

This is the plumbing. It absorbed real days of work. It doesn't have a clean
human story because the work didn't surface frustrations or pivots or quotable
corrections. It just got built, and the rest of v0.7 leaned on it: tmux-first
depended on the bridge primitives, the Telegram group bridge depended on the
same shared package, and the next release line's cross-repo work depended on
both.

## What Three Days Cost and Bought

The cost was a release line that didn't sit still. v0.7.0 shipped on April 6
with the transport architecture and the PID identity work. v0.7.1 shipped on
April 7 with session restart and tmux-first session management. v0.7.2 shipped
on April 8 with the cleanup pass that followed from actually using what had been
built the day before: the hook bug, the path encoding bug, the
daemon-in-nested-tmux env strip, the identity reload guard. Three days, three
releases, plus a lot of velocity that doesn't make it into release notes.

The benefit was that the agents stopped falling apart. Agents that know whether
they're alive (PID identity). Agents that can reach across machines and repos
(transport). Agents that don't need a background listener to receive messages
(tmux-first sessions). Agents that survive a context-window compaction with
their plan intact (session restart). Each of those replaced a workaround I'd
been carrying since v0.5 or earlier. The cron watchdog from v0.6.3, the listener
loop, the manual re-registration after a restart, the proxy agent for cross-repo
messaging: all of those still work, none of them are the recommended path
anymore.

The heuristic I take from this line: some releases are accumulations and some
are foundations, and you can tell which is which by whether the new release lets
you delete patches from earlier ones. v0.7 let me start deleting.
