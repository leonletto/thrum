#!/bin/bash
# auto-restart-check.sh — Checks context usage and triggers restart if threshold exceeded.
#
# WIRING NOTE: As of Claude Code v2.x, no built-in hook event delivers
# context_window.used_percentage via stdin. The Notification hook only carries
# message/title/notification_type fields. The Stop and SubagentStop hooks fire
# too late (after Claude has already stopped) and do not include context_window
# data either.
#
# To use this script, invoke it from an external monitor instead of a plugin
# hook. Options:
#
#   1. Cron / watchdog: Poll the thrum agent state file on an interval and
#      call this script directly, passing JSON with used_percentage.
#      Example cron (every 5 min):
#        */5 * * * * echo '{}' | /path/to/auto-restart-check.sh
#      (The script will query thrum for current context usage when no stdin
#      data is available — extend as needed.)
#
#   2. Tmux watchdog loop: In a tmux window alongside the agent, run a loop
#      that reads thrum status and pipes it here.
#
#   3. Future hook support: When Claude Code adds a hook event that exposes
#      context_window.used_percentage (e.g., a ContextUpdate or Status event),
#      register this script under that event in plugin.json like so:
#
#        "ContextUpdate": [
#          {
#            "matcher": "",
#            "hooks": [
#              {
#                "type": "command",
#                "command": "${CLAUDE_PLUGIN_ROOT}/scripts/auto-restart-check.sh",
#                "timeout": 10
#              }
#            ]
#          }
#        ]

set -euo pipefail

# Read threshold from thrum config JSON (0 = disabled)
THRUM_DIR="${THRUM_DIR:-.thrum}"
CONFIG_FILE="$THRUM_DIR/config.json"
if [ ! -f "$CONFIG_FILE" ]; then
  exit 0
fi
THRESHOLD=$(jq -r '.restart.auto_threshold // 0' "$CONFIG_FILE" 2>/dev/null || echo "0")
if [ "$THRESHOLD" = "0" ] || [ -z "$THRESHOLD" ]; then
  exit 0
fi

# Read status JSON from stdin
STATUS_JSON=$(cat)

# Extract context usage percentage
USED=$(echo "$STATUS_JSON" | jq -r '.context_window.used_percentage // 0' 2>/dev/null || echo "0")

if [ "$USED" -ge "$THRESHOLD" ] 2>/dev/null; then
  # Save conversation snapshot
  thrum restart save --reason context-threshold 2>/dev/null || exit 0

  # For tmux-managed agents: trigger full restart
  TMUX_SESSION=$(thrum whoami --field tmux_session 2>/dev/null || true)
  if [ -n "$TMUX_SESSION" ]; then
    SESSION_NAME=$(echo "$TMUX_SESSION" | cut -d: -f1)
    # Force restart — snapshot already saved
    thrum tmux restart "$SESSION_NAME" --force &
  fi
fi
