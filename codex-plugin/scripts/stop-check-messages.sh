#!/bin/bash
# Stop hook: check for unread thrum messages before allowing the agent to stop.
# If unread messages exist, block the stop and direct the agent to check inbox.
# If no unread messages, exit 0 to let Codex stop normally.
#
# Block mechanism: JSON {"decision":"block","reason":"..."} on stdout.
# The reason field becomes the continuation prompt re-injected to the agent.
#
# Dependencies: jq 1.6+ (for now/fromdate), thrum CLI
# This is an instant check (no blocking wait) — runs in <1s per turn.

# Read hook input JSON — we need stop_hook_active to prevent infinite loops
INPUT=$(cat)

# Step 1: Parse cwd from stdin (codex always provides it in Stop hook input).
CWD=$(echo "$INPUT" | jq -r '.cwd // "."')

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

THRUM_CONFIG="${CWD}/.thrum/config.json"

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

# Phase 1: Resolve agent identity — must happen before inbox check so the
# query is scoped to this agent only.
#
# 4-step resolution algorithm (spec §3.3.1):
# Step 2: Honor explicit env override if user set it (back-compat with manual exports).
AGENT_ID="${THRUM_AGENT_ID:-${THRUM_NAME:-}}"

# Step 3: If still unset, ask the daemon. `thrum whoami --json` walks the
# canonical identity-resolution chain (env → .thrum/identities/<name>.json by
# worktree-path match → daemon's session registry). Run from cwd so the daemon
# scopes its lookup correctly.
if [ -z "$AGENT_ID" ]; then
  WHOAMI_JSON=$(cd "$CWD" && thrum whoami --json 2>/dev/null || true)
  AGENT_ID=$(echo "$WHOAMI_JSON" | jq -r '.agent_id // empty' 2>/dev/null || true)
fi

# Step 4: Still unset → no agent registered for this cwd. Silent exit (never
# block on missing identity).
if [ -z "$AGENT_ID" ]; then
  exit 0
fi

# Phase 2: Check unread messages scoped to this agent
INBOX_JSON=$(cd "$CWD" && THRUM_AGENT_ID="$AGENT_ID" thrum inbox --unread --json 2>/dev/null) || exit 0
MSG_COUNT=$(echo "$INBOX_JSON" | jq -r '.unread // 0')
if [ "$MSG_COUNT" -gt 0 ]; then
  printf '{"decision":"block","reason":"ACTION REQUIRED: You have %s unread message(s). Run `thrum inbox --unread` now to read and respond to them. Then run `thrum message read --all` to mark them read so this hook does not fire again on old messages."}\n' "$MSG_COUNT"
  exit 0
fi

# Skip listener check for tmux-managed agents (daemon nudges directly).
# If tmux_session is set in the identity file, the agent was launched via tmux.
# The session may have died, but if so the agent wouldn't be running this hook
# either (it runs inside the tmux pane). So field presence is sufficient here.
TMUX_SESSION=$(cd "$CWD" && THRUM_AGENT_ID="$AGENT_ID" \
  thrum whoami --field tmux_session 2>/dev/null)
if [ -n "$TMUX_SESSION" ]; then
  exit 0
fi

# Phase 3: Check if listener process is alive via PID file
THRUM_DIR="${THRUM_HOME:-$CWD}"
PID_FILE="$THRUM_DIR/.thrum/var/${AGENT_ID}-listener.pid"

if [ ! -f "$PID_FILE" ]; then
  printf '{"decision":"block","reason":"Your background message listener is not running. Start it now."}\n'
  exit 0
fi

LISTENER_PID=$(jq -r '.pid // empty' "$PID_FILE" 2>/dev/null)
if [ -z "$LISTENER_PID" ]; then
  printf '{"decision":"block","reason":"Your background message listener is not running (invalid PID file). Start it now."}\n'
  exit 0
fi

if ! kill -0 "$LISTENER_PID" 2>/dev/null; then
  rm -f "$PID_FILE"
  printf '{"decision":"block","reason":"Your background message listener has stopped (stale PID file). Restart it."}\n'
  exit 0
fi

# No unread messages and listener is healthy — let Codex proceed normally
exit 0
