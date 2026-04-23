---
name: thrum-restart
description:
  Save a conversation snapshot and prepare for session restart. Use when you
  need a fresh session due to context exhaustion, rate limits, or stuck state.
# source: claude-plugin/commands/restart.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Restart

Use this skill when the user explicitly wants the `restart` Thrum workflow.
Prefer the umbrella `thrum` skill when the request spans multiple commands or
needs broader coordination judgment.

## Session Restart

Save your conversation history and prepare for a session restart.

### Steps

1. Run the save command:

```bash
thrum tmux snapshot save
```

1. Check if you are in a tmux-managed session:

```bash
thrum whoami --field tmux_session
```

1. If in tmux (non-empty output), notify the coordinator:

```bash
thrum send "Restart snapshot saved. Please run: thrum tmux restart <session-name> --force" --to @coordinator_main
```

Then wait up to 5 minutes for the coordinator to restart you. Do not exit on
your own. If no restart occurs within 5 minutes, fall back to the non-tmux
instructions below.

1. If NOT in tmux (empty output), print these instructions for the operator:

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
