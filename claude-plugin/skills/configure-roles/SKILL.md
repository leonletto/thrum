---
name: configure-roles
description: >
  Detect environment and generate customized role-based preamble templates for
  your Thrum agents. Creates .thrum/role_templates/ files that auto-apply on
  agent registration.
allowed-tools:
  "Bash(thrum:*), Bash(bd:*), Bash(git:worktree*), Read, Write, Glob"
---

# Configure Roles

Generate role-based preamble templates customized to your project environment.
Templates auto-apply when agents register via `thrum quickstart`.

**Announce:** "Using configure-roles to set up preamble templates for your
team."

## Step 1: Detect Environment

Run these commands and collect the output (suppress errors):

```bash
thrum runtime list 2>/dev/null          # Installed runtimes
git worktree list 2>/dev/null           # Worktrees and branches
bd stats 2>/dev/null                    # Beads task tracker state
thrum agent list --context 2>/dev/null  # Registered agents
ls .claude/skills/ 2>/dev/null          # Installed Claude skills
thrum config show 2>/dev/null           # Thrum configuration
```

Also check:

- `.claude/settings.json` for MCP servers (context7, auggie-mcp, etc.)
- `toolkit/templates/roles/` for shipped example templates

## Step 2: Report Findings

Summarize what you detected:

- Runtimes installed
- Worktrees and branches in use
- Whether beads is configured
- Current team composition (agents, roles, modules)
- Available MCP servers and skills
- Existing role templates (if re-running)

## Step 3: Check for Existing Templates

```bash
ls .thrum/role_templates/ 2>/dev/null
```

If templates exist, show them and ask what to change. Do NOT start from scratch.

## Step 4: Ask Questions

Ask these in sequence using AskUserQuestion:

### 4a: Team Structure

"What roles does your team need?"

Options based on detected agents, plus common defaults:

- coordinator, implementer (most common)
- coordinator, implementer, planner
- coordinator, implementer, researcher
- Custom set

### 4b: Autonomy Level Per Role

For each role selected, ask: "What autonomy level for the {role} role?"

- **Strict** — waits for coordinator instruction, limited scope
- **Autonomous** — can self-assign tasks, broader scope

### 4c: Scope Rules (optional)

If multiple worktrees detected: "Should agents be restricted to their own
worktree?"

- Yes (strict scope boundaries)
- No (can read across worktrees)

## Step 5: Generate Templates

For each role:

1. Read the shipped example from `toolkit/templates/roles/{role}-{autonomy}.md`
2. Customize based on environment detection:
   - If beads detected: include `bd` commands in Task Tracking section
   - If MCP servers detected: add to Efficiency section
   - If specific skills detected: reference them
   - If worktree restrictions: adjust Scope Boundaries
3. Write to `.thrum/role_templates/{role}.md`

## Step 6: Offer Deploy

If agents are already registered:

```bash
thrum roles deploy --dry-run    # Preview
thrum roles deploy              # Apply to all agents
```

## Re-run Behavior

When `.thrum/role_templates/` already has files:

1. Show existing templates with a summary of each
2. Ask what to change (add role, modify existing, remove)
3. Only regenerate requested templates
4. Offer deploy for changes

## Environment-Specific Customizations

| Detected             | Template Customization                                   |
| -------------------- | -------------------------------------------------------- |
| Claude Code runtime  | Add Task tool + sub-agent guidance to Efficiency section |
| Augment runtime      | Add auggie-mcp codebase-retrieval to Efficiency section  |
| Beads installed      | Add `bd` commands to Task Tracking, disable TodoWrite    |
| Thrum MCP server     | Add MCP tool references, CLI fallback for sub-agents     |
| Claude plugin skills | List installed skills with usage guidance                |
| Context7 MCP         | Add library docs guidance to Efficiency section          |
| Multiple worktrees   | Add worktree scope rules to Scope Boundaries             |
