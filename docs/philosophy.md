## Why Thrum Exists

AI coding agents are genuinely productive. A single agent can implement a
feature, write tests, and commit working code faster than most developers type.
Run several in parallel across worktrees and you can move through an entire
backlog in an afternoon.

Most multi-agent tools are solving a different problem. They want full autonomy
— you give the agents a goal, they figure out the plan, they ship code, you
review it later. That works for some people and some work.

Thrum is for when you want the speed of multiple agents but you still want to
understand what they built. You do the thinking. The agents do the typing. And
when you're ready, you can hand off the execution phase too — but the plan is
always yours.

## Three Approaches to Working with AI Agents

There are three ways people work with AI agents.

**Autonomous orchestration.** You describe a goal, the system breaks it into
tasks, assigns agents, and delivers results. You set objectives and review
outcomes. [Gastown](https://github.com/gastownhall/gastown) is excellent for
this.

**Human-directed work.** You do the research. You make the decisions. You write
the instructions. Agents execute your plan on separate branches. You review the
code, run the tests, and merge.

**Orchestrated execution.** You still do the research and write the plan — but
you hand the execution to an orchestrator agent. You tell it how you want the
work done: which worktrees, which runtimes, where the review gates are. It spins
up implementers, runs the work epic by epic, stops where you told it to stop,
and hands you a merge report. It never writes code. It never merges without your
say-so. You did the thinking. The orchestrator does the babysitting.

Thrum is for the second and third approaches. Thrum keeps you in control of the
plan. The difference between the two is just how much of the execution you want
to do yourself.

Most people start with human-directed work. Once you've done it a few times and
you trust the agents to follow your plans, the orchestrator saves you from
sitting at the terminal relaying "okay, start epic 2" all afternoon. That's why
Thrum has a separate orchestrator role — distinct from the coordinator you use
to research and plan. The coordinator helps you think. The orchestrator runs
what you've already decided.

## The Workflow

Here's what a typical day looks like when using Thrum with a tool like Beads for
issue tracking:

**1. Research.** You work with an agent to research a problem or feature. The
agent does all the boring heavy lifting — reading through the codebase, tracing
dependencies, understanding the current state of things — and comes back to you
with a proposed solution. You can chat about it, ask questions, and make changes
until you like it.

**2. Brainstorm.** Before you write any code or even a spec, you brainstorm with
your agent. You talk through the problem, explore approaches, ask questions,
poke holes. The agent pushes back, suggests alternatives, flags things you
haven't thought about. This is the [brainstorming skill](workflow-templates.md)
in action — it's designed to make sure you've actually thought through the
problem before committing to a solution.

**3. Plan.** Now you have an agreed-on approach, so you tell the agent to turn
it into a real plan. It investigates the codebase, finds all the dependencies,
figures out what will break, and writes a spec. Then it breaks the spec down
into idempotent steps — organized, parallelizable where possible. It uses the
Beads issue tracker to create Epics and Tasks, which are the full record of what
to do. Then the agent writes a prompt file you give to a different agent to
implement. It has all the details needed — the spec is referenced, the tasks are
laid out, it has directions on how you like it to execute (use sub-agents, make
sure test coverage is 80%, run all tests, code review when you're done, etc.).
This is the [writing-plans skill](workflow-templates.md). You can read the plan,
change it, or reject it — you see exactly what's going to happen before any code
gets written.

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

This cycle repeats. Research, brainstorm, plan, implement, review. You get the
speed of parallel agents with the confidence of understanding every change.

I've packaged all of these steps into a single
[project-setup skill](workflow-templates.md) — an opinionated flow that walks
you through brainstorming, spec writing, plan creation, task breakdown, and
worktree setup in one cohesive pipeline. It's the same workflow I use every day.
You don't have to use it, but if you want a structured starting point, it's
there.

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

Thrum doesn't plan your work — it makes planning your work easier and faster.
You do that with the help of your agents, and you can see what's going to happen
before it happens — not after the damage is done.

The orchestrator is opt-in. The messaging layer is the core. If you'd rather
stay hands-on and relay work yourself, Thrum works exactly the same without it.

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

If you want to direct the work yourself, understand what got built, and
gradually let the agents handle more as you trust them — that's what Thrum is
for.

## Next Steps

- [Quickstart Guide](quickstart.md) — get Thrum installed and running in 5
  minutes, with your first agent registered and sending messages
- [Orchestrator Role](orchestrator-role.md) — hand off a plan you wrote and let
  the orchestrator run the execution phase while you do other things
- [Agent Coordination](agent-coordination.md) — practical patterns for running
  multiple agents in parallel using the workflow described above
- [Workflow Templates](workflow-templates.md) — pre-built skill pipelines for
  the full research → plan → implement → review cycle
- [Beads and Thrum](beads-and-thrum.md) — how task tracking and messaging work
  together to give agents persistent memory across sessions
