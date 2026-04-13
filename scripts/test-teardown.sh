#!/usr/bin/env bash
set -euo pipefail

# test-teardown.sh — Tear down a thrum test environment.
#
# Counterpart to test-setup.sh. Stops the daemon, kills tmux sessions,
# cleans up git worktrees, and removes the test directory.
#
# Usage:
#   scripts/test-teardown.sh --root /tmp/thrum-test
#
# Options:
#   --root <path>   Test environment root (required)
#   --preserve      Keep the directory tree (for inspection), just stop processes

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMUX_EXEC="${SCRIPT_DIR}/tmux-exec"
BIN="$(cd "${SCRIPT_DIR}/.." && pwd)/bin/thrum"

ROOT=""
PRESERVE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    --preserve) PRESERVE=true; shift ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$ROOT" ]; then
  echo "error: --root is required" >&2
  exit 1
fi

if [ ! -d "$ROOT" ]; then
  echo "Nothing to tear down (directory not found: $ROOT)"
  exit 0
fi

log() { echo "[test-teardown] $*"; }

COORDINATOR_DIR="$ROOT/coordinator"
IMPLEMENTER_DIR="$ROOT/implementer"

# Read test-env.json for metadata (best-effort)
ENV_FILE="$ROOT/.test-env.json"
if [ -f "$ENV_FILE" ] && command -v jq >/dev/null 2>&1; then
  COORDINATOR_DIR=$(jq -r '.coordinator // empty' "$ENV_FILE")
  IMPLEMENTER_DIR=$(jq -r '.implementer // empty' "$ENV_FILE")
  [ -n "$COORDINATOR_DIR" ] || COORDINATOR_DIR="$ROOT/coordinator"
  [ -n "$IMPLEMENTER_DIR" ] || IMPLEMENTER_DIR="$ROOT/implementer"
fi

# Step 1: Stop daemon (graceful via tmux-exec)
if [ -d "$COORDINATOR_DIR/.thrum" ]; then
  log "Stopping daemon..."
  "$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 10 -- \
    "$BIN" daemon stop 2>/dev/null || true

  # Fallback: force-kill from PID file
  PID_FILE="$COORDINATOR_DIR/.thrum/var/thrum.pid"
  if [ -f "$PID_FILE" ]; then
    DAEMON_PID=$(jq -r '.pid // empty' "$PID_FILE" 2>/dev/null || true)
    if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
      log "Force-killing daemon PID $DAEMON_PID"
      kill -9 "$DAEMON_PID" 2>/dev/null || true
    fi
  fi

  sleep 1
fi

# Step 2: Kill tmux sessions matching the test socket
# tmux-exec uses a custom socket; kill the entire server
"$TMUX_EXEC" list 2>/dev/null | while read -r session; do
  "$TMUX_EXEC" destroy "$session" 2>/dev/null || true
done

# Step 3: Remove git worktrees cleanly
if [ -d "$IMPLEMENTER_DIR" ] && [ -d "$COORDINATOR_DIR/.git" ]; then
  log "Removing implementer worktree..."
  git -C "$COORDINATOR_DIR" worktree remove --force "$IMPLEMENTER_DIR" 2>/dev/null || true
fi

# Step 4: Remove artifacts (unless --preserve)
if [ "$PRESERVE" = true ]; then
  log "Preserving test environment at $ROOT (--preserve)"
else
  log "Removing test environment at $ROOT"
  rm -rf "$ROOT"
fi

log "Teardown complete."
