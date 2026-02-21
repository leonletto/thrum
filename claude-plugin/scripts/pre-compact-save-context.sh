#!/bin/bash
# Pre-compact hook: save mechanical state to thrum context before auto-compaction.
# This captures git state, beads state, and recent activity so the agent can
# recover context after compaction. The agent's narrative summary (from
# /update-context) provides richer context, but this ensures at minimum the
# mechanical state is preserved.
#
# Bundled with the thrum Claude plugin — runs from ${CLAUDE_PLUGIN_ROOT}/scripts/.

set -euo pipefail

# Find the repo root from the current working directory (set by Claude Code)
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO_ROOT"

# Gather git state
BRANCH=$(git branch --show-current 2>/dev/null || echo "unknown")
RECENT_COMMITS=$(git --no-pager log --oneline -10 2>/dev/null || echo "unavailable")
GIT_STATUS=$(git status --short 2>/dev/null || echo "unavailable")
AHEAD=$(git rev-list --count @{upstream}..HEAD 2>/dev/null || echo "?")

# Gather beads state (skip if unavailable)
BD_STATS=$(bd stats 2>/dev/null || echo "beads unavailable")
BD_IN_PROGRESS=$(bd list --status=in_progress 2>/dev/null | head -20 || echo "none")
BD_READY=$(bd ready 2>/dev/null | head -15 || echo "none")

# Gather thrum agent info
THRUM_STATUS=$(thrum status 2>/dev/null | head -10 || echo "unavailable")

# Compose context
CONTEXT=$(cat <<CTXEOF
## Pre-Compact State Snapshot

**Saved automatically before context compaction.**
Run \`/thrum:load-context\` for a richer narrative summary.

### Git State
- **Branch:** $BRANCH
- **Ahead of origin:** $AHEAD commits
- **Uncommitted changes:**
\`\`\`
$GIT_STATUS
\`\`\`

### Recent Commits
\`\`\`
$RECENT_COMMITS
\`\`\`

### Beads State
\`\`\`
$BD_STATS
\`\`\`

**In-progress tasks:**
\`\`\`
$BD_IN_PROGRESS
\`\`\`

**Ready to work:**
\`\`\`
$BD_READY
\`\`\`

### Thrum Agent Status
\`\`\`
$THRUM_STATUS
\`\`\`
CTXEOF
)

# Save to thrum context
echo "$CONTEXT" | thrum context save 2>/dev/null || true

# Also write to /tmp as backup (in case thrum context save fails)
# Include agent identity + epoch in filename for multi-agent disambiguation
WHOAMI_JSON=$(thrum whoami --json 2>/dev/null || echo "{}")
AGENT_NAME=$(echo "$WHOAMI_JSON" | grep '"agent_id"' | sed 's/.*"agent_id": *"\([^"]*\)".*/\1/' || echo "unknown")
AGENT_ROLE=$(echo "$WHOAMI_JSON" | grep '"role"' | sed 's/.*"role": *"\([^"]*\)".*/\1/' || echo "unknown")
AGENT_MODULE=$(echo "$WHOAMI_JSON" | grep '"module"' | sed 's/.*"module": *"\([^"]*\)".*/\1/' || echo "unknown")
EPOCH=$(date +%s)
BACKUP_FILE="/tmp/thrum-pre-compact-${AGENT_NAME}-${AGENT_ROLE}-${AGENT_MODULE}-${EPOCH}.md"
echo "$CONTEXT" > "$BACKUP_FILE" 2>/dev/null || true
# Clean up old backups for THIS agent (keep only the latest)
ls -t /tmp/thrum-pre-compact-${AGENT_NAME}-${AGENT_ROLE}-${AGENT_MODULE}-*.md 2>/dev/null | tail -n +2 | xargs rm -f 2>/dev/null || true

# Output brief summary for hook feedback (PreCompact output does NOT survive
# compaction — this is informational only. The agent recovers context via
# /thrum:load-context which reads from thrum context saved above.)
echo "Pre-compact: saved state to thrum context + ${BACKUP_FILE}. After compaction, run /thrum:load-context to recover."
