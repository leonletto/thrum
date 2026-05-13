---
title: "Cross-Machine"
slug: "v0-5-cross-machine"
date: "2026-03-18"
author: "Leon Letto"
description: "v0.5.6 through v0.5.9. v0.5 had built a working messaging substrate on one machine. The second half of the line was about whether it survived contact with reality: a runtime registry that knew what kind of agent it was talking to, a hook that prevented a footgun that destroyed .git directories, and the March 18 evening I finally watched a message cross from my Mac Mini to my MacBook Pro over Tailscale."
tags: ["release", "v0.5", "tailscale", "multi-runtime"]
---

By March 14, the substrate from the first half of v0.5 was settled enough that I could start poking at the edges. The web UI worked. Identity was pinned to worktrees. Messages had read receipts. Role templates had been rewritten by what a 31-task session taught me. The system was solid in the sense that it stopped breaking under load.

It was not yet solid in the sense that it survived contact with reality. Five days later, v0.5.9 shipped, and the line had cleared three real-use papercuts (a footgun that destroyed `.git` directories, an "agent deleted" button that did not actually delete anything, a sync layer that worked on one machine and could not be relied on across two) plus the first time I watched a message cross from my Mac Mini to my MacBook Pro over Tailscale and arrive within seconds.

This is the second of two posts about v0.5. The first one covered v0.5.0 through v0.5.5 from the CHANGELOG, because the episodic-memory plugin that records my conversations with the coordinator was not running yet. This second post is mostly the same shape, with one exception: March 18 has a real archive of the Tailscale work, and that part of the story has Leon-quotes in it.

## What the System Could Tell About Itself

v0.5.6 shipped March 14. The headline addition was a three-tier agent detection registry. Until then, the daemon's only way to know what kind of runtime was running on the other end of a process tree was to hardcode the names it expected. Claude Code was the main one. As soon as I wanted to support Codex and Aider, that approach stopped scaling. The runtime registry that v0.5.6 introduced detected agents in three increasingly authoritative tiers: environment variables first (`CLAUDE_PROJECT_DIR` and equivalents), config files second (`~/.codex/config.toml`, `.cursor/`, etc.), binary verification third (does `which codex` resolve and is it executable). Each runtime got a registry entry with these three signals and a label. The registry was data-driven so adding a new runtime meant adding a registry entry, not editing twelve switch statements.

In the same release, `thrum init --skills` got added: a lightweight installation path that installs just the Thrum skill into an agent's plugin directory without running the full registration flow. Useful for multi-agent environments where an agent just needs the messaging commands and the rest of Thrum's identity model would be overkill. The skill content for that path got embedded into the binary, so the installation worked without a network round-trip.

Two smaller changes mattered more than they sounded. First, `thrum wait` started emitting structured action directives rather than raw message content. The stop hook also moved onto the directive format. Before this, the wait command's output was just the message text and the hook had to parse it heuristically; after, both spoke a single protocol. Second, the stop hook + listener heartbeat became a hybrid pair so that messages did not get lost in the gap between listener re-arms. The previous model had a window of a few seconds where an inbound message could arrive after the listener exited and before the next one was spawned. The hybrid closed it.

The CHANGELOG entry also notes a README rewrite to match the website voice and the removal of "git-backed" from the front-page identity language. I had been talking about Thrum as a git-backed messaging system, which is technically accurate but reads like marketing. The new framing positioned the CLI as primary and MCP as optional, which was a better description of how I actually used it.

## The Footgun That Destroyed `.git`

v0.5.7 shipped March 15. The release contains two fixes that together were as close as I came to a "do not do that ever again" patch.

The first was a small bug with a large failure mode. Clicking "delete agent" in the web UI was returning "Method not found." The `agent.delete` and `agent.cleanup` RPC handlers existed in the daemon but had never been registered on the WebSocket registry, so the UI's call resolved against nothing. The agent appeared deleted in the UI, sort of, and came back the next time anything queried the agent table. The fix was one registration call and a real cleanup pass: orphaned sessions, session child tables (refs, scopes), and the corresponding events in `events.jsonl` all got scrubbed, not just the agent row.

The second was the a-sync worktree protection. The sync layer maintains its own git worktree at `.git/thrum-sync/a-sync/`, separate from the user-facing worktrees. That detached worktree shares the underlying `.git` directory with the rest of the repo. Running `git checkout`, `git switch`, `git reset`, `git merge`, `git rebase`, or `git pull` inside that worktree, or running `cd` into it and then doing any of those, can destroy the `.git` directory. Not the worktree's local state. The repository's actual git database.

Somebody hit this. I do not need to know who or what triggered it to know somebody hit this, because the v0.5.7 fix is shaped like the response of someone who watched a `.git` directory go away in front of them. The fix is a PreToolUse hook (`block-sync-worktree-cd.sh`) that intercepts shell tool calls and refuses anything that would `cd` or `pushd` into the sync worktree, and refuses any `git -C` command targeted at it. Whatever was lost when someone discovered this was lost. After v0.5.7, it could not happen to anybody else.

The CHANGELOG entry's only comment on it is matter-of-fact: "Checking out a different branch there destroys the entire `.git` directory." That sentence is the shape of "we are not going to talk about how I learned this."

## Purge

v0.5.8 shipped March 17. One substantial feature, no story. `thrum purge` lets you remove messages, sessions, and events before a cutoff date. Relative durations (`2d`, `24h`), date-only formats (`2026-03-15`), full RFC 3339 timestamps. `--all` purges everything. `--confirm` is required to actually execute (a dry-run preview is the default). Agents, groups, and subscriptions are not touched. The implementation cleaned both the SQLite tables and the JSONL files. A `RemoveBeforeTimestamp()` helper in the JSONL package did the file rewriting.

The feature was straightforward. The purge command shipped, did what it said, and looked perfectly fine.

## The Mac Mini and the .env File

March 18. I had been waiting for this for a while. My Mac Mini and my MacBook Pro were finally on the same Tailscale network, and I was setting up cross-machine sync between them for the first time. v0.5.9 was open and I was running through the setup with the coordinator.

> "now I am setting up to test tailscale sync with my other computer as well. Here is what is working on the other computer: Tailscale sync is running: tailscale: enabled: true hostname: leonsmacmini connected_peers: 0 sync_status: idle"

That was the first time the cross-machine architecture was being tested on actual hardware rather than on a script that simulated two daemons on the same box. The Tailscale daemon was up on both ends. The peering had been established. The sync status said idle, which was correct because nothing had been sent yet.

Then I tried to actually run a sync. It did not work.

The agent dug into it. The `THRUM_TS_AUTHKEY` and other Tailscale env vars were sitting in a `.env` file at the repo root, the way the documentation suggested. The daemon was not reading them. The variables were not in the process environment because the daemon's start path didn't load the .env. I'd been exporting them manually every time I started the daemon and had not noticed that the documented path didn't actually work end-to-end. That gap was the kind of thing you only find by doing the setup as a real user would, not as someone who has the env vars cached in their shell.

The fix landed in v0.5.9 as `.env` auto-loading at daemon start. `THRUM_TS_*` and `TAILSCALE_*` variables now load from either repo root or `.thrum/.env` automatically. No more manual export. Same release also fixed an adjacent diagnostic gap: `thrum sync status --json` was not surfacing Tailscale state even when the env vars were loaded, which made the manual-export workaround harder to diagnose than it should have been.

Same v0.5.9 also dropped the periodic sync interval from five minutes to fifteen seconds, with a ten-second recent-message threshold. Combined with the push notifications that had landed earlier, cross-machine message delivery dropped from "eventually" to under twenty seconds. The scheduler also learned to run an initial sync at startup instead of waiting for the first tick, which removed the up-to-fifteen-second cold-start gap.

Two other Tailscale issues got cleaned up in v0.5.9. First, every RPC had a hardcoded ten-second context timeout, and `peer.wait_pairing` is a long-poll operation that runs for minutes. The pairing RPC was getting killed by the timeout immediately. Fix: a separate `RegisterLongPollHandler` with a six-minute timeout for pairing operations. Second, tsnet creates hostnames with a `-1` suffix that regular DNS cannot resolve, so `peer join` had been failing whenever a peer used the tsnet hostname instead of the underlying Tailscale IP. Fix: use the IP, not the hostname. This is the same hostname-versus-IP friction that came back as a Leon-quote in v0.6.0, where the same lesson got generalized: names for display, IPs for transport.

There was one other v0.5.9 fix that does not belong in the Tailscale section but matters. The `DefaultPreamble` had been showing the old "Wait for messages" one-liner instead of the new `Background Message Listener` section that contained the actual STEP_1 / STEP_2 spawn pattern. The background listener pattern had been upgraded weeks earlier and the preamble had silently fallen behind. Docs said one thing, code did another. The fix was to bring the preamble back in sync with what the listener pattern actually needed.

## What v0.5's Second Half Bought

The cost of v0.5.6 through v0.5.9 was a five-day stretch where the only piece that looks like a feature in the release notes (`thrum purge`) was the smallest piece of work. The big ones (the runtime registry, the a-sync hook, the .env auto-loading, the long-poll timeout fix, the IP-based peer addressing, the listener preamble correction) all read like maintenance.

The benefit was that the system survived contact with a second machine. Identity worked across worktrees. Messages had receipts. Templates encoded what the multi-agent stress test had taught. The runtime registry meant the system could talk to Codex and Aider as easily as Claude Code. The .git destruction footgun had a hook in front of it. And on the night of March 18, a message left my MacBook Pro and arrived on the Mac Mini in under twenty seconds, the way I had been describing the system for weeks before it could actually do it.

The heuristic from this stretch: a system that has not been used across two machines does not yet exist. It is a hypothesis. The .env loading gap, the hostname-versus-IP confusion, the long-poll timeout, the missing `THRUM_TS_*` plumbing in `sync status --json`: every one of those was a hypothesis the system had been carrying that broke the moment the second machine was real. The work that closed those gaps is what made the architecture into a system instead of a sketch.

That is the breather. The next session pivoted to the Tailscale peering redesign that became v0.6.0, which is a different story.