---
title: "The Release That Wouldn't Land"
slug: "v0-10-the-release-that-wouldnt-land"
date: "2026-05-15"
author: "Leon Letto"
description:
  "v0.10 was supposed to be the big one. It took four versions to actually ship,
  and on the way it forced the first real release-candidate process. That part
  turned out to matter more than the feature."
tags: ["release", "v0.10", "codex", "testing"]
draft: false
---

The plan for v0.10 was a fresh-install experience that didn't feel like reading
a man page. A real `thrum init` wizard, opinionated defaults for roles and
worktrees, and a new role template that pinned implementer agents to their own
worktree so they couldn't drive-by edit the main repo. That was the headline. It
shipped on May 3 as v0.10.0.

It came back as v0.10.1 the same day.

## What 0.10.0 Did

The wizard works. Pre-fill any prompt with `--name`, `--role`, `--module`,
`--worktrees-root`, `--roles=enhanced|default|skip`, `--no-daemon`, and you can
script the whole thing end-to-end. Worktrees moved from
`~/.workspaces/<project>` to `~/.thrum/worktrees/<project>` so the project owns
its own corner of the home directory instead of squatting in the namespace I
happen to use.

That part of 0.10.0 is still what 0.10.3 ships. The wizard didn't get rewritten.
Everything around it did.

## Same-Day Patch

The wizard goes through a code path called `buildInlineQuickstartCmd`, which
means daemon-spawned panes now run `thrum quickstart` far more often than they
used to. Those panes inherit the daemon's environment. When you start the daemon
from a primed shell, its environment contains `THRUM_HOME`, a pointer to
whatever worktree you were standing in when you ran `thrum daemon start`.

The result: a brand-new worktree would run `thrum quickstart`, and instead of
writing its identity into its own `.thrum/`, it would write it into
`$THRUM_HOME/.thrum/`. The parent worktree's identity. New agent overwrites old.
Latent bug since March, surfaced because Epic-D moved more traffic through the
affected path.

The fix in 0.10.1 had three parts: exempt `init` and `quickstart` from the
`THRUM_HOME` substitution that other commands rely on, plumb an explicit
`--repo` into the daemon-inline quickstart so it doesn't have to consult the
environ, and stop a few load-side paths from re-applying the substitution to an
already-resolved repo path. Two new release-test scenarios codified the
regression.

Same release also added boot-time identity reconciliation. After a daemon
restart, write RPCs (`thrum send`, `thrum tmux start`) from a registered
worktree could fail with `anonymous caller cannot invoke X`. The peercred
resolver looks up agents by joining `session_refs` against `sessions`, both of
which are local-only durable state that can drift away from the identity files
on disk. v0.10.1 walks `.thrum/identities/*.json` at boot and replays the rows.

## Next Day, Different Env Leak

v0.10.2 came out on May 4. Most of it was variations on the same theme that bit
0.10.1: environment variables leaking through processes that should not have
been talking to each other.

The big one: tmux panes spawned by the daemon were inheriting `THRUM_*` env vars
from the daemon's environ. A pane's `thrum whoami` would resolve to the
daemon-starter's identity instead of the pane's intended agent. The fix scrubs
`THRUM_*` at one chokepoint inside `safecmd` so every tmux exec path benefits at
once.

Then a sibling bug: even with the daemon-side scrub in place, long-running tmux
servers still leaked. Tmux session env is sourced from the _server's_ environ at
server-start time, not from the client connection at session-create time. So
after the daemon scrub, sessions created against a long-running tmux server were
still inheriting whatever environ the server captured weeks or months ago. Fix:
every `tmux new-session` now sets per-session `-e KEY=` overrides to neutralize
whatever the server cached.

There was also a quieter bug that had been losing me disk space for a while:
`thrum purge --confirm` was passing the wrong field name to its JSONL filter
(`created_at` vs. the actual `timestamp`), so every record passed the date check
and nothing got pruned. Verified live: a `--before 30d` purge against my dev
box's 335MB sync dir actually freed space for the first time in months.

## What 0.10.3 Was Supposed To Be

By the time v0.10.2 went out, 0.10.3 had already accumulated its own scope.
Codex as a first-class citizen: full plugin parity with Claude Code, a
`SessionStart` hook that auto-primes, a one-command install script, the 14
role-discipline skills synced over. And the release-test harness: a tmux-based
test framework that runs full multi-agent scenarios end-to-end, the kind of
testing that used to live in my head as "do the manual test plan again."

This was a real release. It needed real testing.

So for the first time, I cut a release candidate instead of a tag. Anyone who
wanted the beta could get it from the [beta channel](../docs/beta-channel.html):
one curl one-liner, pinned to `vX.Y.Z-rc.N`. The whole point of the beta channel
was to slow down, run real workflows against the candidate, and find the things
`make ci` doesn't.

It worked. The RC chain ran from rc.1 through rc.6.

## What the RC Phase Caught

rc.1 shipped the new tmux silence watchdog, the thing that nudges an agent if it
doesn't engage with the prime briefing after launch. It never fired. Not once.
The watchdog compared two pane snapshots taken 30 seconds apart and bailed if
they differed. Claude Code's animated thinking spinner makes consecutive
snapshots never byte-equal, so the watchdog always saw "engaged" and always
bailed. The rewrite uses a two-anchor semantic check: find the banner sentinel
at the top, find the runtime's chrome divider at the bottom, ignore the spinner
pattern between them.

rc.2 fixed that, then surfaced a tip-line false positive. Claude renders
contextual tip lines between the spinner and the divider, exactly the band the
rc.2 algorithm was inspecting. rc.3 moved the bottom anchor to the spinner
itself.

rc.3 also exposed that `waitForPaneReady`, the function that decides when a
fresh pane is ready to receive a keystroke, was using the same broken
byte-equality pattern the watchdog rewrite had just replaced. On Claude Code it
would either declare ready prematurely (banner went into the input box but the
following Enter was swallowed) or hit its 60-second ceiling. rc.4 rebuilt it
around the same silence-driven pattern: poll `tmux #{window_activity}` until 5
seconds of silence, settle for 2 seconds, then declare ready.

rc.4 surfaced one more in the same family: even with a fully-rendered pane,
modern TUI runtimes treat a long string immediately followed by Enter as "Enter
inside paste" and swallow the submission. A new helper inserts a 200ms gap
between text and Enter so the input widget exits paste mode first.

rc.5 was the big one. On macOS, every peer-credential lookup the daemon had ever
done had been silently failing. `gopsutil.Process.Cwd()` is documented as "not
implemented yet" on Darwin and returns an error on every call. That error wasn't
recognized as anonymous-caller, so the daemon fell through to the legacy "trust
whatever agent_id the CLI sends" path. The CLI built that claim from
`THRUM_AGENT_ID` env vars when set. That's the same failure mode the env-scrub
work in 0.10.1 and 0.10.2 had been trying to close, which is why this bug had
looked so much like the others. It wasn't the same bug. The env-leak fixes in
0.10.1 and 0.10.2 were correct on their own merits, and they still ship. The
peercred resolver had been doing quiet damage in parallel the whole time, and
some of the "agent is misidentified" symptoms I'd been carrying into rc.4 were
downstream of this, not of the env-leak thread I'd been blaming. Replaced the
gopsutil delegation with an `lsof -p PID -Fn -d cwd` subprocess. Slow path, but
reliable. A unit test exercises the real path against the test process's own PID
so the regression can't recur silently.

rc.6 closed the CLI half: even with the daemon-side fix, the CLI was still
consulting env vars first when deciding which daemon socket to dial. Stale env
from a parent shell anchored elsewhere bypassed the daemon's correct identity
resolution entirely. The CLI now walks up from the supplied path looking for a
`.thrum/` ancestor; `THRUM_HOME` is a fallback for the legitimate "pin to a
worktree from outside any worktree" case. The same restructuring applies to
`THRUM_AGENT_ID` and to identity-file lookup.

And one small one I'm fond of: the beta-channel install snippet on the docs page
itself had `VERSION=` on the wrong side of the shell pipe.
`VERSION=vX.Y.Z-rc.N curl ... | sh` sets the env var on `curl`, which sh never
sees, so the installer fell back to `latest`. Real-world hit during rc.1 soak:
the documented command installed v0.10.2 instead of v0.10.3-rc.1. Snippets now
read `curl ... | VERSION=vX.Y.Z-rc.N sh`. The kind of bug you can only find by
actually using the thing.

## What This Cost, And Bought

Three patch releases over two days for what was supposed to be one feature drop.
Six release candidates over the following week before any of it was something
I'd give to another human.

In return: the macOS peercred resolver, which had been broken for as long as
Thrum has had a peercred resolver, is finally fixed. The watchdog actually
watches. The keystroke-submission path actually submits. The env-scrub story is
complete from daemon environ to tmux server environ to CLI fallback chain. And
the release-test harness now exists and ran against six candidates in a row.

The thing I'll keep from this is the RC cycle. v0.10.3 is the first Thrum
release that went through a real beta phase, and the reason that mattered is
that everything caught in rc.1 through rc.6 would otherwise have been v0.10.4
through v0.10.9. Bug reports from real users. Footguns documented after they
fired. Instead they're release notes on a candidate nobody depended on. I'm
going to do this for every minor release from here.

If you want to follow along, the [beta channel docs](../docs/beta-channel.html)
describe how to install the current RC. The stable release ships when the soak
is done.

## What Came After

When I posted this, the chain stopped at rc.6. Five more candidates went out
over the next few days, each one closing one small bug the previous candidate
had surfaced under real use. An init flag the wizard would clobber on upgrade. A
`message read --all` race where messages arriving between listing and reading
got swept up and silently marked read. A spinner regex that didn't recognize
Claude 2.1.141's twisting glyph. None of them were dramatic on their own. Each
one was the kind of thing that turns into "thrum is flaky" if you ship it
without catching it first.

I'm done now. We'll be releasing in the next day or so.
