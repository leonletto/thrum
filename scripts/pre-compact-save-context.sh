#!/bin/bash
# Pre-compact hook: save mechanical state to thrum context before auto-compaction.
# This captures git state, beads state, and recent activity so the agent can
# recover context after compaction. The agent's narrative summary (from
# /update-context) provides richer context, but this ensures at minimum the
# mechanical state is preserved.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
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
Run \`/update-context\` for a richer narrative summary.

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

# Output brief summary for hook feedback
echo "Pre-compact: saved git/beads/thrum state to agent context"
