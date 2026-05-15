---
title: "What Agents Remember"
slug: "v0-9-2-release"
date: "2026-04-29"
author: "Leon Letto"
description:
  "v0.9.2 was supposed to be one feature: a way for agents in a new repo to
  inherit what agents in my other repos had already figured out. By the end of
  the week the release also included a bug fix to a feature that ate its own
  configuration, a months-old UX papercut nobody had filed, and the first post
  on the blog you are reading right now."
tags: ["release", "v0.9.2", "role-skills", "blog"]
---

v0.9.2 was supposed to be one feature. The role-skills layer: a way for agents
in a brand-new repo to inherit the things agents in my other repos had already
figured out the hard way. By the end of the week the release also included a fix
for a feature that ate its own configuration, a months-old UX papercut I'd been
blaming on myself, several days of release-test plumbing, and the first post on
the blog you are reading right now. Only the role-skills layer was on the plan
when April 24 started.

The week ran from v0.9.1 (April 24) through v0.9.2 (April 29).

## The Brainstorm

Every time I started Thrum in a new repo, the agents on it began from zero. They
didn't know any of the things the agents on my other repos had already learned.
The coordinator on a fresh project would re-discover the same review patterns,
re-derive the same dispatch discipline, hit the same dead ends. Every fresh
install paid a learning tax.

The obvious answer was to put all the rules into the role preamble that always
loads. I'd been doing that, more or less, with CLAUDE.md. The problem was that
most of the rules in the giant block didn't apply to most of the work the agent
was doing in any given session. Loading them all at the top of every
conversation was burning context for nothing.

I sat down with the coordinator on the morning of April 24 to think it through.
The conversation was unusually clean. I made four decisions in a row and they
all stuck.

First, the researcher should be a long-lived agent that other agents send to,
not a discipline that every agent practices. Specialization, not duplication.

Second, the skills shouldn't all live in the preamble. They should live in the
plugin, each with a description that says when it's useful, and the agent pulls
them in only when the situation matches. The preamble itself stays small. I told
the agent:

> "I think the main preamble needs to be expanded because even now it's very
> small compared to the size of the project state or the memory files that are
> loaded. I don't think there's a problem with making the preamble bigger."

So the preamble grew but not unboundedly. It holds identity, message protocol,
and status discipline. Everything else is invokable.

Third, the skills should sync across all the runtimes I support, not just Claude
Code. The conversion is lossy in places, especially on runtimes whose plugin
systems are younger than Claude Code's, but the syncing primitive should exist:

> "Codex now has a plugin ecosystem so sync-skills.sh will have to mature into a
> bigger script to convert everything properly to the other systems as they
> mature."

Fourth, the researcher's memory shouldn't live in one giant file. It should be a
thin index pointing at `bd memories research-*` entries, so other agents can
query the same store. That decision had a nice side effect:

> "We could even ship specialized thrum modules later in this way since they
> could be installed with a simple script to add the beads memories."

The shape that landed in v0.9.2: ten new skills (three for the coordinator, four
for the implementer, three for the researcher), six revised role preambles, and
a sync into the OpenCode and Codex plugin trees. The shipped skills are the
universal floor. Project-specific corrections live in beads under a
`<role>-rule-*` namespace and accumulate in-session whenever I tell an agent to
stop doing something. The two layers compose: universal rules from the plugin,
local rules from your repo.

The names of the skills aren't the point. The point was to stop bloating every
conversation with knowledge it didn't need yet.

## The Bug That Introduced Itself

The next day I was on my other machine doing live validation. I ran
`/thrum:configure-roles`, picked my autonomy and scope settings, watched the
templates render, and then ran `thrum context preamble --init` to write the
preamble out.

The preamble it wrote was the generic default. The configuration I'd just done
was gone.

I messaged the coordinator:

> "I found one bug. If I run `thrum context preamble --init` it writes the
> version without the new roles rather than the ones I just configured with
> /thrum:configure-roles. I think there needs to be a way for thrum to know that
> I ran that and what my choices were so that the other thrum commands don't
> revert my changes."

The implementer found the cause inside an hour. There were two code paths that
wrote the preamble. The path used by `thrum roles deploy` called
`RenderRoleTemplate`, which knew about the configuration. The path used by
`--init` called `RoleAwarePreamble(role)`, the hardcoded fallback. The two had
been written months apart and nobody had ever run them in sequence, because
nobody had ever needed to. The new `configure-roles` flow was the first time
both got exercised in the same session, and the second one silently undid the
first.

Patching the call site was the easy part. The harder question came one beat
later, when I asked the agent where my configure-roles answers were actually
being stored:

> "Where is the decisions I made configured and stored? Is it in
> .thrum/config.json?"

The answer was: nowhere. The skill rendered the templates and that was it.
Nothing on disk had any structured record of which choices I'd made. So `--init`
couldn't have respected them even if the call site had been right; there was
nothing for it to respect. The bug wasn't in the call site. It was that the
configuration was a process, not a thing.

I told the agent where the answer should live:

> "I think any configuration like this should live here .thrum/config.json not
> in a new file."

That decision is what most of v0.9.2's CLI surface comes from. A `role_config`
key in `.thrum/config.json` holds the answers. `thrum roles refresh` re-renders
templates when the config changes. A drift hint in `thrum prime` fires when the
config and the rendered templates disagree. And the role templates themselves
are now embedded in the binary, so they don't break when the filesystem path
moves.

So: building the role-skills layer was a brainstorm. Making it survive contact
with a real machine was a separate week of work, and the bug that triggered it
took ten seconds to hit.

## The Papercut I'd Been Quietly Tolerating

A day or two later, the coordinator was telling me about a bug in `make ci`.
Tests were failing when there were live tmux sessions on the machine, because
something wasn't isolating itself properly. I read the description and asked
offhand:

> "Is that why when I run thrum tmux connect I see sessions from other repos?"

There was a pause. Yes, it was. The session-listing code scanned every tmux
session on the machine that carried the `@thrum-managed=1` tag, and the tag was
set by every Thrum daemon, not scoped to the daemon that set it. The test
failure and the picker bug were the same bug. There was even a comment in the
code that said "filter out unrelated sessions on the same tmux socket," and the
filter underneath the comment didn't actually do that.

The fix added a second tmux option, `@thrum-thrum-dir`, that records which Thrum
project a session belongs to. The picker filters by that. Sessions that predate
the change get gracefully skipped. The implementer turned it around in seven
minutes.

This is the kind of bug that had been mildly annoying me for a while, and I'd
never bothered to file it because I assumed I was holding it wrong. Hearing the
test failure described made it click. It wasn't user error. It was actually
wrong. Months of low-grade friction resolved by saying it out loud.

## The Plumbing That Took the Longest

The work that nobody is going to ask about absorbed most of the week, which is
how it usually goes. The release-test framework took about ten merges over five
days: porting scenarios that used to live in a manual test plan into a bash
harness that runs them automatically. MCP routing, plugin slash commands,
cross-session messaging, context and compaction, restart snapshots, worktree
setup, multi-runtime, orchestrator, monitor jobs, snapshot CLI. Each one trivial
in isolation. Together, a release-readiness story that no longer depends on me
remembering to run the manual plan.

There were also a handful of SessionStart hook fixes. The identity banner wasn't
always firing. The pane-side banner that goes up when you start or restart a
session inside tmux had timing issues. The hook payload was getting truncated in
some runtimes because nothing was watching its size. None of these are flashy.
They're the sort of things that, when broken, make the agent feel slightly off
in a way you can't quite point at, and then one day you fix them and the off
feeling is gone and you stop noticing.

I don't have a clean narrative for this part of the release. I just want to say
it took longer than any of the named features did.

## And Then the Release Happened, and Then This Blog Happened

I cut the v0.9.2 tag on the morning of April 29. The release came up clean.
GoReleaser pushed binaries for darwin, linux, windows. The Homebrew tap updated.
The GitHub release notes formatted properly. While I was watching that go
through, I opened the docs site and noticed the Overview page was leading with a
long "what's new in v0.9.2" block that didn't belong there.

I sent a Telegram message to the coordinator to fix it. The first fix it came
back with was a new `whats-new.html` page. While I was looking at that, I had a
second thought:

> "I have a vision of a releases page which is really a blog page if you
> understand my meaning. Each release gets its own page that reads like a blog
> post talking about why it was built, why the changes were made, and what the
> benefits are. This way the expectations for the blog posts are not really that
> they're going to be super bloggy. And it also allows us to go back and use the
> session history of the Claude agents like you during the planning session.
> That can be the basis for the release posts about why it was built."

That's what this is. The whats-new page got deleted in the same session that
wrote the first post.

## What a Week Cost and Bought

The cost was that one planned feature became four shipped things, and the
original feature took twice the time to land because half of it was discovered
the moment a real user ran the first command. The role-skills layer is the
headliner of v0.9.2, but the part of v0.9.2 that makes the layer actually work,
the persistent `role_config` in `.thrum/config.json`, the refresh subcommand,
the drift hint in prime, came entirely from a ten-second test on a different
machine.

The benefit was that the release is traceable end to end to one design session
at the start of the week, plus one bug from trying to use what got built, plus
one offhand question that fixed a thing I'd been quietly tolerating, plus a
website pivot that produced this blog. The big planned work was the role-skills
layer. Most of what makes v0.9.2 useful is the stuff that came from running into
things while building it.

The heuristic from this line: build configurations as things, not processes. A
process leaves no record of what was decided. A thing can be respected,
refreshed, audited, asked about. `configure-roles` ate its own config because
the answers existed only in the moment the skill ran, and that turned out not to
count as a configuration.
