---
name: configure-roles
description: ">"
# source: claude-plugin/skills/configure-roles/SKILL.md
# generated-by: scripts/sync-skills.sh
---

## Configure Roles

Generate role-based preamble templates customized to your project environment.
Templates auto-apply when agents register via `thrum quickstart`.

**Announce:** "Using configure-roles to set up preamble templates for your
team."

### Interaction Style — MANDATORY

Use **`AskUserQuestion` for ALL interactive prompts.** Do not ask "reply with 1,
2, or 3" style questions in plain prose. Every choice the user makes goes
through the structured `AskUserQuestion` UI so answers are unambiguous and
machine-readable.

### Step 1: Detect Environment

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

- `.claude/settings.json` for MCP servers (context7, etc.)
- Existing rendered templates in `.thrum/role_templates/`
- Current saved answers in `.thrum/config.json` under `role_config` (if any)

To inspect a shipped reference template, use the CLI shim — never read directly
from a filesystem path, since the binary may run from any directory:

```bash
thrum roles templates print coordinator-autonomous
thrum roles templates print orchestrator             # single-variant role
```

### Step 2: Report Findings

Summarize what you detected:

- Runtimes installed
- Worktrees and branches in use
- Whether beads is configured
- Current team composition (agents, roles, modules)
- Available MCP servers and skills
- Existing role templates (if re-running)
- Existing `role_config` in `.thrum/config.json` (if re-running)

### Step 3: Check for Existing Configuration

```bash
ls .thrum/role_templates/ 2>/dev/null
thrum config show 2>/dev/null | jq '.role_config' 2>/dev/null
```

If `role_config` exists, prefill each AskUserQuestion with the saved value (see
"Re-run Behavior" below) so the user only has to confirm rather than re-enter
every choice.

### Step 4: Ask Questions

Ask these in sequence using `AskUserQuestion`. Each call should accept exactly
one answer and surface the available choices structurally.

#### 4a: Team Structure

Question: "What roles does your team need?"

Options based on detected agents, plus common defaults:

- coordinator, implementer (most common)
- coordinator, implementer, planner, researcher
- coordinator, implementer, reviewer, tester
- Custom set

Available roles (all have strict and autonomous variants except `orchestrator`,
which is single-variant):

| Role         | Purpose                                              |
| ------------ | ---------------------------------------------------- |
| coordinator  | Orchestrates team, dispatches tasks, reviews/merges  |
| implementer  | Writes code in assigned worktree                     |
| planner      | Creates plans, designs architecture, writes specs    |
| researcher   | Investigates codebases, produces research reports    |
| reviewer     | Reviews code for quality, security, correctness      |
| tester       | Writes and runs tests, verifies acceptance criteria  |
| deployer     | Handles builds, releases, deployment operations      |
| documenter   | Creates and maintains documentation                  |
| monitor      | Watches system health, reports anomalies             |
| orchestrator | Drives plan execution across agents (single-variant) |

#### 4b: Autonomy Level Per Role

For each role selected (except `orchestrator`), call `AskUserQuestion`: "What
autonomy level for the {role} role?"

- **Strict** — waits for coordinator instruction, limited scope
- **Autonomous** — can self-assign tasks, broader scope

#### 4c: Scope Rules

If multiple worktrees detected, ask via `AskUserQuestion`: "Should agents be
restricted to their own worktree?"

- **single_worktree** — strict scope boundaries
- **cross_worktree** — can read across worktrees

### Step 5: Generate Templates

For each role:

1. Read the shipped reference via the CLI shim:

   ```bash
   thrum roles templates print {role}-{autonomy}
   ```

   For `orchestrator`, omit the autonomy suffix (single-variant):

   ```bash
   thrum roles templates print orchestrator
   ```

2. Customize based on environment detection:
   - If beads detected: include `bd` commands in Task Tracking section
   - If MCP servers detected: add to Efficiency section
   - If specific skills detected: reference them
   - If worktree restrictions: adjust Scope Boundaries

3. Write to `.thrum/role_templates/{role}.md`. Keep per-agent template tokens
   (`{{.AgentName}}`, `{{.Module}}`, `{{.WorktreePath}}`,
   `{{.CoordinatorName}}`, `{{.RepoRoot}}`) **literal** — they get substituted
   per-agent at deploy time, not at template-write time.

### Step 5b: Persist Answers

After writing the templates, persist the user's answers so other code paths
respect their choices and drift detection can flag plugin upgrades:

```bash
cat <<'EOF' | thrum roles save-config
{
  "schema_version": 1,
  "roles": {
    "coordinator": {"autonomy": "{{coord_autonomy}}", "scope": "{{coord_scope}}"},
    "implementer": {"autonomy": "{{impl_autonomy}}", "scope": "{{impl_scope}}"}
  }
}
EOF
```

`thrum roles save-config` writes `role_config` into `.thrum/config.json`, fills
`schema_version` / `plugin_version` / `configured_at` with sensible defaults if
absent, and backfills `rendered_hash` per role from the current shipped body
hash so `thrum prime` shows no drift hints.

The save is atomic and preserves every other top-level config key
(backup/daemon/identity/telegram) byte-identical.

### Step 6: Offer Deploy

If agents are already registered, ask via `AskUserQuestion` whether to deploy
now:

```bash
thrum roles deploy --dry-run    # Preview
thrum roles deploy              # Apply to all agents
```

### Re-run Behavior

When `role_config` exists in `.thrum/config.json` (the canonical record), load
the saved answers and use them as the **prefilled values** for each
`AskUserQuestion` call so the user only has to re-confirm rather than re-enter
every choice:

1. Show existing roles with their saved autonomy and scope.
2. Ask via `AskUserQuestion` what to change: add a role, modify an existing
   role, or remove one.
3. Only regenerate requested templates.
4. Always re-run `thrum roles save-config` so `rendered_hash` is refreshed and
   the saved `plugin_version` / `configured_at` reflect the current run.
5. Offer deploy for changes.

If the user wants to apply only template-content updates after a plugin upgrade
(no answer changes), point them at `thrum roles refresh` — it regenerates
rendered files from saved answers without asking any questions.

### Environment-Specific Customizations

| Detected             | Template Customization                                   |
| -------------------- | -------------------------------------------------------- |
| Claude Code runtime  | Add Task tool + sub-agent guidance to Efficiency section |
| Augment runtime      | Add Augment-specific tool guidance to Efficiency section |
| Beads installed      | Add `bd` commands to Task Tracking, disable TodoWrite    |
| Thrum MCP server     | Add MCP tool references, CLI fallback for sub-agents     |
| Claude plugin skills | List installed skills with usage guidance                |
| Context7 MCP         | Add library docs guidance to Efficiency section          |
| Multiple worktrees   | Add worktree scope rules to Scope Boundaries             |
