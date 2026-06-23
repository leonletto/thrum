---
title: "28 Agents and a Release on the Side"
slug: "28-agents-and-a-release-on-the-side"
date: "2026-05-21"
author: "Leon Letto"
description:
  "v0.10.5 shipped while the team was heads-down on something else. One agent
  ran the whole release; close to 28 others were building the next version. The
  release turned out to be the boring part."
tags: ["release", "v0.10.5", "agents", "parallelism"]
draft: false
---

Shipping a release used to mean stopping. You freeze the work, you cut a branch,
you sit on it until the soak is done, and nothing important moves until the
version is out the door.

v0.10.5 didn't go that way.

It shipped while the team was heads-down on something else entirely. One agent
ran the whole thing — start to finish, eight candidates, four days — as a
background process, while somewhere close to 28 others were building v0.11. The
release was real work.

## What 28 Agents Looks Like

On the v0.11 side, the parallelism was the point. The main entrypoint — which
had bloated to an embarrassing size on a steady diet of "I'll clean that up
later" — got torn down by more than half, its command families extracted into
their own files. A SafeHandler migration ran across sixteen packages in
parallel. Separate tracks for the pane watcher, the agent-lifecycle work, the
context-restart engine, all moving at the same time.

And somewhere in the middle of all that, one track was building the very
substrate that let the others run. The tool holding up the work of improving the
tool.

It is not free. On a May evening I sent the coordinator a plain request:

> I see 33 agents running. Can you assess them and which ones are not needed
> anymore? my memory is under pressure.

![Activity Monitor showing a wall of agent processes and the memory-pressure readout near the ceiling.](/img/blog/v0105-memory-pressure.png)

That screenshot is what a normal day looks like on this machine: somewhere
between 20 and 28 agents running, some of them bigger than others. That evening
was different. I noticed the computer hesitating, checked, found 12 GB of swap,
and closed everything except VS Code before sending that message.

The coordinator ran `thrum tmux status`, found 28 actually alive (the rest were
zombie peers I'd miscounted), and sorted every one of them into buckets: work
complete and safe to tear down; paused and waiting; drifted from their last
snapshot and needing a look; essential and untouchable — including itself and
the release agent mid-merge. Six minutes later:

> Memory sweep complete: 28 to 16 alive (12 agents torn down).

The teardowns were non-destructive. Worktrees, branches, and task state all
persist. A torn-down agent comes back with a single command. The ceiling I hit
wasn't the coordination layer. It was the machine the coordination layer was
running on.

Side note: I need a new computer!

## One Agent, Eight Candidates

The release agent owned v0.10.5 from end to end. Eight candidates, rc.1 through
rc.8 over four days. Each bump meant the same loop: version the codebase, update
the what's-new and beta-channel callouts, run the soak, fix what came back.

It also handled the forward-merges — the unglamorous work that keeps a release
branch and the development branch from drifting into two different codebases.
Every fix that landed on the release line went into the development line the
same day. Four merge cycles in four days, each one a small reconciliation nobody
wants to do by hand.

I handed it the release and went back to watching the substrate work. That's the
part that still catches me off guard when I think about it. The release was real
work. It ran as a background process.

## The Load Was the Test

The release is modest on features, and it should be. The attention was
elsewhere. But the reason it took eight candidates to feel solid is the same
reason the substrate mattered: 28 agents hammering the same message bus all day
was the most thorough load test the system had ever had, and it kept finding
edges.

The bugs were the kind you never hit with three agents. A nudge echoing back to
the agent that sent it. A regression that only reappeared under volume. A
pane-engagement check mistaking a one-line acknowledgment for real activity. A
tmux capture breaking when output wrapped across lines. Messages sitting unread
because nothing reminded the agent they were there. A monitor leaving zombie
processes behind when it stopped. None of these are headline features. All of
them are the difference between a message bus you can trust at scale and one
you're quietly nervous about — and none of them would have surfaced if the team
had been three agents instead of 28.

The one deliberate change is the breaking one: `thrum send` now requires you to
name the recipient, either `--to @agent` or `--broadcast`. The old behavior let
a missing flag fall through to a default, which is the kind of convenient
shortcut that sends a message to the wrong place at the worst possible time.
After enough near-misses with it, making the recipient explicit was worth the
break.

Then the quieter additions: the headless worktree API, forward-ported from the
substrate work, and a uniform cross-worktree identity preflight — which is the
next section's story.

## What Held It Together

What made 28 agents survivable was the cross-worktree guard from
[the last post](shooting-yourself-in-both-feet.html). Each agent is pinned to
its own worktree by process identity, and any command run from the wrong
directory aborts instead of acting under the wrong name. At three agents, that's
a nicety. At 28, it's the only reason the message log means anything — because
every message is provably from who it says it's from, and the alternative fails
closed.

v0.10.5 extended that guard to fire uniformly across the full command surface.
That plus the forward-merges kept the release line and development line as one
codebase the whole time, so a fix never had to be written twice.

## The Boring Part

The interesting thing here isn't that 28 agents ran at once. It's that the
release stopped being the event.

A couple months ago, cutting a version was the whole day's work — the thing
everything else waited on. This time it was the piece I handed to one agent so I
could pay attention to something harder. I checked in occasionally. Mostly I
didn't.

The tool has gotten good enough to get out of the way. That's a strange thing to
be proud of — a release you barely noticed shipping. But getting out of the way
is most of what good infrastructure does, and the day the release became the
boring part was the day this started to feel finished.

## What's Coming?

I think v0.11 is going to be a much more interesting release because there are a
lot of features. It's probably going to have many more release candidates before
it's solid. But that'll be fun.

There's [a page about it here](https://thrum.team/docs/substrate/overview.html)
if you want to see what's coming.
