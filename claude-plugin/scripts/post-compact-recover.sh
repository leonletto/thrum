#!/usr/bin/env bash
# PostCompact hook: emit orientation prompt + re-arm listener (multi-agent only)
set -euo pipefail

THRUM_HOME="${THRUM_HOME:-.}"
THRUM_CONFIG="$THRUM_HOME/.thrum/config.json"

# Always emit orientation prompt
echo "You were just compacted. Run \`thrum prime\` to restore your project context and session state." >&2

# Check single-agent mode — if so, done
if [ -f "$THRUM_CONFIG" ] && command -v jq >/dev/null 2>&1; then
  SAM=$(jq -r '.daemon.single_agent_mode // false' "$THRUM_CONFIG" 2>/dev/null)
  if [ "$SAM" = "true" ]; then
    exit 0
  fi
fi

# Multi-agent: check heartbeat and re-arm listener if stale
AGENT_ID="${THRUM_AGENT_ID:-${THRUM_NAME:-}}"
if [ -z "$AGENT_ID" ]; then
  exit 0
fi

IDENT_FILE="$THRUM_HOME/.thrum/identities/${AGENT_ID}.json"
if [ ! -f "$IDENT_FILE" ]; then
  echo "Listener may need re-arming — check with \`thrum prime\`." >&2
  exit 0
fi

HEARTBEAT=$(jq -r '.listener.heartbeat // empty' "$IDENT_FILE" 2>/dev/null)
if [ -z "$HEARTBEAT" ]; then
  echo "No listener heartbeat found. Spawn a new listener." >&2
  exit 0
fi

AGE=$(echo "null" | jq --arg hb "$HEARTBEAT" '($hb | fromdate) as $t | (now - $t) | floor' 2>/dev/null || echo 9999)
if [ "$AGE" -gt 600 ]; then
  echo "Listener heartbeat is stale (${AGE}s ago). Spawn a new listener." >&2
fi

exit 0
