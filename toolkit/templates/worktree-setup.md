# Worktree & Branch Setup Guide

> **Note:** This is a distributable template. Fill in the `{{PLACEHOLDER}}` values with your project-specific information before using.

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

```bash
# From the main repository root
cd {{PROJECT_ROOT}}

# Create the worktree with a new branch
git worktree add {{WORKTREE_BASE}}/{{FEATURE_NAME}} \
  -b feature/{{FEATURE_NAME}}
```

---

## Beads Redirect Setup (Required for New Worktrees)

Every new worktree MUST have beads configured to share the main issue database.
Without this, `bd` commands will fail or use an isolated database.

### Option 1: Use Setup Script (if available)

```bash
./scripts/setup-worktree-beads.sh {{WORKTREE_BASE}}/{{FEATURE_NAME}}
```

### Option 2: Manual Setup

```bash
# Create .beads directory in the worktree
mkdir -p {{WORKTREE_BASE}}/{{FEATURE_NAME}}/.beads

# Create redirect file with ABSOLUTE path to main .beads
echo "{{PROJECT_ROOT}}/.beads" > \
  {{WORKTREE_BASE}}/{{FEATURE_NAME}}/.beads/redirect
```

**Always use absolute paths.** Relative paths break if the worktree is outside
the main repo tree.

### Verify Setup

```bash
cd {{WORKTREE_BASE}}/{{FEATURE_NAME}}

# Verify beads points to shared database
bd where
# Expected output should reference: {{PROJECT_ROOT}}/.beads

# Verify issues are visible
bd ready
bd list --status=open

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
