#!/bin/bash
set -euo pipefail

# Setup thrum redirect for a git worktree
# Usage: ./scripts/setup-worktree-thrum.sh /path/to/worktree

WORKTREE_PATH="${1:?Usage: $0 /path/to/worktree}"
MAIN_REPO="$(cd "$(dirname "$0")/.." && pwd)"

# Validate main repo
if [ ! -d "$MAIN_REPO/.thrum" ]; then
    echo "Error: Main repo not initialized. Run 'thrum init' first."
    exit 1
fi

# Create redirect
mkdir -p "$WORKTREE_PATH/.thrum/identities"
echo "$MAIN_REPO/.thrum" > "$WORKTREE_PATH/.thrum/redirect"

# Verify
echo "Thrum redirect created: $WORKTREE_PATH/.thrum/redirect -> $MAIN_REPO/.thrum"

# Check daemon
SOCKET="$MAIN_REPO/.thrum/var/thrum.sock"
if [ -S "$SOCKET" ]; then
    echo "Daemon socket found at $SOCKET"
else
    echo "Daemon not running. Start with: cd $MAIN_REPO && thrum daemon start"
fi
