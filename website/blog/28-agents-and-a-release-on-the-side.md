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

Shipping a release used to mean stopping. You freeze the work, cut a branch, fix
whatever the soak turns up, and nobody touches anything important until the
version is out the door. v0.10.5 did not go that way. It shipped while the team
was heads-down on something else entirely.

The something else was v0.11, the personal-agent substrate, and at the peak
close to 28 agents were working on it at once. The release ran off to the side,
handled start to finish by a single dedicated agent while the rest of the
machine kept building the next thing.

## What 28 Agents Looks Like

On the v0.11 side, the parallelism was the point. The SafeHandler migration ran
across sixteen branches at once, `m1` through `m16`, one per package. The
`main.go` teardown took the file from 10,736 lines down to 4,414, a 59 percent
cut, by extracting twenty command families into their own files. Separate tracks
for the pane watcher, the agent-lifecycle work, the context-restart engine, all
moving at the same time. One of those tracks was the substrate architect itself,
building the very thing that let the rest of them run at all: the tool holding
up the work of improving the tool.

It is not free. On May 19 at 9:37 in the evening I sent the coordinator a plain
request:

> I see 33 agents running. Can you assess them and which ones are not needed
> anymore? my memory is under pressure.

![Activity Monitor showing a wall of agent processes and the memory-pressure readout near the ceiling.](/img/blog/v0105-memory-pressure.png)

The screenshot shows what I normally have running: somewhere between 20 and 28
agents, some of them bigger than others. That evening I noticed my computer
hesitating, and when I checked I had 12 GB of swap. I panicked, killed
everything except VS Code, and sent that message to the coordinator.

The coordinator ran `thrum tmux status`, found 28 actually alive (the other
handful were zombie peers I had miscounted), and sorted every one of them into
buckets: ten safe to tear down, work complete; four paused; five that had
drifted from the last snapshot and needed a look; eight to keep, including
itself and the release agent mid-merge. It tore down twelve, caught two cases
where the snapshot was stale, verified the release agent's in-flight work along
the way, and came back six minutes later:

> Memory sweep complete: 28 to 16 alive (12 agents torn down).

The teardowns were non-destructive. Worktrees, branches, and task state all
persist; a torn-down agent comes back with a single `thrum tmux launch`. The
ceiling I hit at 28 agents was not the coordination layer. It was the machine
the coordination layer was running on.

Side note: I need a new computer!

## One Agent, Eight Candidates

The release agent was called `v0105-parallel`, and it owned v0.10.5 from end to
end. Eight release candidates, rc.1 on May 17 through rc.8 on May 21, four days.
Each bump meant the same loop: version the codebase, swap the what's-new and
beta-channel callouts to the new rc, run the soak, fix what came back.

It also ran the forward-merges, the unglamorous work that keeps a release branch
and a development branch from drifting into two different codebases. Every fix
that landed on the release line went into the development line the same day: the
tmux capture fix, the breaking send change, a paneAgentEngaged correction, a
six-pick cherry batch. Four merge cycles in four days, each one a small
reconciliation nobody wants to do by hand.

I gave it the release and went back to watching the substrate. That is the part
that still surprises me. The release was real work, eight candidates and a
breaking change, and it ran as a background process.

## The Load Was the Test

The release is modest on features, and it should be. The attention was
elsewhere. But the reason it took eight candidates to feel solid is the same
reason the substrate mattered: 28 agents hammering the same message bus all day
was the most thorough load test the system had ever had, and it kept finding
edges.

The bugs it surfaced were the small, concurrency-shaped kind you never hit with
three agents. A nudge that echoed back to the agent that sent it, a regression
of an earlier fix that only reappeared under volume. Pane-engagement detection
that mistook a one-line acknowledgment for real activity. A tmux capture that
broke when an agent's output wrapped across lines. Messages sitting unread
because nothing reminded the agent they were there, which is what the new
daemon-side backstop nudger fixes. A monitor that left zombie processes behind
when it stopped. None of these are headline features. All of them are the
difference between a message bus you can trust at scale and one you cannot, and
none of them would have shown up if the team had been three agents instead
of 28.

The one deliberate change is breaking: `thrum send` now requires an explicit
recipient, either `--to @agent` or `--broadcast`. The old behavior let a missing
flag fall through to a default, which is the kind of convenience that sends a
message to the wrong place at the worst possible time. After enough near-misses,
making the recipient explicit was worth the break.

Then the quieter additions: the headless worktree API, forward-ported from the
substrate work, and a uniform cross-worktree identity preflight, which is the
next section's story.

## What Held It Together

What made 28 agents survivable was the cross-worktree guard, the fix from the
last post. Each agent is pinned to its own worktree by process identity, and any
command run from the wrong directory aborts instead of acting under the wrong
name. At three agents that is a nicety. At 28 it is the only reason the message
log means anything: every message is provably from who it says it is from,
because the alternative fails closed.

v0.10.5 extended that guard to fire uniformly across the command surface, not
just the obvious mutating verbs. The forward-merges were the other half: the
release line and the development line stayed one codebase the whole time, so a
fix never had to be written twice.

## The Boring Part

The interesting thing here is not that 28 agents ran at once. It is that the
release stopped being the event. A couple months ago, cutting a version was the
whole day's work, the thing everything else waited on. This time it was the
piece I handed to one agent so I could pay attention to something harder.

The tool has gotten good enough to get out of the way. That is a strange thing
to be proud of, a release you barely noticed shipping. But getting out of the
way is most of what good infrastructure does, and the day the release became the
boring part was the day this started to feel finished.

## What's Coming?

I think v0.11 is going to be a much more interesting release because there are a
lot of features. It's probably going to have many more release candidates before
it's solid. But that'll be fun.

There's [a page about it here](https://thrum.team/docs/substrate/overview.html)
if you want to see what's coming.
