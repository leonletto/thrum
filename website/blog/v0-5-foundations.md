---
title: "Building the Floor"
slug: "v0-5-foundations"
date: "2026-03-09"
author: "Leon Letto"
description:
  "v0.5.0 through v0.5.5: the line where the glamour shipped (a Slack-style web
  UI) on top of the load-bearing work that nobody is going to remember (durable
  read receipts, pinned worktree identity, role templates rewritten after a
  31-task multi-agent session). I started Thrum in late January and the first
  eight weeks were too fluid to write about. This post is what I can
  reconstruct."
tags: ["release", "v0.5", "ui", "identity"]
---

I started this project in late January. The first eight or nine weeks were fluid
enough that it's hard now to find the narrative to talk about it as a blog post.
The episodic-memory plugin that records my conversations with the coordinator
wasn't running yet, so I have no transcripts to go back to. Decisions came and
went on the same day. The agents I was building it with lost most of the work to
context compactions before any of it became durable. By the time the line that
became v0.5 settled into a shape worth describing, it was a few weeks of
accumulated momentum that already had its own grammar.

This post is therefore reconstructed from the CHANGELOG and what I remember. I
will be brief where I cannot say more than the release notes already do, and I
will mark where a real moment landed when I can.

The window covered here is v0.5.0 (February 23) through v0.5.5 (March 9). I'll
do v0.5.6 through v0.5.9, where the archive does pick up, in a second post.

## The Web UI Was the Glamour

v0.5.0 on February 23 was the biggest single release of the line. The web
dashboard got rebuilt from scratch as a Slack-style three-panel interface:
sidebar navigation on the left, a list view in the middle, a context panel on
the right. Live Feed for the activity stream. My Inbox for direct messages.
Group Channels with member management and channel-scoped messaging. Agent Inbox
with context (intent, branch, session info). Who Has? for file coordination, so
I could see which agent was editing what. Keyboard shortcuts for the views,
`Cmd+K` for search. ComposeBar with `@mention` autocomplete. Unread badges.
Message deep-linking from Live Feed into inbox conversations.

This was the first version of Thrum that I could show someone and have them
understand what it was without watching me run CLI commands for ten minutes. The
CHANGELOG entry for v0.5.0 is the longest single entry in the whole v0.5
section, and the web UI is most of it.

The thing the UI was sitting on top of mattered more, though. v0.4.5 the week
before had landed `safedb` (compile-time context enforcement on every SQLite
query), `safecmd` (context-aware git and exec wrappers with timeouts), name-only
routing (messages route by agent name and group membership; role strings stopped
matching inboxes), and a resilience test suite of 32 scenarios covering RPC,
concurrency, crash recovery, and multi-daemon races. v0.5.0 was the visible
layer on top of a substrate that had finally stopped breaking under load.

The visible-versus-load-bearing pattern carries the rest of this post.

## The Worktree Identity Problem

March 6, v0.5.3. Three new environment variables landed: `THRUM_HOME`,
`THRUM_AGENT_ID`, `THRUM_NAME`. Agents working in git worktrees stopped silently
drifting to the daemon's default identity.

The problem had been quietly poisoning multi-agent workflows for weeks. Each
agent in a session ran in its own worktree of the repo. The daemon ran out of
whichever worktree it had been started in. When the CLI in worktree B asked the
daemon "who am I?", the resolution path had no concept of caller worktree, so it
answered with the daemon's own anchor (worktree A). Messages got routed to the
wrong agent. Status reports landed under the wrong identity. Two agents would
each think they were the coordinator. The web UI Live Feed would show traffic
attributed to whoever the daemon had registered first.

v0.5.3 fixed this by pinning identity at the CLI layer. The startup script set
`THRUM_HOME`, `THRUM_AGENT_ID`, and `THRUM_NAME` when Claude launched, and every
CLI command then bound to its home repo via `--repo "$THRUM_HOME"`. The env vars
also got persisted into Claude Code's session environment through
`CLAUDE_ENV_FILE`, so SessionStart hooks saw the right identity from boot.

This is the same `THRUM_HOME` machinery that gave us trouble again in v0.10,
when a primed shell's env leaked into a daemon and that daemon then leaked it
into every tmux pane it spawned. The footgun was born here in v0.5.3. The fix
was correct for the scope it covered. The scope just kept expanding.

Same release also embedded three strategy reference files (sub-agent dispatch,
registration, resume-after-context-loss) into the binary itself, so `thrum init`
could write them out to `.thrum/strategies/` without needing a network
round-trip. The preamble pointed agents at the strategies directory for
operational patterns. Less giant-block-of-rules in the preamble, more "read X if
you hit Y."

## Did My Message Arrive

March 9 morning, v0.5.4. The new command was `thrum sent`, and the thing it gave
you was the answer to "did anyone read what I sent." Before v0.5.4, sending a
message was fire and forget. The daemon accepted it, wrote it to the JSONL, and
the recipient might or might not have ever seen it. There was no command to ask.

After v0.5.4, every `send` wrote durable rows into a new `message_deliveries`
table tracking each recipient with a `delivered_at` and a `read_at` timestamp.
`mark-read` updated the receipts. `thrum sent` listed your outbound messages
with read status per recipient. `thrum sent --unread` filtered to messages whose
recipients had not opened them yet. `thrum sent show MSG_ID` gave you the full
recipient breakdown for a single message.

The CHANGELOG framing for this release was "eliminating guesswork about
routing." That was the truth of it. The first time I ran `thrum sent --unread`
against my own outbox and saw four messages I had assumed were received and
weren't, the system felt like something different than it had felt the day
before.

Same release also fixed a latent bug in `thrum wait`. The wait command wasn't
waking for direct mentions in replies, and it wasn't waking for group messages
where the agent was a member. Inbox display rules and wait rules had drifted.
v0.5.4 aligned them, and `wait` finally returned only what the agent was
actually addressed by.

## What a 31-Task Session Taught the Templates

March 9 evening, v0.5.5. Same day as v0.5.4. I had spent some hours that week
running a 31-task multi-agent session. I don't have the transcript of it (the
archive doesn't go back that far), but the CHANGELOG entry that came out of it
is unusually specific: "Role templates updated with learnings from a 31-task
multi-agent session: mandatory sub-agent delegation, CAN/CANNOT scope
boundaries, background listener pattern, and `thrum sent` integration."

The shape of those four changes tells you what went wrong during the test.

**Mandatory sub-agent delegation.** Implementer agents were doing their own work
directly in their main session instead of dispatching to sub-agents. This burned
context fast and meant that any restart lost the whole investigation. v0.5.5
baked into the implementer role template a 5-step task protocol with required
sub-agent delegation for research and verification.

**CAN/CANNOT scope boundaries.** Coordinators were editing implementer code.
Implementers were editing other implementers' worktrees. Researchers were
committing. None of these were catastrophic on their own, but they accumulated
into a mess where you couldn't tell from a diff which agent had touched what.
v0.5.5 wrote CAN and CANNOT lists into each role template so every agent knew
its authority surface at registration time, not by inference from past behavior.

**Background listener pattern.** Agents needed to receive messages without
blocking their primary work. The pattern that fell out (a background
`thrum wait` process that survived context compactions through a heartbeat file)
got encoded into every role template that needed it. The cron watchdog from
v0.6.3 was still two weeks away from being invented; this was the half-step
before it.

**`thrum sent --unread`.** Coordinators had a habit of sending a directive and
then forgetting whether the recipient acted on it. The new command went into the
preamble and the strategies, so the coordinator could routinely scan for un-read
directives before declaring something done.

There was one safety addition from v0.5.5 worth naming. The `DefaultPreamble`
got a line warning agents against running `thrum context save` manually. That
command was destroying accumulated session state when called outside the proper
flow, and agents were casually calling it on the theory that "saving is always
good." The fix was to direct them at the `/thrum:update-context` skill instead,
which preserves state correctly. It turns out that "saving is always good" is
wrong when the save command destroys the state it was supposed to preserve.

## What the Floor Was For

The cost of v0.5.0 through v0.5.5 was that I spent two weeks shipping work most
of which was load-bearing in a way that doesn't take a screenshot. The web UI
took the screenshots. Everything else just made the messages route correctly and
the identities pin and the deliveries stick and the agents not destroy their own
context.

The benefit was that by March 9 the system had a working foundation. The next
four days of patches (v0.5.6 through v0.5.9, covered in the next post) were
about operational hardening on top of that floor, leading up to the first
cross-machine sync session over Tailscale.

The heuristic from this stretch: the first version of any system that survives
real use is the version after the first user. Until then you are designing for
what you imagined the system would need. v0.5.0 was the version I designed for.
v0.5.5 was the version a 31-task session told me to design.
