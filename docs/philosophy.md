
## Why Thrum Exists

AI coding agents are genuinely productive. A single agent can implement a
feature, write tests, and commit working code faster than most developers type.
Run several in parallel across worktrees and you can move through an entire
backlog in an afternoon.

Most multi-agent tools are trying to solve a different problem than Thrum. They
want full autonomy — you give the agents a goal, they figure out the plan, they
ship code, you review it later. That's fine for some work. It's not what I built
Thrum for.

Thrum is for when you want the speed of multiple agents but you still want to
understand what they built. You do the thinking. The agents do the typing.

## Two Approaches to Working with AI Agents

There are two ways people work with AI agents.

**Autonomous orchestration.** You describe a goal, the system breaks it into
tasks, assigns agents, and delivers results. You set objectives and review
outcomes.

**Human-directed work.** You do the research. You make the decisions. You write
the instructions. Agents execute your plan on separate branches. You review the
code, run the tests, and merge.

Thrum is for the second approach. It doesn't assign tasks or plan work. It gives
agents a way to message each other across worktrees and machines, so you can run
several in parallel without being the message relay yourself.

## The Workflow

Here's what a typical day looks like when using Thrum with a tool like Beads for
issue tracking:

**1. Research.** You work with an agent to research a problem or feature. The
agent does all the boring heavy lifting — reading through the codebase, tracing
dependencies, understanding the current state of things — and comes back to you
with a proposed solution. You can chat about it, ask questions, and make changes
until you like it.

**2. Plan.** You ask the agent to investigate the codebase, docs, or whatever
needs changing. It does the boring detailed slog of finding all the
dependencies, figuring out what will break, and adding that to the plan.
Rewriting the docs to fit the new code, etc. Then it gives you a spec that you
can read and understand and approve or change as you see fit.

**3. Document.** Now you have an agreed-on plan so you tell the agent to break
it down into idempotent steps, optimized for making it very organized and
parallelizable where possible. Then it uses the Beads issue tracker to create
Epics and Tasks which are the full record of what to do. Then the agent writes a
prompt file which you give to a different agent to implement. It has all the
details needed — the spec is referenced, the Epics and Tasks are talked about,
it has directions on how you like it to execute (use sub-agents, make sure test
coverage is 80%, run all tests, code review when you are done, etc.). Now you
can read this if you like, or don't trust the agent yet, and you can see exactly
what is going to happen.

**4. Implement.** You hand that prompt to an agent on a worktree. It claims
tasks, writes code, runs tests, commits. Thrum lets you see what it's doing
(`thrum team`, `thrum who-has`). If you're running multiple agents on different
features, Thrum lets them message each other and stay coordinated without you
relaying information manually.

**5. Review and merge.** Now the code is written and the tests pass and the work
is isolated in a worktree on a branch. You ask your coordinator agent — probably
the one you worked with to research and write the spec and the prompt — to do a
code review against the spec and deal with any findings. It tells you what it
found and usually asks if you want it to fix them. When you are satisfied, you
tell it to merge and you are done.

This cycle repeats. Research, plan, document, implement, review. You get the
speed of parallel agents with the confidence of understanding every change.

The prompts you write are documentation. The issues you create are your audit
trail. The git history shows exactly what happened. Nothing is hidden.

## What Makes This Feel Different

When you work this way, it still feels like you wrote the code. The agent was
faster than you typing, but the decisions were yours. Six months later when
something breaks, you remember why it works this way — because you approved the
plan before the code got written.

## Inspectable by Design

No magic. Everything in Thrum is just files you can look at.

- **Messages** are JSONL files on a Git branch. They're just text — `cat` them,
  `grep` them, pipe them through `jq`.
- **Agent identity** is a JSON file in `.thrum/identities/`. Open it with any
  text editor.
- **Sync** is Git push and pull. Run `git log` on the `a-sync` branch to see
  exactly what synced and when.
- **State** is a SQLite database rebuilt from the JSONL source of truth. Query
  it directly, or delete it — it rebuilds.

There's no cloud service, no opaque API. If something goes wrong, you look at
files.

## What Thrum Is Not

Thrum doesn't plan your work - it makes planning your work easier and faster.

It won't break everything down and make all the decisions for you unless you
tell it to. You do that with the help of your agents and you can see what is
going to happen before it happens - not after the damage is done.

Thrum doesn't orchestrate your agents. It gives agents a way to message each
other across worktrees and machines, so you can run several in parallel without
being the message relay yourself.

Thrum doesn't stop agents or interrupt their work. If you need to stop an agent,
you stop the process. Thrum provides the communication layer — you provide the
control.

It's not a framework either. Any agent that can run shell commands or use MCP
tools can use Thrum. There's no SDK to integrate, no protocol to implement
beyond basic messaging.

And it's not trying to replace you. You're the one who understands the codebase.
Thrum just makes it practical to direct multiple agents at once. And the process
keeps you in the loop as much as you want. Transparent and auditable. You are in
control.

## For Working Developers

I built Thrum for myself — someone who ships production code and needs to
understand the codebase they're working in. Not for AI researchers building
novel agent architectures. Not for platform teams building orchestration
systems.

If you want agents to autonomously tackle your backlog while you do something
else, there are good tools for that. If you want to direct the work yourself and
have agents execute faster than you can type, that's what Thrum is for.
