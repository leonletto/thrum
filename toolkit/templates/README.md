# Workflow Templates for AI Agent Development

This directory contains templates for the three-phase workflow used to plan and implement features with AI agents.

## What These Are

These templates encode a proven workflow for agent-driven development:

1. **PLAN** - Brainstorm, design, and decompose work into tasks
2. **PREPARE** - Set up an isolated git worktree with shared issue tracking
3. **IMPLEMENT** - Execute tasks autonomously, with resume support after context loss

The workflow uses **Beads** for task tracking and **Git worktrees** for isolated branches.

## Files

### `CLAUDE.md`
Overview document explaining the three-phase process, how to fill in placeholders, and the source-of-truth hierarchy. Start here.

### `planning-agent.md`
Phase 1 template. Give this to a planning agent to:
- Explore the codebase and clarify requirements
- Propose architectural approaches
- Write a design spec
- Create Beads epics with detailed task descriptions

### `worktree-setup.md`
Phase 2 guide. Use this to:
- Choose or create a git worktree for isolated development
- Configure Beads redirect so all worktrees share the same issue database
- Verify the setup before handing off to implementation

### `implementation-agent.md`
Phase 3 template. Give this to an implementation agent to:
- Orient from Beads status and git history (works for both fresh starts and resumes)
- Work through tasks in dependency order
- Run quality gates and merge to main
- Handle blockers and coordinate with other agents

## Quick Start

1. **Fill in placeholders:** All templates use `{{PLACEHOLDER}}` syntax. Replace these with your project-specific values before using.

2. **Plan the work:**
   ```bash
   # Give planning-agent.md to your planning agent
   # It will create epics and tasks in Beads
   ```

3. **Set up a worktree:**
   ```bash
   # Follow worktree-setup.md to create/select a workspace
   git worktree add ~/.workspaces/myproject/feature -b feature/auth
   ```

4. **Implement:**
   ```bash
   # Give implementation-agent.md to your implementation agent
   # It will work through tasks autonomously
   ```

5. **Resume after context loss:**
   ```bash
   # Restart the implementation agent with the same prompt
   # The "Orient" phase recovers state from Beads and git
   # No work is duplicated
   ```

## Key Concepts

### Beads as Source of Truth

Task descriptions in Beads contain:
- File paths to create/modify
- Function signatures or code examples
- Acceptance criteria
- Verification commands

The planning agent front-loads detail so implementing agents can work autonomously without conversation history.

### Worktree + Beads Redirect

All worktrees MUST share a single Beads database via redirect files:

```bash
# In each worktree
echo "/absolute/path/to/main/.beads" > .beads/redirect
```

This ensures all agents see the same tasks, regardless of which worktree they're in.

### Resume-Friendly Implementation

The implementation template is designed to recover from context loss:
1. Agent hits context limit or session ends
2. Restart with the same filled-in template
3. "Orient" phase reads Beads status and git commits
4. Agent picks up from the first incomplete task

Completed work is never redone because it's tracked in Beads and committed to git.

## Customization

### Adjust Detail Level

- **Backend/API work:** Include function signatures and type definitions in task descriptions
- **UI/CSS work:** Include full code examples and visual verification steps
- **Integration work:** Specify connection points and data flow

### Add Project Conventions

- Testing requirements (e.g., "every public function needs a test")
- Code style (e.g., "use JSDoc for all exports")
- Commit message format (e.g., "type(scope): description")

### Integrate with Thrum

If using Thrum for agent messaging, add registration steps:

```bash
# At start of implementation
thrum quickstart --name {{AGENT_NAME}} --role implementer --intent "Working on {{EPIC_ID}}"

# During work
thrum send "Progress update: completed task X" --to @coordinator

# At completion
thrum send "Completed {{EPIC_ID}}, ready for review" --to @coordinator
```

## Learn More

- See `../agents/` for Beads and Thrum agent integration guides
- See the Beads project repository for task tracking documentation
- See the Thrum project repository for multi-agent coordination documentation
