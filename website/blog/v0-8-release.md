---
title: "Toward an Orchestrator"
slug: "v0-8-release"
date: "2026-04-13"
author: "Leon Letto"
description: "v0.8 spans five days and three releases. The framing question on day one was whether two features were really the same plan. By day five Thrum had an orchestrator role, three runtimes launching through the same primitives, a refreshed identity model, and the three bugs the work uncovered along the way."
tags: ["release", "v0.8", "orchestrator", "multi-runtime"]
---

The day after v0.7.2 went out, I opened a session with the coordinator and asked a sequencing question. Two features had been sitting on the backlog: multi-runtime tmux (the work that would let OpenCode and Codex launch the same way Claude Code does), and a real orchestrator role with worktree management and review gates. I'd been thinking about both for a while. The question was which one to do first.

> "Can you analyze those two features to see which one we should implement first because I think they both are part of the same overall plan to make thrum more of an orchestrator?"

The coordinator made the case for multi-runtime first: it renamed `ClaudePID` to `AgentPID` (schema v17, a breaking change), and the orchestrator role would build on top of runtime-aware identity rather than re-derive it. I agreed.

> "yes. Lets do that."

Five days later, v0.8.2 shipped. The orchestrator role was real, three runtimes were launching through the same primitives, the daemon's view of "who is who" had been redesigned around a refresh primitive instead of an edge case, and three of the more interesting bugs we found weren't on any list when the week started.

## Inbox: 380 Unread

Same April 8 session. The coordinator dispatched the first multi-runtime task to a brand-new agent in a fresh worktree, and I pasted back what the agent saw the moment it ran prime.

> "Inbox: 380 unread (380 total) - process these before starting new work"

A new agent, with no history, seeing every group and broadcast message anyone had ever sent. The inbox filter was supposed to scope to the agent. It wasn't.

Two bugs underneath. In `internal/cli/prime.go`, the prime command never set `ForAgent` or `ForAgentRole` in its inbox query, so the unread count came back unscoped. And in `internal/daemon/rpc/message.go`, there was no time boundary on group and broadcast queries, so a new agent saw everything historical regardless of when its identity was registered. The fix needed both: pass the whoami fields through prime, and add a `registered_at` floor so a new agent only sees messages from its own existence forward.

> "create beads for both and then I think you can fix them here using sub-agents. And now that we have the thrum tmux commands working, you can even test it in a new work tree and then just remove the work tree when you're done."

That instruction is its own small story. The thing I was building with (tmux commands from v0.7.1) was the thing I used to test the fix for a bug in the thing I'm currently building. The new feature lets you spin up a test agent on demand, hit it with the failing scenario, and tear it down. Filed as thrum-8rj (P1) and thrum-2ue (P2). Both fixed the same session.

Same evening, while I was at it, I noticed `thrum tmux start` was sending `/thrum:prime` at T+8s and `HandleLaunch` was independently sending it at T+10s. Two prime commands from two different places. Duplicate removed. Commit: "fix(tmux): remove duplicate prime in start command."

## Stale, and Wrong-Prefixed

April 10. The multi-runtime work was reaching the point where I could actually launch a Codex agent through `thrum tmux start --runtime codex`. The first time I tried it, the prime command went into the pane and Codex stared at it.

> "It actually wants the dollar sign instead of the slash."

Codex uses `$thrum-prime`, not `/thrum:prime`. One `case "codex":` addition in `primeCommandForRuntime()`. Tested.

> "it worked perfect. commit."

But that fix exposed a much bigger problem. I had four worktrees up at the time: Claude Code in one, OpenCode in another, Codex in a third, the coordinator main in a fourth. I ran `thrum team`.

> "You'll notice that you are showing a stale and website dev is showing a stale and team-fix is showing a stale and multi-runtime is showing a stale. Also you can't tell which runtime they're on and we were supposed to be detecting that."

Every agent showed `[stale]`. None of them showed which runtime they were on. The daemon had no current view of the things it was supposed to be tracking.

My framing for the fix was important to me. I didn't want the answer to be "patch prime to refresh on the way in." I wanted the daemon's view of identity to converge automatically:

> "From what I understand from the few commits ago when we built this new feature, the daemon is supposed to keep track of who's who and what they're running and what PID they are, that type of thing... I guess we can start there and build a shared function that does the work. That way if we want to add that function to other thrum commands, which are run more often, so that they also double check and update the identity file if needed, we can do that later. Any fix should be not just an edge case in thrum prime. Does this make sense?"

That turned into a six-section design session. The coordinator walked through the architectural decisions as a series of questions; I gave terse approvals. Where does the refresh run (every `getClient()` call, so every CLI command participates). How does it detect drift (compare in-process state to identity file mtime and content). How does it trigger re-registration (it doesn't on its own; the next quickstart or tmux-start does, but the team listing is now always live). How does the daemon's `team.list` build its picture (scan all worktree identity files instead of relying on cached rows).

One addition from me that mattered:

> "Don't forget to use safeCmds for the os level stuff so we don't introduce locking bugs."

The `process.go` `ps` calls were using bare `exec.Command`. That's the pattern that bit us in v0.6 and again later. Flagging it inline meant the safecmd migration covered it from the start instead of getting added as a 0.8.1 patch.

The result of that conversation is what shipped as `RefreshLocalIdentity` in v0.8.0, alongside the multi-runtime work and the new orchestrator role with `thrum worktree create/teardown/list` and the `thrum:orchestrate` skill. And alongside the tmux command queue, the daemon's per-session FIFO that finally gives commands a reliable place to land instead of racing the pane state. And alongside the OpenCode and Codex plugins, with the Codex plugin pulled into alignment with the Claude Code source-of-truth via `sync-skills.sh`. And alongside the safecmd migration, 47 call sites moved off bare `exec.Command` across 11 files.

v0.8.0 went out the evening of April 10.

## And Then the Same-Day Patch

About an hour after the tag pushed, the release workflow failed on `npm publish`. The cause was almost embarrassing. `opencode-plugin/package-lock.json` was in `.gitignore`, because some earlier pattern had matched it. The release workflow runs `npm ci`, which requires a lockfile. The lockfile was on disk but not in git. CI checked out a tree without it. `npm ci` refused.

The fix was a one-line negation pattern in `.gitignore` to un-ignore that specific lockfile. v0.8.1 went out the same day. The thing about same-day patches is they don't really feel like releases. They feel like getting the tagging right.

## Housekeeping, the Breather, and What Came After

The next two days were quieter. I dispatched a CLI audit to clean up things that had accumulated as the surface area grew. Remove groups as a user-facing concept (about 2400 lines of code gone across 24 files). Restrict `--to` to agent IDs and `@everyone`. Fix a latent bug where `@everyone` was leaking across git-synced repos because of how scope matching had been written.

Then April 13 morning. I came back to a daemon running v0.8.1 and a list of 22 remote branches in the repo, most of which were stale from earlier feature work that had been cherry-picked into the trunk. I spent the first hour cleaning. 18 branches deleted. Bidirectional merge between `website-dev` and `thrum-dev` to reconcile some divergent doc edits. A `git filter-repo` run to purge `dev-docs/` out of git history entirely (about 9.5MB, 28 commits), because the dev-docs were supposed to be gitignored and somehow had sneaked into history during an earlier merge. Then a pre-commit guard to make sure that didn't happen again.

At 10:15 AM I sent the coordinator the sentence that, in retrospect, was the breather moment of the line.

> "Excellent. Now we can get back to work. Can you look at the previous couple of commits in this branch and the backlog and beads to see what we need to do before we can do the next release?"

The housekeeping had been the actual reset. That sentence was the return to feature work. The backlog scan came back with two issues that needed to go out before the next tag.

## The Monitor That Never Delivered

The first one was thrum-taa, a P0. The monitor feature, which had shipped in v0.8.0 three days earlier, never actually delivered messages. The matcher fired. The matching agent was identified. The delivery call happened. Nothing landed.

Root cause was elegant and embarrassing in equal measure. `HandleStart` for a monitor never registered a synthetic agent and session row for the `monitor:<name>` sender identity. `HandleSend` requires both rows to exist before it will deliver. The delivery closure in the monitor code silently swallowed the resulting error and returned. So every monitor match looked successful at the matcher layer and silently dropped at the delivery layer.

The integration test masked the bug by pre-seeding the rows manually. That's the cliché of every "broken from day one" bug: a test that helpfully sets up the world for the code under test, and never asks whether the code under test is supposed to set up the world itself.

Fix: an `ensureMonitorSender()` call in `HandleStart`, `HandleRestart`, and an `EnsureAllMonitorSenders()` pass at daemon startup so monitors that survive a restart get their rows back. Twelve minutes from dispatch to working fix.

The second one was thrum-ltj, a P2 I'd flagged a day earlier. `SyncLoop.Start()` was calling `EnsureSyncBranch` but never `CreateSyncWorktree`. When the sync directory existed without a valid worktree, `git status` failed every 30 seconds for the life of the daemon. Fixed by adding the idempotent `CreateSyncWorktree` call before the loop starts.

> "Excellent. Can you fix both of them here using subagents?"

Both fixes back in twelve minutes. Committed, pushed.

Then one more thing for the release.

> "Now I have one more thing to add to the release, which is the cursor plugin."

The Cursor Agent plugin. Five hooks, two rules, four skills, eleven commands, MCP config, a `local-install.sh` for deployment. Pulled into alignment with the Claude Code plugin source of truth via `sync-skills.sh`, the same primitive that synced OpenCode and Codex. Four runtimes through one syncing script. Tagged as v0.8.2 that afternoon.

## What Five Days Cost and Bought

The cost was a line that didn't stop. v0.8.0 on April 10 with the big drop. v0.8.1 the same day for a CI gitignore mistake. v0.8.2 three days later with the P0 monitor fix, the sync worktree fix, the CLI audit, and the Cursor plugin. Plus the housekeeping morning that purged 28 commits of history and 18 stale branches before any of v0.8.2's actual content got written.

The benefit was that Thrum stopped being a Claude Code feature with a daemon attached and became an agent orchestrator with four supported runtimes. The orchestrator role meant I could hand a multi-epic plan to a coordinator and watch it dispatched to a fleet of agents in their own worktrees without me being the conductor. The refresh primitive meant `thrum team` actually showed live state without anyone running prime first. The tmux command queue meant a command sent to an agent landed in a known order and a known channel instead of racing the pane. And the inbox finally only showed each agent its own messages.

The heuristic from this line: the test you didn't write is the bug you have. Monitor delivery looked working for three days because the test set up its own world. The 380-message flood looked working until a brand-new agent ran prime. Stale identities looked working until four runtimes were up at once. Each one of those was a feature exercising a scenario its own tests hadn't simulated. The fix in every case was small. The lesson was the same.