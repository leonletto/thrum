# Sub-Agent Strategy

Delegate research, independent tasks, and verification to sub-agents. Your main
context should focus on task coordination, dependency management, and
cross-cutting decisions.

## Principles

1. **Parallelize independent tasks** — When multiple unblocked tasks touch
   different files/packages, implement them simultaneously via sub-agents
2. **Delegate research** — Spawn sub-agents to explore unfamiliar code before
   implementing, rather than reading it into your context
3. **Verify in background** — Run tests and lint via background sub-agents while
   you continue with the next task
4. **Focused prompts** — Give each sub-agent the full task description, worktree
   path, quality commands, and expected deliverables

## Agent Selection

| Task                        | Agent Type                  | Model  | Background? |
| --------------------------- | --------------------------- | ------ | ----------- |
| Implement a complex task    | `general-purpose`           | sonnet | no\*        |
| Implement a simple task     | `general-purpose`           | haiku  | no\*        |
| Explore unfamiliar code     | `Explore`                   | sonnet | yes         |
| Run tests / lint            | `general-purpose`           | haiku  | yes         |
| Review implementation       | `feature-dev:code-reviewer` | sonnet | no          |
| Doc updates / config tweaks | `general-purpose`           | haiku  | yes         |

\*Use foreground when you need the result before proceeding. Use background when
you can continue other work while they run.

## When to Parallelize vs. Work Sequentially

**Parallel** (sub-agents):

- Tasks touching different files/packages with no shared state
- Independent verification (tests, lint, coverage)
- Research into multiple unrelated code areas
- Doc/config updates alongside implementation

**Sequential** (direct work):

- Tasks modifying shared files or depending on prior task output
- Tasks requiring deep context from current session's changes
- Tasks needing judgment calls mid-implementation
- Single remaining task (no parallelism benefit)

## Verifier Sub-Agent Pattern

After completing a task, spawn a background sub-agent to independently verify:

- Run the test suite and confirm all pass
- Review implementation against the task description
- Check code follows project conventions
- Confirm acceptance criteria are met

This lets you start the next task immediately while verification runs in
parallel.

## Sub-Agent Prompt Guidelines

When writing prompts for sub-agents:

- Include the full task description with acceptance criteria
- Specify the absolute worktree path (sub-agents cannot resolve relative paths)
- Provide the exact quality commands to run (`make test`, `make lint`, etc.)
- State the expected deliverables explicitly
- Sub-agents cannot access MCP tools — they fall back to Bash
- Sub-agents cannot see CLAUDE.md or project `.agents/` files unless you include
  the content in the prompt
