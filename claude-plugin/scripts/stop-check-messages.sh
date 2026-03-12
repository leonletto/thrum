#!/bin/bash
# Stop hook: check for unread thrum messages before allowing the agent to stop.
# If unread messages exist, block the stop and feed them to Claude.
# If no unread messages, exit 0 to let Claude stop normally.
#
# This is an instant check (no blocking wait) — runs in <1s per turn.

# Log file for debugging
LOG="/tmp/thrum-stop-hook.log"

# Read hook input JSON — we need stop_hook_active to prevent infinite loops
INPUT=$(cat)
echo "$(date '+%H:%M:%S') Hook fired. Input: $INPUT" >> "$LOG"

# Check if we're already in a stop-hook continuation cycle
STOP_HOOK_ACTIVE=$(echo "$INPUT" | grep -o '"stop_hook_active":\s*true' || true)
if [ -n "$STOP_HOOK_ACTIVE" ]; then
  echo "$(date '+%H:%M:%S') stop_hook_active=true, exiting" >> "$LOG"
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
  echo "$(date '+%H:%M:%S') Phase 1: unread=$msg_count" >> "$LOG"
  if [ "$msg_count" -gt 0 ]; then
    echo "$(date '+%H:%M:%S') Phase 1: found unread, exit 2" >> "$LOG"
    echo "Thrum: $msg_count unread message(s) found. Process them and respond appropriately:" >&2
    echo "$existing" >&2
    exit 2
  fi
fi

# No unread messages — let Claude proceed normally
echo "$(date '+%H:%M:%S') No unread messages, exit 0" >> "$LOG"
exit 0
