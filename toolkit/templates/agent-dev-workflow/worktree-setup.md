# Worktree & Branch Setup Guide

## Purpose

Guide for selecting an existing worktree or creating a new one for development
work. Ensures beads is configured correctly so all worktrees share the same
issue database.

## Inputs Required

- `{{PROJECT_ROOT}}` — Absolute path to the main repository root
- `{{WORKTREE_BASE}}` — Base directory for worktrees (e.g.,
  `~/.workspaces/myproject`)
- `{{FEATURE_NAME}}` — Short name for the feature/branch (e.g., `auth`, `sync`,
  `ui`)

---

## Decision Flow

### Step 1: Check Existing Worktrees

```bash
git worktree list
```

Review the output. An existing worktree can be reused if:

- Its branch is not actively being worked on by another agent
- The branch topic is related to your work (or the branch can be repurposed)
- It has no uncommitted/stashed changes from prior work

Also check `bd list --status=in_progress` — if tasks reference a specific
worktree, that worktree may be occupied.

### Step 2: Decide — Reuse or Create

| Scenario                                            | Action                  |
| --------------------------------------------------- | ----------------------- |
| Existing worktree with matching branch, not in use  | Reuse it                |
| Existing worktree with unrelated branch, not in use | Create new branch in it |
| All worktrees are in active use                     | Create a new worktree   |
| Work needs isolation from all other branches        | Create a new worktree   |

### Step 3a: Reuse an Existing Worktree

```bash
cd {{WORKTREE_PATH}}

# Verify it's clean
git status

# If on wrong branch, create or switch
git checkout -b feature/{{FEATURE_NAME}}
# OR
git checkout feature/{{FEATURE_NAME}}

# Pull latest from main
git fetch origin main
git rebase origin/main

# Verify beads is working
bd where    # Should point to {{PROJECT_ROOT}}/.beads
bd ready    # Should show issues from the shared database
```

### Step 3b: Create a New Worktree

Use the setup script to create branch, worktree, and all redirects in one
command:

```bash
# From the main repository root
cd /Users/leon/dev/opensource/thrum

# Full bootstrap — branch, worktree, thrum + beads redirects, identity
./scripts/setup-worktree-thrum.sh \
  ~/.workspaces/thrum/{{FEATURE_NAME}} \
  feature/{{FEATURE_NAME}} \
  --identity impl-{{FEATURE_NAME}} \
  --role implementer
```

The script handles:

1. Branch creation (new) or reuse (existing)
2. Worktree creation at the specified path
3. Thrum redirect → shared daemon and messages
4. Beads redirect → shared issue database
5. `thrum quickstart` → identity and empty context file

**Flags reference:**

| Flag                | Default       | Purpose                                   |
| ------------------- | ------------- | ----------------------------------------- |
| `--identity <name>` | _(none)_      | Agent identity name (triggers quickstart) |
| `--role <role>`     | `implementer` | Agent role                                |
| `--base <branch>`   | `main`        | Base branch for new branch creation       |

Module is auto-derived from the branch name (`feature/auth` → `auth`).

**Without `--identity`**, the script only creates the worktree and redirects (no
quickstart, no identity/context files).

### Verify Setup

```bash
cd ~/.workspaces/thrum/{{FEATURE_NAME}}

# Verify beads points to shared database
bd where
# Expected: /Users/leon/dev/opensource/thrum/.beads

# Verify issues are visible
bd ready
bd list --status=open

# Verify identity and context files were created
ls -la .thrum/identities/
ls -la .thrum/context/

# Verify git state
git --no-pager log --oneline -3
git status
```

---

## Parallel Work Rules

When multiple agents share a worktree:

1. **Pull before starting** — `git pull --rebase`
2. **Commit frequently** — Small, focused commits reduce merge conflicts
3. **Document file ownership** — Each epic owns specific directories/files
4. **Clear commit messages** — Other agents read your commits to understand
   changes
5. **If conflict occurs** — Stop, pull, resolve, then continue

### File Ownership Table (Template)

When assigning parallel epics to the same worktree, define ownership:

| Epic A ({{EPIC_A_NAME}})  | Epic B ({{EPIC_B_NAME}})  |
| ------------------------- | ------------------------- |
| `internal/{{module_a}}/`  | `internal/{{module_b}}/`  |
| `internal/rpc/{{a}}_*.go` | `internal/rpc/{{b}}_*.go` |

**Shared files** (require coordination):

- Build files (`Makefile`, `package.json`)
- RPC routers or service registries
- Shared documentation

---

## Updating CLAUDE.md

After creating a new worktree, update the project's `CLAUDE.md` worktree table
so other agents know it exists:

```markdown
| Worktree         | Branch                     | Path                                 |
| ---------------- | -------------------------- | ------------------------------------ |
| {{FEATURE_NAME}} | `feature/{{FEATURE_NAME}}` | `{{WORKTREE_BASE}}/{{FEATURE_NAME}}` |
```

---

## Troubleshooting

### "not in a bd workspace"

The beads redirect file is missing or incorrect:

```bash
ls -la .beads/
cat .beads/redirect
bd where
```

### Wrong Database

```bash
bd where
# Should show {{PROJECT_ROOT}}/.beads, not a local database
```

### Sync Warnings in Worktrees

Warnings about "snapshot validation failed" or "git status failed" are
**normal** in worktrees. Check the final output — if it shows success, you're
fine.
