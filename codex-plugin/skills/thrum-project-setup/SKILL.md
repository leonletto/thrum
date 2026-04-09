---
name: thrum-project-setup
description:
  Convert a plan file into beads epics, tasks, prompts, and worktrees before
  implementation starts.
# source: claude-plugin/skills/project-setup/SKILL.md (adapted for codex naming)
---

# Thrum Project Setup

Convert a plan file into beads epics and tasks with detailed implementation
guidance and ready-to-use worktree planning.

This is the Codex-registered equivalent of Claude Code's `/thrum:project-setup`.

## When to use

- A design or plan exists and you need beads epics or tasks before coding.
- Work must be decomposed for multiple agents or worktrees.
- You need implementation-ready task descriptions, not a loose checklist.

## Prerequisite

Verify beads is installed before proceeding:

```bash
bd version
```

Do not continue without `bd`.

## Core flow

1. Read the plan file and the design doc it references.
2. Check the current beads state for related or overlapping work.
3. Decompose the plan into epics and tasks with clear dependencies.
4. Ensure each task description is a self-contained implementation guide.
5. Set up any worktree or branch guidance needed for execution.

## Inputs

- plan file path
- project root
- tech stack
- quality commands
- coverage target

If any are missing, ask the user directly before creating tasks.

## Output contract

- beads epics and tasks with dependencies
- task descriptions detailed enough for autonomous implementation
- worktree or branching guidance when relevant
- a short summary of what was created

## Resources

- `resources/implementation-agent.md`
- `resources/philosophy-template.md`
