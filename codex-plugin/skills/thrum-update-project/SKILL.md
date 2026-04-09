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

3. **Architecture Health table** — Add/update rows for work done this session.
   Only add rows for genuinely new capabilities.

4. **Recent Sessions** — The file should have AT MOST 3 detailed session
   sections. The new session goes at the top.
   - **If there are already 3 detailed sessions**: Consolidate the oldest into
     a summary paragraph under a "Previous Sessions" section.
   - **If there are more than 3**: Consolidate ALL sessions older than the 3
     most recent into grouped summaries.

5. **Session History Management** (IMPORTANT):
   - **Last 3 sessions**: Keep full detail (10-20 lines each)
   - **Sessions N-10 to N-3**: Consolidate into 2-3 themed paragraphs
   - **Sessions before N-10**: One-paragraph summary
   - Target: session history section ~80-100 lines total

6. **Open Epics / Active Work** — Replace with current epic list from beads.

7. **What's Queued / Next Steps** — Update priorities based on current state.

### Edit Rules

- Use the Edit tool's old_string / new_string to target specific sections
- Each Edit should replace a clearly bounded section (between headings)
- Do NOT touch sections that haven't changed
- Preserve all markdown formatting and heading hierarchy

## Task 4: Return Summary

Return ONLY a brief summary. Include:
- Which sections were edited
- What changed in each
- Final file line count
- Any issues encountered

## CRITICAL Rules

- Use EDIT tool, not Write tool — targeted changes only
- Do NOT touch stable sections (Key Architecture Files, etc.) unless they changed
- Do NOT let session history grow unbounded — consolidate per the rules above
- Target total file size: 150-300 lines
- Use ABSOLUTE paths for all file operations
""")
````

## Step 3: Report Result

When the subagent returns, relay its summary to the user.
