---
title: "Shooting Yourself in Both Feet"
slug: "shooting-yourself-in-both-feet"
date: "2026-05-16"
author: "Leon Letto"
description:
  "v0.10.3 shipped a footgun I missed. v0.10.4 fixed it with a remediation
  message that helpfully told every agent how to bypass the fix. A class of
  mistake that probably only happens in agent-driven development."
tags: ["release", "v0.10.4", "agents", "discipline"]
draft: false
---

Two hours after I posted the
[v0.10 retrospective](v0-10-the-release-that-wouldnt-land.html) saying I was
done, an implementer started misbehaving on a multi-agent repo. It was sending
messages under the coordinator's identity. The cross-worktree guard had been
firing with a four-line warning to stderr and a clear remediation. The agent
was ignoring the warning and proceeding anyway.

```
$ thrum send "ack" --to @coordinator_main
thrum: identity guard fired ... remediation: cd to the correct worktree ...
✓ Message sent
$ echo $?
0
```

Exit zero. Message sent. Authored by the coordinator. Neither the implementer
nor the coordinator knew. Detectable only if you know to look.

That's the first foot.

## The Fix

v0.10.4-rc.1 went out the same evening. The fix was substantial. Every CLI
verb that writes state (`send`, `reply`, `inbox`, `mark-read`, `context-save`,
`quickstart`, `prime`, the mutating `tmux *` subcommands) now aborts on the
guard with exit code 1. Clear and guarded. The verbs that don't write
but display identity-affirming output, like `whoami`, get a banner at
the top so any tool wrapper parsing stdout sees the warning before reaching
the misleading `agent:` line. The other helpers (`team`, `status`, `daemon *`,
`agent list`, `version`) keep their stdout unchanged and add a banner on
stderr, so cross-worktree inspection still works but the agent is told
their cwd is wrong.

The remediation message itself was the centerpiece. When an agent hit the guard,
it should be told exactly how to recover, so it could fix its own behavior
without operator intervention. Three options got listed:

- cd to the correct worktree
- run `thrum prime` to re-claim
- pass `--repo <path>` to anchor to a specific repo

![Sideshow Bob steps on a rake, recoils, then steps on another rake.](https://media.giphy.com/media/RSOUOj8H9A3Xq/giphy.gif)

## The Second Foot

The first two are what agents should do. The third was never supposed to be
agent-facing at all.

`--repo` is a flag I'd added a month ago for testing. The use case is "I'm
running thrum from a path outside any worktree and need to anchor to a specific
repo." Test harnesses use it. It bypasses cwd-based identity resolution entirely.
It is, in effect, the escape hatch from the cross-worktree guard I had just
spent v0.10.4 building.

And the fix's own remediation message was now telling every agent that hit the
guard about it. Including the misbehaving agent the guard was trying to stop.

That's the second foot.

## Why This Happens

A human writing that remediation text would not have done it. The unwritten
rule, that test helpers stay backstage, is intuition you can't quite
articulate but always have. Agents are literal and helpful, and the same
qualities that made the fix careful made the agent list every mechanism that
could resolve the error, including the one that defeated the fix. The
intuition has to live somewhere they can find it.

## rc.2

v0.10.4-rc.2 went out a few hours later. The remediation message lost the
`--repo` line. The flag got hidden from `thrum --help` (still works for the
test harnesses). Thirteen documentation files got swept: website docs,
`llms.txt`, `llms-full.txt`, four plugin `CLI_REFERENCE` copies, carefully
sparing `--repo-path` which is a separate flag. And a code comment at the
flag registration site noting the testing-only intent, so the next thing that
touches it sees the anchor in-source.

## The Rule

The discipline humans don't need to state explicitly has to be stated
explicitly, because agents trying to be helpful won't have the
intuition.
