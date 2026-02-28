#!/bin/bash
# Pre-compact hook: save mechanical state to thrum context before auto-compaction.
#
# NOTE: The canonical version of this script is bundled with the thrum Claude
# plugin at claude-plugin/scripts/pre-compact-save-context.sh. This copy exists
# for repos that don't use the plugin but want the pre-compact hook.
#
# If using the plugin, the hook runs via ${CLAUDE_PLUGIN_ROOT}/scripts/ — this
# file is only used if referenced directly from .claude/settings.json hooks.

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

# Save to thrum context — APPEND to existing context, don't overwrite.
# First load existing context, strip any previous pre-compact snapshot, then append.
EXISTING=$(thrum context show --raw --no-preamble 2>/dev/null || true)
if [ -n "$EXISTING" ]; then
  # Remove any previous "## Pre-Compact State Snapshot" section (and everything after it)
  TRIMMED=$(echo "$EXISTING" | sed '/^## Pre-Compact State Snapshot$/,$d')
  if [ -n "$(echo "$TRIMMED" | tr -d '[:space:]')" ]; then
    MERGED=$(printf '%s\n\n%s' "$TRIMMED" "$CONTEXT")
  else
    MERGED="$CONTEXT"
  fi
else
  MERGED="$CONTEXT"
fi
echo "$MERGED" | thrum context save 2>/dev/null || true

# Also write to /tmp as backup (in case thrum context save fails)
# Include agent identity + epoch in filename for multi-agent disambiguation
AGENT_NAME=$(thrum whoami --json 2>/dev/null | grep -o '"name":"[^"]*"' | cut -d'"' -f4 || echo "unknown")
AGENT_ROLE=$(thrum whoami --json 2>/dev/null | grep -o '"role":"[^"]*"' | cut -d'"' -f4 || echo "unknown")
AGENT_MODULE=$(thrum whoami --json 2>/dev/null | grep -o '"module":"[^"]*"' | cut -d'"' -f4 || echo "unknown")
EPOCH=$(date +%s)
BACKUP_FILE="/tmp/thrum-pre-compact-${AGENT_NAME}-${AGENT_ROLE}-${AGENT_MODULE}-${EPOCH}.md"
echo "$CONTEXT" > "$BACKUP_FILE" 2>/dev/null || true
# Clean up old backups for THIS agent (keep only the latest)
ls -t /tmp/thrum-pre-compact-${AGENT_NAME}-${AGENT_ROLE}-${AGENT_MODULE}-*.md 2>/dev/null | tail -n +2 | xargs rm -f 2>/dev/null || true

# Output brief summary for hook feedback
echo "Pre-compact: saved state to thrum context + ${BACKUP_FILE}. After compaction, run /thrum:load-context to recover."
