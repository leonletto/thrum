#!/usr/bin/env bash
set -euo pipefail

# test-setup.sh — Create an isolated thrum test environment.
#
# Extracts the E2E setup pattern from tests/e2e/global-setup.ts into a
# standalone bash script. Reusable by both E2E tests and manual agent testing.
#
# All thrum calls go through scripts/tmux-exec to break PID ancestry and
# prevent identity contamination from the caller's shell.
#
# Usage:
#   scripts/test-setup.sh --root /tmp/thrum-test \
#     --runtime cursor --plugin cursor-plugin --name my_test
#
# Options:
#   --root <path>      Test environment root (required)
#   --runtime <name>   Runtime to init with (default: claude)
#   --plugin <path>    Plugin directory with local-install.sh (optional)
#   --name <prefix>    Agent name prefix (default: test)
#   --no-build         Skip make build-go / build-ui
#   --no-worktree      Skip implementer worktree creation
#   --no-remote        Skip bare remote creation

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMUX_EXEC="${SCRIPT_DIR}/tmux-exec"
BIN="${REPO_ROOT}/bin/thrum"

# Defaults
ROOT=""
RUNTIME="claude"
PLUGIN=""
NAME="test"
DO_BUILD=true
DO_WORKTREE=true
DO_REMOTE=true

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    --runtime) RUNTIME="$2"; shift 2 ;;
    --plugin) PLUGIN="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --no-build) DO_BUILD=false; shift ;;
    --no-worktree) DO_WORKTREE=false; shift ;;
    --no-remote) DO_REMOTE=false; shift ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$ROOT" ]; then
  echo "error: --root is required" >&2
  exit 1
fi

# Resolve plugin path to absolute
if [ -n "$PLUGIN" ]; then
  PLUGIN="$(cd "$PLUGIN" && pwd)"
  if [ ! -f "$PLUGIN/local-install.sh" ]; then
    echo "error: $PLUGIN/local-install.sh not found" >&2
    exit 1
  fi
fi

COORDINATOR_DIR="$ROOT/coordinator"
IMPLEMENTER_DIR="$ROOT/implementer"
BARE_REMOTE_DIR="$ROOT/bare-remote"
COORD_NAME="${NAME}_coordinator"
IMPL_NAME="${NAME}_implementer"

log() { echo "[test-setup] $*"; }

# Step 1: Build (optional)
if [ "$DO_BUILD" = true ]; then
  log "Building Go binary..."
  make -C "$REPO_ROOT" build-go
  log "Building UI..."
  make -C "$REPO_ROOT" build-ui
fi

if [ ! -f "$BIN" ]; then
  echo "error: thrum binary not found at $BIN" >&2
  exit 1
fi

# Step 2: Clean previous run
if [ -d "$ROOT" ]; then
  log "Cleaning previous test environment at $ROOT..."
  # Try graceful daemon stop
  if [ -d "$COORDINATOR_DIR/.thrum" ]; then
    "$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 10 -- \
      "$BIN" daemon stop 2>/dev/null || true
    # Fallback: force-kill from PID file
    PID_FILE="$COORDINATOR_DIR/.thrum/var/thrum.pid"
    if [ -f "$PID_FILE" ]; then
      DAEMON_PID=$(jq -r '.pid // empty' "$PID_FILE" 2>/dev/null || true)
      if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        log "Force-killing stale daemon PID $DAEMON_PID"
        kill -9 "$DAEMON_PID" 2>/dev/null || true
      fi
    fi
    sleep 1
  fi
  # Clean worktrees before removing
  if [ -d "$IMPLEMENTER_DIR" ] && [ -d "$COORDINATOR_DIR/.git" ]; then
    git -C "$COORDINATOR_DIR" worktree remove --force "$IMPLEMENTER_DIR" 2>/dev/null || true
  fi
  rm -rf "$ROOT"
fi

# Step 3: Create coordinator repo
log "Creating coordinator repo at $COORDINATOR_DIR"
mkdir -p "$COORDINATOR_DIR"
git -C "$COORDINATOR_DIR" init --initial-branch=main
git -C "$COORDINATOR_DIR" config user.email "test@test.com"
git -C "$COORDINATOR_DIR" config user.name "Test"
echo "# Test Repo" > "$COORDINATOR_DIR/README.md"
git -C "$COORDINATOR_DIR" add .
git -C "$COORDINATOR_DIR" commit -m "Initial commit"

# Step 4: thrum init via tmux-exec (auto-select runtime)
log "Running thrum init (runtime: $RUNTIME)..."
"$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 30 -- \
  sh -c "echo 1 | $BIN init --runtime $RUNTIME"

# Step 5: Deploy plugin to coordinator (if specified)
if [ -n "$PLUGIN" ]; then
  log "Deploying plugin to coordinator..."
  "$PLUGIN/local-install.sh" --target "$COORDINATOR_DIR"
fi

# Step 6: Register coordinator agent via tmux-exec
log "Registering coordinator agent ($COORD_NAME)..."
"$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 30 -- \
  "$BIN" quickstart --role coordinator --module all \
  --name "$COORD_NAME" --intent "Test coordinator"

# Step 7: Wait for daemon to be ready
log "Waiting for daemon..."
ATTEMPTS=0
MAX_ATTEMPTS=30
while [ $ATTEMPTS -lt $MAX_ATTEMPTS ]; do
  RESULT=$("$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 5 -- \
    "$BIN" daemon status 2>/dev/null || true)
  if echo "$RESULT" | grep -q "running\|ok"; then
    log "Daemon is ready."
    break
  fi
  ATTEMPTS=$((ATTEMPTS + 1))
  sleep 1
done
if [ $ATTEMPTS -ge $MAX_ATTEMPTS ]; then
  echo "error: daemon did not become ready within ${MAX_ATTEMPTS}s" >&2
  exit 1
fi

# Read daemon WS port
WS_PORT=$("$TMUX_EXEC" exec --cwd "$COORDINATOR_DIR" --clean --timeout 5 -- \
  "$BIN" daemon status --json 2>/dev/null | jq -r '.ws_port // empty' || true)
log "Daemon WebSocket port: ${WS_PORT:-unknown}"

# Step 8: Create implementer worktree (optional)
if [ "$DO_WORKTREE" = true ]; then
  log "Creating implementer worktree at $IMPLEMENTER_DIR"
  git -C "$COORDINATOR_DIR" worktree add "$IMPLEMENTER_DIR" -b implementer-wt HEAD

  # Write .thrum/redirect pointing to coordinator's .thrum/
  mkdir -p "$IMPLEMENTER_DIR/.thrum"
  echo "$COORDINATOR_DIR/.thrum" > "$IMPLEMENTER_DIR/.thrum/redirect"

  # Deploy plugin to implementer worktree (if specified)
  if [ -n "$PLUGIN" ]; then
    log "Deploying plugin to implementer worktree..."
    "$PLUGIN/local-install.sh" --target "$IMPLEMENTER_DIR"
  fi

  # Register implementer agent
  log "Registering implementer agent ($IMPL_NAME)..."
  "$TMUX_EXEC" exec --cwd "$IMPLEMENTER_DIR" --clean --timeout 30 -- \
    "$BIN" quickstart --role implementer --module main \
    --name "$IMPL_NAME" --intent "Test implementer"
fi

# Step 9: Create bare remote (optional)
if [ "$DO_REMOTE" = true ]; then
  log "Creating bare remote at $BARE_REMOTE_DIR"
  mkdir -p "$BARE_REMOTE_DIR"
  git -C "$BARE_REMOTE_DIR" init --bare --initial-branch=main
  git -C "$COORDINATOR_DIR" remote remove origin 2>/dev/null || true
  git -C "$COORDINATOR_DIR" remote add origin "$BARE_REMOTE_DIR"
  git -C "$COORDINATOR_DIR" push origin main
fi

# Step 10: Write .test-env.json
ENV_JSON=$(cat <<ENVEOF
{
  "root": "$ROOT",
  "coordinator": "$COORDINATOR_DIR",
  "implementer": "$IMPLEMENTER_DIR",
  "remote": "$BARE_REMOTE_DIR",
  "coordinator_name": "$COORD_NAME",
  "implementer_name": "$IMPL_NAME",
  "runtime": "$RUNTIME",
  "plugin": "$PLUGIN",
  "ws_port": ${WS_PORT:-null},
  "has_worktree": $DO_WORKTREE,
  "has_remote": $DO_REMOTE
}
ENVEOF
)
echo "$ENV_JSON" > "$ROOT/.test-env.json"

log "Test environment ready at $ROOT"
echo "$ENV_JSON"
