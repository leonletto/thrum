---
title: "Four Patches in Ten Days"
slug: "v0-6-release"
date: "2026-03-28"
author: "Leon Letto"
description:
  "Native Telegram was the headline of v0.6. Underneath: a daemon restart aimed
  at the wrong repo, a purge command that kept losing to its own sync layer, and
  a watchdog pattern invented overnight."
tags: ["release", "v0.6", "telegram"]
draft: false
---

There was no roadmap for v0.6. There was a plan to keep the v0.5 work usable
while I extended Tailscale peering to another repo. Ten days later, the line had
grown to four patches. One of them a brand-new Telegram bridge that didn't exist
when the line started. One of them a sync model that had never been introduced
to the deletion model. One of them a watchdog pattern that got invented in the
middle of a conversation at three in the morning.

This is what shipping at the speed of friction looks like. Every patch in v0.6
came from hitting a wall in real use, not from a backlog.

## The Wrong Name for the Right Machine

The line opened with what was supposed to be a routine extension: Tailscale sync
had been live on the main repo since v0.5.9, and the remote agent immediately
asked whether `falcon-backend` could join the mesh too. It could. `.thrum/.env`
on falcon-backend, port 9101 to avoid colliding with thrum on 9100, hostname
`leons-mac-m1-pro-falcon`.

Then the coordinator restarted the daemon. The wrong daemon. CWD was still
pointing at the thrum repo, so that's what got restarted. Self-caught one
command later, but it's the kind of mistake multi-repo coordination produces at
every step, and the kind of mistake a tighter `--repo` story has to anticipate,
because the next agent isn't going to catch it.

The more substantive issue was a category error in the peer config. I'd been
using tsnet hostnames where I should have been using IPs.

> "we need to focus on using the tailscale IP for all real configuration and the
> names just as names and not hostnames. This is causing problems."

Names are for display. They're not reliable network addresses when you're
crossing machine boundaries through Tailscale. The correction landed in v0.6.0
as a real split: `peer add` stores tsnet hostnames for readability, `peer join`
resolves to IPs for the actual connection. `peer join` also started accepting a
peercode as a positional argument, not just `--peercode`. The remote agent had
hit a "flag needs an argument" error trying to pipe the code. That's the kind of
small friction that stops a workflow cold.

## Gated

On March 20 I was testing Anthropic's official Telegram Claude Code plugin.
Outbound worked fine. Messages going the other direction, from Telegram into the
session, weren't appearing.

I checked the MCP server code. Verified the process connections. Confirmed the
protocol was implemented correctly on our end. The inbound channel was closed.

> "The `--channels` feature is gated behind a server-side feature flag
> (`tengu_harbor`) that Anthropic controls. It's not enabled for most users
> yet."

A gated feature is a feature you don't have. I didn't want to wait for a
rollout. The Grammy library powering the Telegram side is open source. Thrum
already has a daemon with WebSocket RPC. The question was whether we could build
the bridge ourselves.

> "Can you investigate how hard it would be to add this support to thrum using
> the same code since its open source and here?"

A sub-agent explored Grammy alongside Thrum's daemon architecture. The answer
was yes. Implement Telegram as an isolated WebSocket RPC client in the Go
daemon, fail-closed security boundary, no internal Go imports, so a broken
Telegram config can't take down the rest of the system.

v0.6.0 shipped March 21 with the full bridge: bidirectional messaging,
allowlist-based access control, per-user rate limiting,
`thrum telegram configure/status/pair`, and a settings panel in the web UI. The
same release also added role-aware preambles across nine roles and switched the
default inbox to ConversationView.

The Telegram bridge worked. But configuring it required a manual step that a
developer can do and nobody else can: you had to look up your Telegram user ID
yourself and paste it into the config. v0.6.1 closed that gap.
`thrum telegram configure` now restarts the daemon into pairing mode and
captures your user ID from the first message you send. You don't need to know
what a Telegram user ID is. You send a message and it records who you are.

## Purge Didn't Know About Sync

On March 25 I reported a problem that had been quietly bothering me in a
multi-machine setup.

> "I have been using `thrum purge --before 1d --confirm` in another environment
> and the messages are not purging from all the agents fully. Also, when I
> delete an agent from the web console, sometimes it comes back and I have to
> delete it again. I think maybe the sync is the cause."

The guess was right.

Thrum stores events in two places: an append-only JSONL log, and a SQLite
database rebuilt from that log. When `purge` ran, it removed rows from SQLite.
The JSONL log was untouched. Sync works by replaying JSONL events on peers. So a
peer would sync, find those events in the log, replay them into SQLite, and the
deleted messages and agents would come back. Every time. Purge was local. Sync
wasn't. The two systems had never been introduced.

The fix required teaching sync what deletion means. There is no clean way to
represent "this data should not exist" in an append-only log without adding
something. We added a `purge.executed` event type: when you run purge, it writes
a tombstone event into the JSONL log. A new `purge_metadata` table (schema v15)
stores the latest purge cutoff. When a peer syncs, `SyncApplier` checks that
cutoff and rejects any incoming events older than it. The tombstone travels with
the data; deletion propagates.

Agent cleanup got the same treatment in v0.6.2: deleting an agent now fully
scrubs its messages, sessions, and events, not just the agent row. That's what
makes the tombstone stick.

Seven tasks, five commits, a full test plan. The root cause was straightforward
once named: purge had never been designed with sync in mind.

## What the Cron Job Was Actually For

A few days later I was looking at a different problem entirely. The night of
March 27 into March 28, my coordinator session was manually re-arming a message
listener every eight minutes. Five or six cycles of timeout, re-arm, wait,
timeout. It worked. Every cycle was pure overhead: a new agent spawned, tokens
spent, nothing learned.

The agent proposed two options: increase the timeout, or make the listener loop
internally. I pointed out that I'd thought it already looped internally.

> "I thought that was how it already worked?"

It was supposed to. The documentation said the listener loops up to ten cycles.
The agent definition had a budget of 20 Bash calls, which did allow ten cycles,
but at about 80 minutes total, not four hours. We updated budgets across five
files: 20 Bash calls to 62, ten cycles to 30, listener window to roughly four
hours. Better.

Then I mentioned `/loop`, a skill from a separate project. Could it replace the
background listener pattern entirely? The agent explored it. `/loop` runs a
prompt on an interval, which sounded right. The problem showed up the moment we
tried it with `thrum wait`: it fired into the foreground session and blocked it.
I interrupted it mid-run.

> "Is there no way for it to run backgrounded like before?"

That question opened the third option. Not "increase the budget" and not "use
`/loop` as the worker." Use a cron job as a guardian: check whether a listener
is running, and if one isn't, spawn one. The cron job doesn't do the listening.
It just makes sure something is.

That's the watchdog. A `CronCreate` with the prompt "If there is no background
message listener running, spawn one now." We found the pattern, built it, and
shipped it as v0.6.3 the same day. The CHANGELOG notes a 65% reduction in
listener token consumption compared to the manual re-arm model. That number came
after, not before. We were solving the problem in front of us. The efficiency
was what fell out.

## What Ten Days Cost, And Bought

Four patches in ten days is a cadence that only makes sense if you don't fight
it. None of these patches were on a roadmap. All of them came from somebody (me,
or a remote agent, or a coordinator on a sleepless overnight) hitting an edge
during real use. The wrong-daemon restart, the gated inbound channel, the
resurrecting agents, the listener that didn't loop as long as the docs claimed.
They surfaced because the system was being used, not because I was hunting for
them.

The cost was a release line that didn't sit still. The benefit was that the v0.6
line ends as a substantially different product than it started: native Telegram
instead of a gated dependency, sync that understands deletion, a watchdog
pattern that I've since reused for other long-running coordinator tasks.
Friction-driven shipping isn't the cleanest model on paper. In practice, when
you're the only person using the thing and you're the one who keeps hitting the
walls, it's the model that has any chance of actually keeping up.
