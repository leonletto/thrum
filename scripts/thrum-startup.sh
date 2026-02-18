#!/bin/bash
# Universal Thrum agent startup script
# Works from any runtime: Claude hooks, Codex hooks, Cursor extensions, Gemini profile.sh
#
# Usage:
#   bash scripts/thrum-startup.sh [name] [role] [module]
#   THRUM_NAME=alice THRUM_ROLE=reviewer bash scripts/thrum-startup.sh
#
# Environment variables (override positional args):
#   THRUM_NAME    - Agent name (default: default_agent)
#   THRUM_ROLE    - Agent role (default: implementer)
#   THRUM_MODULE  - Agent module (default: main)
#   THRUM_INTENT  - Session intent (default: General agent work)
#   THRUM_ANNOUNCE - Set to "true" to broadcast presence (default: false)
set -e

# Configuration (env vars take precedence over positional args)
AGENT_NAME="${THRUM_NAME:-${1:-default_agent}}"
AGENT_ROLE="${THRUM_ROLE:-${2:-implementer}}"
AGENT_MODULE="${THRUM_MODULE:-${3:-main}}"
AGENT_INTENT="${THRUM_INTENT:-General agent work}"

# 1. Ensure daemon is running
if ! thrum daemon status &>/dev/null; then
  thrum daemon start
fi

# 2. Register agent (idempotent)
thrum quickstart \
  --name "$AGENT_NAME" \
  --role "$AGENT_ROLE" \
  --module "$AGENT_MODULE" \
  --intent "$AGENT_INTENT" \
  --json

# 3. Check inbox and output context
thrum inbox --unread --json

# 4. Optional: Announce presence
if [ "${THRUM_ANNOUNCE:-false}" = "true" ]; then
  thrum send "Agent $AGENT_NAME online" --to @everyone --priority low --json
fi
