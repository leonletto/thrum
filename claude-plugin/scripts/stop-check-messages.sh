#!/bin/bash
# Stop hook: check for unread thrum messages before allowing the agent to stop.
# If unread messages exist, block the stop and feed them to Claude.
# If no unread messages, exit 0 to let Claude stop normally.
#
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

# Phase 1: Check for unread messages first (instant check)
existing=$(cd "$PROJECT_DIR" && thrum inbox --unread --json 2>/dev/null)
if [ $? -eq 0 ] && [ -n "$existing" ]; then
  msg_count=$(echo "$existing" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('messages',[])))" 2>/dev/null || echo "0")
  if [ "$msg_count" -gt 0 ]; then
    echo "Thrum: $msg_count unread message(s) found. Process them and respond appropriately:" >&2
    echo "$existing" >&2
    exit 2
  fi
fi

# Phase 2: Check if the message listener is alive
# If the listener has died (stale heartbeat or missing), tell Claude to restart it.
AGENT_ID="${THRUM_AGENT_ID:-${THRUM_NAME:-}}"
THRUM_DIR="${THRUM_HOME:-$PROJECT_DIR}"
IDENT_FILE="$THRUM_DIR/.thrum/identities/${AGENT_ID}.json"

if [ -n "$AGENT_ID" ] && [ -f "$IDENT_FILE" ]; then
  # Extract listener heartbeat and session_id from identity file
  listener_info=$(python3 -c "
import json, sys, datetime
try:
    with open('$IDENT_FILE') as f:
        data = json.load(f)
    listener = data.get('listener')
    if not listener or not listener.get('heartbeat'):
        print('missing')
        sys.exit(0)
    session_id = data.get('session_id', '')
    listener_session = listener.get('session_id', '')
    if listener_session != session_id:
        print('stale_session')
        sys.exit(0)
    hb = datetime.datetime.fromisoformat(listener['heartbeat'].replace('Z', '+00:00'))
    now = datetime.datetime.now(datetime.timezone.utc)
    age_seconds = (now - hb).total_seconds()
    print(f'age={int(age_seconds)}')
except Exception as e:
    print(f'error={e}')
" 2>/dev/null)

  case "$listener_info" in
    missing|stale_session|error=*)
      echo "Your background message listener is not running. Start it now to receive messages from other agents." >&2
      exit 2
      ;;
    age=*)
      age="${listener_info#age=}"
      # If heartbeat is older than 10 minutes, listener is dead
      if [ "$age" -gt 600 ]; then
        echo "Your background message listener has stopped (last heartbeat ${age}s ago). Restart it to receive messages from other agents." >&2
        exit 2
      fi
      ;;
  esac
fi

# No unread messages and listener is healthy — let Claude proceed normally
exit 0
