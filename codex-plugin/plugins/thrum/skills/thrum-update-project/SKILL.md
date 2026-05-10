---
name: thrum-update-project
description: Update project state with session summary and mechanical state
# source: claude-plugin/commands/update-project.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Update Project

Use this skill when the user explicitly wants the `update-project` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


Update `.thrum/context/project_state.md` with fresh project state so a new
session can pick up exactly where this one left off.

**This skill delegates to a subagent to avoid consuming main context.**

## Step 1: Compose Your Session Summary

From YOUR context (what you already know), write a brief summary covering:

- **Session number**: Increment from the current project_state.md's session
  number (check `Phase` line or latest session heading)
- **What you worked on**: Key tasks, epics, beads progressed or closed
- **What changed**: Files modified, features shipped, bugs fixed
- **Key decisions**: Architecture choices, approach changes, investigations done
- **Current state**: What's in progress, what's blocked, what's next

This is the most valuable input — only you have the session narrative. Keep it
concise but complete (10-30 lines). The subagent gathers all mechanical state
independently.

## Step 2: Spawn the Update Agent

Delegate to a **general-purpose subagent** that will:

1. Gather mechanical state with ONE compound bash command
2. Read the existing project_state.md
3. Merge your narrative with gathered state via targeted edits
4. Return a brief summary of what was updated

Replace `SESSION_SUMMARY_HERE` with your composed summary from Step 1. Replace
`REPO_ROOT` with the absolute path to the project root.

````text
Agent(
  subagent_type="general-purpose",
  description="Update project state",
  mode="bypassPermissions",
  prompt="""
Update the project state file using TARGETED EDITS.

## Session Summary (from coordinator)

SESSION_SUMMARY_HERE

## Task 1: Gather Current State

Run this ONE command to collect all fresh data:

```bash
cd REPO_ROOT && \
echo "=== GIT LOG ===" && git --no-pager log --oneline -15 && \
echo "=== GIT STATUS ===" && git branch --show-current && git status --short && \
echo "=== BEADS STATS ===" && bd stats 2>&1 && \
echo "=== OPEN EPICS ===" && (bd list --status=open --type=epic 2>/dev/null || echo "(none)") && \
echo "=== READY ISSUES ===" && (bd ready -n 5 2>/dev/null || echo "(none)")
```

## Task 2: Read Current File

Read `REPO_ROOT/.thrum/context/project_state.md` in full (use the Read tool).

## Task 3: Edit the File In-Place

Use the **Edit tool** to make targeted updates. Do NOT rewrite the entire file.
Make one Edit call per section that needs updating.

### Sections to Update (use Edit tool for each)

1. **Header line** — Update `Last Updated` date and `Phase` status summary.

2. **Current State Summary** — Update version, branch, beads counts.

3. **Architecture Health table** (STRICT 10-row rolling window):
   - The table has a **Session / Date** column — format: `S<N> · YYYY-MM-DD`
     (e.g. `S32 · 2026-04-14`)
   - Add new rows for work done this session at the TOP of the table
   - Then TRIM: keep only the 10 most recent rows PLUS any row currently in a
     broken/regressed state (statuses like BROKEN, REGRESSED, BLOCKED, or a
     note explicitly describing ongoing breakage). Drop all other historical
     COMPLETE/FIXED/MOVED/UPDATED rows — they live in git history + Recent
     Sessions below
   - This is a rolling window, not an append log. If the table is at 10 and
     you add 2 new rows, remove 2 of the oldest non-broken rows
   - Do NOT add rows for routine work already covered in Recent Sessions —
     only genuinely new capabilities, architectural shifts, or broken state

4. **Recent Sessions** — Fixed structure: last 3 detailed + blocks of 5 from
   the beginning. No graduated/ambiguous boundaries.
   - **Top of section**: the 3 most recent sessions, each with a full
     `### Session N (YYYY-MM-DD) — Title` heading and 10-25 lines of detail
   - **Below that**: a `### Session Blocks (consolidated)` heading with one
     paragraph per fixed block: `Sessions 1–5`, `Sessions 6–10`, `Sessions
     11–15`, `Sessions 16–20`, `Sessions 21–25`, etc.

5. **Session History Update Rule** (CRITICAL — keeps this cheap):
   When you add a new session, work **only** at the edges:
   - Append the new session at the top of the detailed list (now 4 detailed)
   - Take the OLDEST detailed session (position 4) and move it out
   - Find the LATEST block in "Session Blocks". Count how many sessions it
     covers:
     - If the block covers FEWER than 5 sessions, append the evicted session
       to that block's summary (extend the range in the heading, add 1-2
       lines about what it contributed)
     - If the block already covers 5 sessions, create a NEW block starting
       with the evicted session: `**Sessions M–M (date):** <one-line>`
   - NEVER re-consolidate older blocks. Blocks 1–5, 6–10, etc. are frozen
     once complete — you only touch the most recent block
   - This keeps the update fast: you read the latest block's header, decide
     append-or-new-block, and write one edit

6. **Worktree Layout** — Run `git worktree list` and rebuild the table with
   current branches. Cross-reference with `thrum team` output (if available in
   your session summary) to annotate which agent is in each worktree.

7. **Open Epics / Active Work** — Replace with current epic list from beads.

8. **What's Queued / Next Steps** — Update priorities based on current state.

### Edit Rules

- Use the Edit tool's old_string / new_string to target specific sections
- Each Edit should replace a clearly bounded section (between headings)
- Do NOT touch sections that haven't changed
- Do NOT touch frozen session blocks (blocks of 5 that are already full)
- Preserve all markdown formatting and heading hierarchy

## Task 4: Return Summary

Return ONLY a brief summary. Include:
- Which sections were edited
- Architecture Health: N rows dropped, N rows added, final count
- Session history: which block absorbed the evicted session (or "new block
  created for Sessions M–M")
- Final file line count
- Any issues encountered

## CRITICAL Rules

- Use EDIT tool, not Write tool — targeted changes only
- Architecture Health table is a **strict 10-row rolling window** + broken
  rows. Never let it grow beyond that
- Session history uses **fixed blocks of 5** from the beginning — only touch
  the latest (incomplete-or-just-filled) block
- Do NOT touch stable sections (Key Architecture Files, etc.) unless they
  changed
- Target total file size: 150-300 lines
- Use ABSOLUTE paths for all file operations
""")
````

## Step 3: Report Result

When the subagent returns, relay its summary to the user.
