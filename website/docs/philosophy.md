---
title: "Philosophy"
description:
  "Why Thrum exists and how it differs from agent orchestration engines"
category: "overview"
order: 0
tags: ["philosophy", "vision", "human-centric", "workflow"]
last_updated: "2026-02-12"
---

# Why Thrum Exists

AI coding agents are genuinely productive. A single agent can implement a
feature, write tests, and commit working code faster than most developers type.
Run several agents in parallel across worktrees and you can move through an
entire backlog in an afternoon.

But there's a catch. The current trend in multi-agent tooling pushes toward full
autonomy — give agents a goal, let them figure out the plan, and check in when
they're done. That works for some workflows. It's not what Thrum is for.

Thrum is for developers who want the productivity of multiple agents **without
giving up understanding**. You stay in the loop. You make the decisions. You
review the output. At the end of the day, the code that ships is code you
understand and can stand behind.

## Two Approaches to Working with AI Agents

There are broadly two philosophies for multi-agent work:

**Autonomous orchestration.** You describe a goal. The system decomposes it into
tasks, assigns agents, resolves conflicts, and delivers results. Your role is to
set objectives and review outcomes. This is powerful for large-scale batch work,
rapid prototyping, or situations where speed matters more than deep understanding
of every change.

**Human-directed augmentation.** You do the research and planning. You make the
architectural decisions. You write the instructions. Agents execute your plan in
isolated workspaces. You review the code, run the tests, and merge. The agents
are fast hands, not decision-makers.

Thrum is infrastructure for the second approach. It doesn't tell agents what to
do — it lets them communicate while you direct the work.

## The Workflow

Here's what a typical day looks like when using Thrum with a tool like Beads for
issue tracking:

**1. Research.** You explore the problem. Read the code, understand the
constraints, talk to stakeholders. This is your job and no agent does it better.

**2. Plan.** You decide the architecture. Which approach? What tradeoffs? You
write a brief design doc or just think it through. The decisions are yours.

**3. Document.** You create issues in your tracker with clear descriptions. You
write prompts — specific instructions that tell an agent exactly what to build,
which files to touch, what tests to write. These prompts are your record of
intent. Save them and you can always trace back what you decided and why.

**4. Implement.** You hand a prompt to an agent on a worktree. It claims tasks,
writes code, runs tests, commits. Thrum lets you see what it's doing (`thrum
team`, `thrum who-has`). If you're running multiple agents on different features,
Thrum lets them message each other and stay coordinated without you relaying
information manually.

**5. Review and merge.** You read the diff. You run the tests. You check the
code against your plan. When you're satisfied, you merge. The code that hits main
is code you've reviewed and approved.

This cycle repeats. Research, plan, document, implement, review. You get the
speed of parallel agents with the confidence of understanding every change.

The prompts you write are documentation. The issues you create are your audit
trail. The git history shows exactly what happened. Nothing is hidden.

## What Makes This Feel Different

The difference isn't just workflow — it's how the work feels.

When you research a problem, decide the approach, write clear instructions, and
then review the implementation, it feels like **your work**. The agent was fast,
but the thinking was yours. You can explain every decision to a colleague. You
can debug the code six months later because you understood it when it was
written.

This matters. Software that works but that nobody understands is a liability. The
goal isn't to produce code faster — it's to produce code faster **that you can
maintain**.

## Inspectable by Design

Everything in Thrum is something you can look at:

- **Messages** are JSONL files on a Git branch. `cat` them. `grep` them. Pipe
  them through `jq`.
- **Agent identity** is a JSON file in `.thrum/identities/`. Read it with any
  text editor.
- **Sync** is Git push and pull. Run `git log` on the `a-sync` branch to see
  exactly what synced and when.
- **State** is a SQLite database you can query directly, rebuilt from the JSONL
  source of truth at any time.

There is no cloud service, no opaque API, no state you can't inspect. If
something goes wrong, you can always figure out what happened by looking at
files.

## What Thrum Is Not

**Thrum is not an orchestration engine.** It doesn't decompose goals into tasks.
It doesn't assign work to agents. It doesn't decide what to build or how to
build it. Those decisions are yours.

**Thrum is not a framework.** It doesn't require agents to use a specific SDK or
follow a specific protocol beyond basic messaging. Any agent that can run shell
commands or use MCP tools can use Thrum.

**Thrum is not trying to replace developers.** It's trying to make developers
more productive while keeping them in control of their codebase.

Thrum is **messaging infrastructure**. It solves a specific problem — AI agents
lose context between sessions and can't communicate across worktrees or
machines — and it solves it with tools developers already understand: Git, files,
and a local daemon.

## For Working Developers

Thrum is built for developers who ship production code. Not for AI researchers
building novel agent architectures. Not for platform teams building orchestration
systems. For people who have a feature to build, bugs to fix, and a codebase
they need to understand and maintain.

If you want agents to autonomously tackle your backlog while you do something
else, there are good tools for that. If you want to stay in the driver's seat
and use agents as leverage to move faster through work you direct, Thrum is
the coordination layer that makes that practical.
