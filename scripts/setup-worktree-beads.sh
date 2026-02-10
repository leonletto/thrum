#!/bin/bash
set -euo pipefail

# Setup Beads redirect for git worktrees
# Usage:
#   ./scripts/setup-worktree-beads.sh                  # auto-detect worktrees
#   ./scripts/setup-worktree-beads.sh <worktree-path>  # setup a specific worktree
#
# Creates a .beads/redirect file in each worktree pointing to the main
# repository's .beads directory, so all worktrees share the same issue database.

MAIN_REPO="$(cd "$(dirname "$0")/.." && pwd)"
REPO_NAME="$(basename "$MAIN_REPO")"

# Validate main repo has .beads initialized
if [[ ! -d "$MAIN_REPO/.beads" ]]; then
    echo "Error: Beads not initialized in $MAIN_REPO"
    echo "Run 'bd init' first."
    exit 1
fi

MAIN_BEADS_ABS="$(cd "$MAIN_REPO/.beads" && pwd)"

setup_worktree() {
    local wt_path="$1"

    # Resolve to absolute path
    if [[ "$wt_path" != /* ]]; then
        wt_path="$(cd "$wt_path" 2>/dev/null && pwd)" || {
            echo "  Error: Path does not exist: $1"
            return 1
        }
    fi

    # Skip main repo
    if [[ "$wt_path" == "$MAIN_REPO" ]]; then
        return 0
    fi

    # Check if already set up
    if [[ -f "$wt_path/.beads/redirect" ]]; then
        local existing
        existing="$(cat "$wt_path/.beads/redirect")"
        if [[ "$existing" == "$MAIN_BEADS_ABS" ]]; then
            echo "  Already configured: $wt_path"
            return 0
        fi
    fi

    mkdir -p "$wt_path/.beads"
    echo "$MAIN_BEADS_ABS" > "$wt_path/.beads/redirect"
    echo "  Created redirect: $wt_path -> $MAIN_BEADS_ABS"
}

# If a path was given, set up just that worktree
if [[ $# -ge 1 ]]; then
    echo "Setting up Beads redirect..."
    setup_worktree "$1"
    echo ""
    echo "Verification:"
    if command -v bd &>/dev/null; then
        cd "$1" && bd where
    else
        echo "  bd CLI not found, skipping verification"
    fi
    exit 0
fi

# No argument — auto-detect worktrees via git
WORKTREES="$(cd "$MAIN_REPO" && git worktree list --porcelain 2>/dev/null | grep '^worktree ' | sed 's/^worktree //')"

if [[ -z "$WORKTREES" ]]; then
    echo "No git worktrees found."
    echo ""
    echo "To create a worktree and set up Beads:"
    echo "  git worktree add <path> <branch>"
    echo "  $0 <path>"
    echo ""
    echo "Suggested worktree locations:"
    echo "  ~/.workspaces/$REPO_NAME/<feature-name>"
    echo "  ~/.worktrees/$REPO_NAME/<feature-name>"
    exit 0
fi

# Count worktrees excluding main
OTHER_COUNT=0
while IFS= read -r wt; do
    [[ "$wt" == "$MAIN_REPO" ]] && continue
    OTHER_COUNT=$((OTHER_COUNT + 1))
done <<< "$WORKTREES"

if [[ "$OTHER_COUNT" -eq 0 ]]; then
    echo "No additional worktrees found (only the main repo)."
    echo ""
    echo "To create a worktree and set up Beads:"
    echo "  git worktree add <path> <branch>"
    echo "  $0 <path>"
    echo ""
    echo "Suggested worktree locations:"
    echo "  ~/.workspaces/$REPO_NAME/<feature-name>"
    echo "  ~/.worktrees/$REPO_NAME/<feature-name>"
    exit 0
fi

echo "Found $OTHER_COUNT worktree(s). Setting up Beads redirects..."
echo ""

while IFS= read -r wt; do
    setup_worktree "$wt"
done <<< "$WORKTREES"

echo ""
echo "Done. All worktrees now share Beads from: $MAIN_BEADS_ABS"
