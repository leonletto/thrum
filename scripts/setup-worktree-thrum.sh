#!/bin/bash
set -euo pipefail

# Setup Thrum worktree with optional branch creation, redirects, and identity
# Usage:
#   ./scripts/setup-worktree-thrum.sh                                      # auto-detect worktrees
#   ./scripts/setup-worktree-thrum.sh <worktree-path>                      # redirect-only mode
#   ./scripts/setup-worktree-thrum.sh <worktree-path> <branch> [options]   # full setup
#
# Options:
#   --identity <name>    Agent identity name (e.g., feature-implementer-1)
#   --role <role>        Agent role (default: implementer)
#   --module <module>    Agent module (default: derived from branch name)
#   --preamble <file>    Preamble file to append to default preamble
#   --base <branch>      Base branch to create from (default: main)
#
# Creates a .thrum/redirect file in each worktree pointing to the main
# repository's .thrum directory, so all worktrees share the same daemon,
# messages, and identities.

MAIN_REPO="$(cd "$(dirname "$0")/.." && pwd)"
REPO_NAME="$(basename "$MAIN_REPO")"

# Validate main repo has .thrum initialized
if [[ ! -d "$MAIN_REPO/.thrum" ]]; then
    echo "Error: Thrum not initialized in $MAIN_REPO"
    echo "Run 'thrum init' first."
    exit 1
fi

MAIN_THRUM_ABS="$(cd "$MAIN_REPO/.thrum" && pwd)"

# --- Argument parsing ---
WORKTREE_PATH=""
BRANCH=""
IDENTITY=""
ROLE="implementer"
MODULE=""
PREAMBLE=""
BASE_BRANCH="main"

POSITIONAL=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --identity)
            IDENTITY="$2"; shift 2 ;;
        --role)
            ROLE="$2"; shift 2 ;;
        --module)
            MODULE="$2"; shift 2 ;;
        --preamble)
            PREAMBLE="$2"; shift 2 ;;
        --base)
            BASE_BRANCH="$2"; shift 2 ;;
        --help|-h)
            echo "Usage: $0 [<worktree-path> [<branch>]] [options]"
            echo ""
            echo "Positional arguments:"
            echo "  <worktree-path>       Path for the worktree (required for setup)"
            echo "  <branch>              Branch name (creates branch + worktree if provided)"
            echo ""
            echo "Options:"
            echo "  --identity <name>     Agent identity name"
            echo "  --role <role>         Agent role (default: implementer)"
            echo "  --module <module>     Agent module (default: derived from branch name)"
            echo "  --preamble <file>     Preamble file to compose with default preamble"
            echo "  --base <branch>       Base branch to create from (default: main)"
            echo "  --help, -h            Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                                    # auto-detect worktrees"
            echo "  $0 ~/.workspaces/thrum/auth                          # redirect-only"
            echo "  $0 ~/.workspaces/thrum/auth feature/auth             # branch + worktree"
            echo "  $0 ~/.workspaces/thrum/auth feature/auth --identity auth-impl"
            exit 0
            ;;
        -*)
            echo "Error: Unknown flag: $1"
            echo "Run '$0 --help' for usage."
            exit 1
            ;;
        *)
            POSITIONAL+=("$1"); shift ;;
    esac
done

# Assign positional args
if [[ ${#POSITIONAL[@]} -ge 1 ]]; then
    WORKTREE_PATH="${POSITIONAL[0]}"
fi
if [[ ${#POSITIONAL[@]} -ge 2 ]]; then
    BRANCH="${POSITIONAL[1]}"
fi

# Default module to branch name (strip feature/ prefix if present)
if [[ -z "$MODULE" && -n "$BRANCH" ]]; then
    MODULE="${BRANCH#feature/}"
fi

# --- setup_worktree function (thrum redirect) ---
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
    if [[ -f "$wt_path/.thrum/redirect" ]]; then
        local existing
        existing="$(cat "$wt_path/.thrum/redirect")"
        if [[ "$existing" == "$MAIN_THRUM_ABS" ]]; then
            echo "  Thrum: already configured"
            return 0
        fi
    fi

    mkdir -p "$wt_path/.thrum"
    echo "$MAIN_THRUM_ABS" > "$wt_path/.thrum/redirect"
    echo "  Thrum: redirect → $MAIN_THRUM_ABS"
}

# --- setup_beads function ---
setup_beads() {
    local wt_path="$1"
    local beads_script="$MAIN_REPO/scripts/setup-worktree-beads.sh"

    if [[ ! -x "$beads_script" ]]; then
        echo "  Beads: setup script not found, skipping"
        return 0
    fi

    if "$beads_script" "$wt_path" 2>&1 | sed 's/^/  Beads: /'; then
        return 0
    else
        echo "  Beads: setup failed (continuing anyway)"
        return 0
    fi
}

# --- Main logic ---

# Case 1: No arguments — auto-detect all worktrees
if [[ -z "$WORKTREE_PATH" ]]; then
    WORKTREES="$(cd "$MAIN_REPO" && git worktree list --porcelain 2>/dev/null | grep '^worktree ' | sed 's/^worktree //')"

    if [[ -z "$WORKTREES" ]]; then
        echo "No git worktrees found."
        echo ""
        echo "To create a worktree and set up Thrum:"
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
        echo "To create a worktree and set up Thrum:"
        echo "  git worktree add <path> <branch>"
        echo "  $0 <path>"
        echo ""
        echo "Suggested worktree locations:"
        echo "  ~/.workspaces/$REPO_NAME/<feature-name>"
        echo "  ~/.worktrees/$REPO_NAME/<feature-name>"
        exit 0
    fi

    echo "Found $OTHER_COUNT worktree(s). Setting up Thrum redirects..."
    echo ""

    while IFS= read -r wt; do
        setup_worktree "$wt"
    done <<< "$WORKTREES"

    echo ""
    echo "Done. All worktrees now share Thrum from: $MAIN_THRUM_ABS"
    exit 0
fi

# Case 2: Worktree path provided, no branch — redirect-only mode
if [[ -z "$BRANCH" ]]; then
    if [[ ! -d "$WORKTREE_PATH" ]]; then
        echo "Error: Worktree path does not exist: $WORKTREE_PATH"
        echo "Provide a branch name to create a new worktree:"
        echo "  $0 $WORKTREE_PATH <branch-name>"
        exit 1
    fi

    echo "Setting up redirects for existing worktree..."
    setup_worktree "$WORKTREE_PATH"
    setup_beads "$WORKTREE_PATH"

    echo ""
    echo "Verification:"
    if command -v thrum &>/dev/null; then
        (cd "$WORKTREE_PATH" && thrum daemon status 2>&1 | head -3) || true
    else
        echo "  thrum CLI not found, skipping verification"
    fi
    exit 0
fi

# Case 3: Full setup — branch + worktree + redirects + identity
echo "Setting up worktree..."

# Step 1: Branch & worktree creation
if git -C "$MAIN_REPO" rev-parse --verify "$BRANCH" &>/dev/null; then
    echo "  Branch '$BRANCH' exists, creating worktree..."
    git -C "$MAIN_REPO" worktree add "$WORKTREE_PATH" "$BRANCH"
else
    echo "  Creating branch '$BRANCH' from '$BASE_BRANCH'..."
    git -C "$MAIN_REPO" worktree add "$WORKTREE_PATH" -b "$BRANCH" "$BASE_BRANCH"
fi

# Resolve worktree path to absolute
if [[ "$WORKTREE_PATH" != /* ]]; then
    WORKTREE_PATH="$(cd "$WORKTREE_PATH" && pwd)"
fi

# Step 2: Thrum redirect
setup_worktree "$WORKTREE_PATH"

# Step 3: Beads redirect
setup_beads "$WORKTREE_PATH"

# Step 4: Quickstart delegation (if --identity provided)
if [[ -n "$IDENTITY" ]]; then
    echo "  Running quickstart..."
    QS_CMD=(thrum quickstart --name "$IDENTITY" --role "$ROLE" --module "$MODULE")
    if [[ -n "$PREAMBLE" ]]; then
        # Resolve preamble path relative to main repo if not absolute
        if [[ "$PREAMBLE" != /* ]]; then
            PREAMBLE="$MAIN_REPO/$PREAMBLE"
        fi
        QS_CMD+=(--preamble-file "$PREAMBLE")
    fi

    if (cd "$WORKTREE_PATH" && "${QS_CMD[@]}"); then
        echo "  Quickstart: identity registered"
    else
        echo "Error: Quickstart failed. Worktree created but identity not configured."
        exit 1
    fi
fi

# Step 5: Verification summary
echo ""
echo "Worktree created:"
echo "  Path:     $WORKTREE_PATH"
echo "  Branch:   $BRANCH"
echo "  Thrum:    redirect → $MAIN_THRUM_ABS"

# Check beads redirect
if [[ -f "$WORKTREE_PATH/.beads/redirect" ]]; then
    BEADS_TARGET="$(cat "$WORKTREE_PATH/.beads/redirect")"
    echo "  Beads:    redirect → $BEADS_TARGET"
else
    echo "  Beads:    not configured"
fi

if [[ -n "$IDENTITY" ]]; then
    echo "  Identity: $IDENTITY (.thrum/identities/$IDENTITY.json)"
    echo "  Context:  .thrum/context/$IDENTITY.md (empty, use /update-context)"
    echo "  Preamble: .thrum/context/${IDENTITY}_preamble.md"
fi

# Step 6: Reminder
FEATURE_NAME="${BRANCH#feature/}"
echo ""
echo "Remember to update CLAUDE.md worktree table:"
echo "| $FEATURE_NAME | \`$BRANCH\` | \`$WORKTREE_PATH\` |"
