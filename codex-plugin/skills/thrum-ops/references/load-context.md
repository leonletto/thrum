---
description:
  Restore saved agent work context after compaction or session restart
---

Load your previously saved agent work context. This recovers what you were
working on, decisions made, and next steps — essential after context compaction
or session restart.

```bash
thrum context show
```

If the context is empty, check for a `/tmp` backup. Backups are named with the
agent's identity (`name-role-module`) and an epoch timestamp. Find YOUR backup
and **skip it if older than 30 minutes** (it predates a more recent
`/thrum:update-context` save):

```bash
# Get your identity to find the right backup
NAME=$(thrum whoami --json 2>/dev/null | grep -o '"name":"[^"]*"' | cut -d'"' -f4)
ROLE=$(thrum whoami --json 2>/dev/null | grep -o '"role":"[^"]*"' | cut -d'"' -f4)
MODULE=$(thrum whoami --json 2>/dev/null | grep -o '"module":"[^"]*"' | cut -d'"' -f4)
BACKUP=$(ls -t /tmp/thrum-pre-compact-${NAME}-${ROLE}-${MODULE}-*.md 2>/dev/null | head -1)
if [ -n "$BACKUP" ]; then
  EPOCH=$(echo "$BACKUP" | grep -o '[0-9]*\.md$' | grep -o '[0-9]*')
  AGE=$(( $(date +%s) - EPOCH ))
  if [ "$AGE" -lt 1800 ]; then
    cat "$BACKUP"
  else
    echo "Backup is $(( AGE / 60 ))m old — likely stale. Use thrum context show instead."
  fi
else
  echo "No backup found for ${NAME:-unknown}-${ROLE:-unknown}-${MODULE:-unknown}."
fi
```

After reviewing the context, update your thrum context with a fresh narrative
summary reflecting your current understanding — follow the same process as
`/thrum:update-context`.
