---
name: thrum-restart
description: Save a conversation snapshot and prepare for session restart. Use when you need a fresh session due to context exhaustion, rate limits, or stuck state.
# source: claude-plugin/commands/restart.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Restart

Use this skill when the user explicitly wants the `restart` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


## Session Restart

Compose a Resume Plan, save your conversation snapshot, and prepare for a
session restart. The Resume Plan is the most important part — it survives
conversation-tail truncation and gives the next session a deterministic
anchor.

### Steps

#### 1. Compose a Resume Plan and write it to a temp file

Fill every field with concrete content — no placeholders. This exact text
will be (a) printed to the terminal in step 2 for the operator to read, and
(c) appended to the snapshot file in step 4. Writing it to a temp file first
guarantees those two copies stay in sync.

```bash
PLAN=$(mktemp -t resume_plan.XXXXXX)
cat > "$PLAN" <<'RESUME_PLAN_EOF'

## Resume Plan

**Shipped this session:**
- <brief bullet per merged/closed item, with bead/PR/SHA where relevant>

**In-flight work:**
- Branch: <branch name>
- Last commit: <short SHA + subject>
- Uncommitted files: <paths, or "none">
- Next concrete step: <one sentence>

**Blockers / open questions:**
- <bullets, or "none">

**Resume plan:**
1. <first step the next session should take>
2. <second step>
3. ...
   (4–8 numbered steps total)
RESUME_PLAN_EOF
```

Fill every `<...>` placeholder with real content before running the
heredoc. The `'RESUME_PLAN_EOF'` delimiter is single-quoted so nothing
inside is expanded — paste your content as literal text. Keep the
leading blank line and the `## Resume Plan` heading exactly; they are
the deterministic anchor for the next session.

#### 2. Print the Resume Plan to the terminal

**Before** saving the snapshot, show the plan so the operator can read
the hand-off at a glance:

```bash
cat "$PLAN"
```

#### 3. Save the conversation snapshot

```bash
thrum tmux snapshot save
```

This captures the conversation tail to
`.thrum/restart/<agent_id>.md` in the current worktree.

#### 4. Append the Resume Plan to the snapshot file

The snapshot file now has the conversation tail. Append the same Resume
Plan text (from the temp file) so the next-session reader has a
predictable anchor independent of the lossy tail capture.

```bash
REPO=$(git rev-parse --show-toplevel) || { echo "ERROR: not in a git worktree"; exit 1; }
AGENT=$(thrum whoami --field agent_id) || { echo "ERROR: agent not registered"; exit 1; }
[ -n "$AGENT" ] || { echo "ERROR: empty agent_id"; exit 1; }
SNAP="${REPO}/.thrum/restart/${AGENT}.md"
cat "$PLAN" >> "$SNAP"
rm -f "$PLAN"
```

Verify the last section of the snapshot contains your plan:

```bash
tail -20 "$SNAP"
```

#### 5. Check if you are in a tmux-managed session

```bash
thrum whoami --field tmux_session
```

#### 6. If in tmux (non-empty output), notify the coordinator

```bash
thrum send "Restart snapshot saved. Please run: thrum tmux restart <session-name> --force" --to @coordinator_main
```

Then wait up to 5 minutes for the coordinator to restart you. Do not exit
on your own. If no restart occurs within 5 minutes, fall back to the
non-tmux instructions below.

#### 7. If NOT in tmux (empty output), print these instructions for the operator

> Restart snapshot saved. To continue in a new session:
>
> 1. Exit this session
> 2. Start a new session in the same directory
> 3. The snapshot will be automatically loaded by `thrum prime`
>
> Or use `thrum tmux snapshot restore` to manually output the snapshot.

### When to Use

- Context window is getting full (you're seeing compaction warnings)
- You've hit rate limits and need to wait
- Your session feels stuck or unproductive
- The operator or coordinator has asked you to restart
