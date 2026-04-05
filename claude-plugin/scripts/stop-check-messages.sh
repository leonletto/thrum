#!/bin/bash
# Stop hook: check for unread thrum messages before allowing the agent to stop.
# If unread messages exist, block the stop and direct the agent to check inbox.
# If no unread messages, exit 0 to let Claude stop normally.
#
# Dependencies: jq 1.6+ (for now/fromdate), thrum CLI
# This is an instant check (no blocking wait) — runs in <1s per turn.

# Read hook input JSON — we need stop_hook_active to prevent infinite loops
INPUT=$(cat)

# Check if we're already in a stop-hook continuation cycle
STOP_HOOK_ACTIVE=$(echo "$INPUT" | grep -o '"stop_hook_active":\s*true' || true)
if [ -n "$STOP_HOOK_ACTIVE" ]; then
  exit 0
fi

# Bail out if thrum isn't available or daemon isn't running
if ! command -v thrum &>/dev/null; then
  exit 0
fi
if ! thrum daemon status &>/dev/null; then
  exit 0
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-.}"
THRUM_CONFIG="${PROJECT_DIR}/.thrum/config.json"

# Early exit: no thrum workspace
if [ ! -f "$THRUM_CONFIG" ]; then
  exit 0
fi

# Early exit: single-agent mode
if command -v jq >/dev/null 2>&1; then
  SAM=$(jq -r '.daemon.single_agent_mode // false' "$THRUM_CONFIG" 2>/dev/null)
  if [ "$SAM" = "true" ]; then
    exit 0
  fi
fi

# Phase 1: Check unread messages
INBOX_JSON=$(cd "$PROJECT_DIR" && thrum inbox --unread --json 2>/dev/null) || exit 0
MSG_COUNT=$(echo "$INBOX_JSON" | jq -r '.unread // 0')
if [ "$MSG_COUNT" -gt 0 ]; then
  echo "ACTION REQUIRED: You have $MSG_COUNT unread message(s). Run \`thrum inbox --unread\` now to read and respond to them. Then run \`thrum message read --all\` to mark them read so this hook doesn't fire again on old messages." >&2
  exit 2
fi

# Phase 2: Check if listener process is alive via PID file
AGENT_ID="${THRUM_AGENT_ID:-${THRUM_NAME:-}}"
THRUM_DIR="${THRUM_HOME:-$PROJECT_DIR}"
PID_FILE="$THRUM_DIR/.thrum/var/${AGENT_ID}-listener.pid"

if [ -z "$AGENT_ID" ]; then
  exit 0
fi

if [ ! -f "$PID_FILE" ]; then
  echo "Your background message listener is not running. Start it now." >&2
  exit 2
fi

LISTENER_PID=$(jq -r '.pid // empty' "$PID_FILE" 2>/dev/null)
if [ -z "$LISTENER_PID" ]; then
  echo "Your background message listener is not running (invalid PID file). Start it now." >&2
  exit 2
fi

if ! kill -0 "$LISTENER_PID" 2>/dev/null; then
  echo "Your background message listener has stopped (stale PID file). Restart it." >&2
  rm -f "$PID_FILE"
  exit 2
fi

# No unread messages and listener is healthy — let Claude proceed normally
exit 0
