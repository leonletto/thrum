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

# Multi-agent: check if listener is alive via PID file
AGENT_ID="${THRUM_AGENT_ID:-${THRUM_NAME:-}}"
if [ -z "$AGENT_ID" ]; then
  exit 0
fi

# Skip listener check for tmux-managed agents (daemon nudges directly)
TMUX_SESSION=$(THRUM_AGENT_ID="$AGENT_ID" \
  thrum whoami --field tmux_session 2>/dev/null)
if [ -n "$TMUX_SESSION" ]; then
  exit 0
fi

PID_FILE="$THRUM_HOME/.thrum/var/${AGENT_ID}-listener.pid"
if [ ! -f "$PID_FILE" ]; then
  echo "No listener running. Spawn a new listener." >&2
  exit 0
fi

LISTENER_PID=$(jq -r '.pid // empty' "$PID_FILE" 2>/dev/null)
if [ -z "$LISTENER_PID" ] || ! kill -0 "$LISTENER_PID" 2>/dev/null; then
  echo "Listener process dead. Spawn a new listener." >&2
  rm -f "$PID_FILE"
fi

exit 0
