---
title: "Why Thrum Exists"
slug: "why-thrum-exists"
date: "2026-02-12"
author: "Leon Letto"
description:
  "I started Thrum because I was tired of being the integration layer between my
  own agents. This is the story of what that actually looked like."
tags: ["origin", "intro"]
---

If you've ever tried to run more than two AI coding agents at once, you already
know the shape of the problem. Terminals everywhere, none of them quite
remembering what the others are doing, and you in the middle with a coffee,
copy-pasting status updates between panes like a confused operator at a 1960s
telephone switchboard.

This is the first post on the Thrum blog. I figured it should be about why Thrum
exists at all, because if you're new here that's the only question that matters.
The [philosophy page](../docs/philosophy.html) has the structured answer. This
post is the unstructured one, the version that sounds like I'm telling it to you
across a kitchen table.

## The Switchboard

Picture four worktrees of the same repo. One agent is refactoring the auth
middleware. Another is writing the tests that the first one's changes will
break. A third is upgrading a dependency that touches both. A fourth is
supposedly cleaning up a legacy file but is actually frozen on a permission
prompt I can't see because the pane is two screens away.

This is faster than doing the work alone. By a lot. That's what makes it
tempting to keep doing it. But the speed is constantly leaking out through me,
because every decision one agent makes that the others need to know about goes
through my hands. I read the message in pane 1, switch to pane 3, summarize, hit
enter. Forty seconds. I do that fifty times a day and I haven't actually built
anything. I've just been a bus.

The agents weren't the bottleneck. I was. And the worst part wasn't the time I
spent shuttling messages around. It was the times I didn't, the decisions I
forgot to relay, the conflict I only noticed at merge time because nobody told
nobody.

## The Realization

The thing the agents needed wasn't more autonomy. They didn't need to be smarter
or more independent. They needed a place to put messages where the other agents
would see them, and where the messages would survive a context compaction or a
session restart.

That's it. That was the whole insight. Most of what I'd been doing manually was
just message routing. Agents already know how to write text. They already know
how to read it. They just had nowhere to put it that the other agents could
find.

The first thing I tried was the most obvious thing: agents writing into shared
files in a folder, other agents reading them. That fell apart almost
immediately, and not for the reason I expected. The reading was fine. The
problem was everything around the reading: managing the files, expiring old
messages so the folder didn't sprawl, and keeping any single agent from blowing
its context window the moment it tried to ingest the pile. There was no clean
answer to "what's new since I last looked" without writing a small index, and
once you're writing an index you're halfway to building the thing on purpose.

I was also starting to use [Beads](https://github.com/leonletto/beads) for issue
tracking during the same stretch of experimentation. At the time, Beads stored
issues as JSONL: one file, append-only, easy to `cat`. I liked that a lot. It
was the first thing in my agent toolchain that felt simple in a way I trusted.
When I sat down to design a proper messaging system, that pattern was sitting
right there.

The other constraint that decided the rest of the architecture was that I wanted
this to work the way I work: in Git, on whatever machine I happen to be on, no
cloud account, no online service to keep in contact with. If my laptop is
offline on a flight, the agents on it should still be able to message each other
and have the messages flush to the rest of the team when the connection comes
back. That ruled out anything that wasn't fundamentally local-first.

So Thrum is what fell out of those two ideas combined. Messages are append-only
JSONL on a Git branch. Sync is `git push` and `git pull`. The state you query is
a SQLite database that gets rebuilt from the JSONL. You can delete it any time
and it comes back. There's no service to host. No account to make. If something
looks wrong you `cat` a file.

## What This Means in Practice

I'm not going to walk through the whole workflow here. The
[quickstart](../docs/quickstart.html) does that better, and the philosophy page
covers how it fits into a research-plan-implement-review loop. The shorter
version of what changed for me is this: I stopped being the bus.

The agents tell each other when they start work. They tell each other what
they're touching, so two of them don't accidentally rewrite the same file. They
tell each other when they're stuck, and the others can pick up the thread. When
one of them restarts and loses its in-context memory, it reads the messages it
missed and knows where things are. I check in via `thrum team` or the web UI,
ask questions where I want to, and otherwise stay out of it.

The plan is still mine. The decisions are still mine. I read the code before it
merges. None of that changed. What changed is the forty seconds, fifty times a
day.

## What the Blog Is For

Thrum has gotten big enough that it's hard to take in all at once. The reference
docs cover the surface area, but they don't tell you which features exist for
which problems, or what it actually feels like to use them, or why a particular
decision is the way it is.

That's what this blog is for. Going forward I'll write one feature at a time:
what it does, the problem it solves, how to use it, and where the rough edges
still are. Some posts will have short videos when watching is easier than
reading. Most won't. I'll also write about the design decisions that don't
belong in reference docs but are worth saying out loud: what I considered, what
I rejected, what I'd do differently now.

If you're brand new, [install it](../docs/quickstart.html) and send a message
between two agents. That takes about five minutes and tells you more than I can
in a post.

More soon.
