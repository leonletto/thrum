---
title: "Resume After Context Loss"
description:
  "How to recover and pick up work after compaction, a new session, or a crash"
category: "strategies"
order: 3
tags: ["resume", "context", "recovery", "compaction"]
last_updated: "2026-03-03"
---

## Resume After Context Loss

This is an operational strategy that agents receive via `.thrum/strategies/`. It
describes the exact sequence to follow when resuming after a context loss event
such as compaction, a new session start, or a crash.

If you are resuming after context loss (compaction, new session, crash, etc.),
follow this sequence exactly. Do not skip steps.

### Step 1: Re-Register with Thrum (Mandatory)

```bash
thrum quickstart --name <agent-name> --role <your-role> --module <branch-name> --intent "Resuming <task-or-epic>"
thrum inbox --unread
thrum sent --unread
# Tip: thrum inbox --unread peeks without marking read; thrum message read --all to mark all read
```

You must re-register even if you were registered in the previous session.
Registration is per-session and does not persist across context loss.

### Step 2: Orient from Beads

```bash
bd show <EPIC_ID>              # What is the overall state of the epic?
bd list --status=in_progress   # Is anything currently mid-flight?
bd ready                       # What tasks are unblocked and available?
```

### Step 3: Orient from Git

```bash
git --no-pager log --oneline -10   # What was committed recently?
git status                          # Is there any uncommitted work?
git diff                            # What changes exist but are not staged?
```

### Step 4: Pick Up from the First Incomplete Task

Read beads status and git history to determine exactly where work stopped. Then:

- If a task is `in_progress` in beads and has uncommitted changes: review the
  diff, decide whether to complete or reset, then continue
- If a task is `in_progress` but git shows it is fully committed: close the task
  in beads and move to the next
- If all visible tasks are `completed`: verify nothing was missed, then proceed
  to the next unblocked task or wrap up

**Key principle: DO NOT redo completed work.** Trust beads task status and git
commit history. If a task is closed in beads and committed in git, it is done —
do not re-implement it.
