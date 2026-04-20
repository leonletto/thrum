---
description:
  Save a conversation snapshot and prepare for session restart. Use when you
  need a fresh session due to context exhaustion, rate limits, or stuck state.
---

# Session Restart

Compose a Resume Plan, save your conversation snapshot, and prepare for a
session restart. The Resume Plan is the most important part — it survives
conversation-tail truncation and gives the next session a deterministic
anchor.

## Steps

### 1. Compose and print a Resume Plan

**Before saving the snapshot**, compose a structured Resume Plan and print
it to the terminal so the operator can read the hand-off at a glance. Fill
every field with concrete content — don't leave placeholders.

Use this template verbatim:

```
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
```

Print the filled-in block to the terminal **before** running any save
command. Keep the exact text — you will reuse it in step 3.

### 2. Save the conversation snapshot

```bash
thrum tmux snapshot save
```

This captures the conversation tail to
`.thrum/restart/<agent-name>.md`.

### 3. Append the Resume Plan to the snapshot file

Append the same Resume Plan block you printed in step 1 to the snapshot
file, under a `## Resume Plan` heading. This gives the next-session reader
a predictable anchor independent of the lossy conversation-tail capture.

```bash
REPO=$(git rev-parse --show-toplevel)
AGENT=$(thrum whoami --field agent_id)
cat >> "${REPO}/.thrum/restart/${AGENT}.md" <<'EOF'

## Resume Plan

**Shipped this session:**
- …(paste the exact same content you printed in step 1)…

**In-flight work:**
- …

**Blockers / open questions:**
- …

**Resume plan:**
1. …
EOF
```

Replace the `…` lines with your actual content before running the
heredoc. Preserve the `## Resume Plan` heading exactly — it is the
deterministic anchor.

### 4. Check if you are in a tmux-managed session

```bash
thrum whoami --field tmux_session
```

### 5. If in tmux (non-empty output), notify the coordinator

```bash
thrum send "Restart snapshot saved. Please run: thrum tmux restart <session-name> --force" --to @coordinator_main
```

Then wait up to 5 minutes for the coordinator to restart you. Do not exit
on your own. If no restart occurs within 5 minutes, fall back to the
non-tmux instructions below.

### 6. If NOT in tmux (empty output), print these instructions for the operator

> Restart snapshot saved. To continue in a new session:
>
> 1. Exit this session
> 2. Start a new session in the same directory
> 3. The snapshot will be automatically loaded by `thrum prime`
>
> Or use `thrum tmux snapshot restore` to manually output the snapshot.

## When to Use

- Context window is getting full (you're seeing compaction warnings)
- You've hit rate limits and need to wait
- Your session feels stuck or unproductive
- The operator or coordinator has asked you to restart
