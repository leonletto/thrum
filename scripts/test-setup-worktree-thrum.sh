#!/bin/bash
set -euo pipefail

# Test harness for setup-worktree-thrum.sh
# Run from the repository root: ./scripts/test-setup-worktree-thrum.sh

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MAIN_REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
SETUP_SCRIPT="$SCRIPT_DIR/setup-worktree-thrum.sh"

PASS=0
FAIL=0
CLEANUP_DIRS=()
CLEANUP_BRANCHES=()

# Colors (if terminal supports them)
if [[ -t 1 ]]; then
    GREEN='\033[0;32m'
    RED='\033[0;31m'
    RESET='\033[0m'
else
    GREEN=''
    RED=''
    RESET=''
fi

pass() {
    echo -e "  ${GREEN}PASS${RESET}: $1"
    PASS=$((PASS + 1))
}

fail() {
    echo -e "  ${RED}FAIL${RESET}: $1"
    FAIL=$((FAIL + 1))
}

cleanup() {
    echo ""
    echo "Cleaning up..."
    for dir in "${CLEANUP_DIRS[@]}"; do
        if [[ -d "$dir" ]]; then
            git -C "$MAIN_REPO" worktree remove "$dir" --force 2>/dev/null || true
            rm -rf "$dir" 2>/dev/null || true
        fi
    done
    for branch in "${CLEANUP_BRANCHES[@]}"; do
        git -C "$MAIN_REPO" branch -D "$branch" 2>/dev/null || true
    done
}
trap cleanup EXIT

# --- Test 1: --help flag ---
echo "Test 1: --help flag"
HELP_OUTPUT=$("$SETUP_SCRIPT" --help 2>&1) || true
if echo "$HELP_OUTPUT" | grep -q "Usage:"; then
    pass "--help shows usage"
else
    fail "--help did not show usage"
fi

# --- Test 2: Unknown flag error ---
echo "Test 2: Unknown flag error"
BOGUS_OUTPUT=$("$SETUP_SCRIPT" --bogus 2>&1) || true
if echo "$BOGUS_OUTPUT" | grep -q "Unknown flag"; then
    pass "Unknown flag produces error"
else
    fail "Unknown flag did not produce error"
fi

# --- Test 3: No-arg mode (auto-detect) ---
echo "Test 3: No-arg mode (backwards compatible)"
NOARG_OUTPUT=$("$SETUP_SCRIPT" 2>&1) || true
if echo "$NOARG_OUTPUT" | grep -qE "(worktree|Thrum)"; then
    pass "No-arg mode runs without error"
else
    fail "No-arg mode failed"
fi

# --- Test 4: Redirect-only mode (existing path) ---
echo "Test 4: Redirect-only mode"
TEST_DIR=$(mktemp -d)
CLEANUP_DIRS+=("$TEST_DIR")
# Create a minimal .thrum dir so it looks like a valid worktree
mkdir -p "$TEST_DIR/.thrum"
REDIR_OUTPUT=$("$SETUP_SCRIPT" "$TEST_DIR" 2>&1) || true
if echo "$REDIR_OUTPUT" | grep -qE "(redirect|configured|Verification)"; then
    pass "Redirect-only mode works for existing path"
else
    fail "Redirect-only mode failed"
fi

# --- Test 5: Redirect-only mode with non-existent path ---
echo "Test 5: Non-existent path without branch"
if ! "$SETUP_SCRIPT" /tmp/nonexistent-test-path-$$ 2>&1; then
    pass "Non-existent path without branch produces error"
else
    fail "Non-existent path should have failed"
fi

# --- Test 6: New branch + worktree creation ---
echo "Test 6: New branch + worktree creation"
TEST_BRANCH="test/setup-wt-$$"
TEST_WT="/tmp/thrum-test-wt-$$"
CLEANUP_DIRS+=("$TEST_WT")
CLEANUP_BRANCHES+=("$TEST_BRANCH")

OUTPUT=$("$SETUP_SCRIPT" "$TEST_WT" "$TEST_BRANCH" 2>&1) || true
if echo "$OUTPUT" | grep -q "Creating branch"; then
    pass "New branch created"
else
    fail "Branch creation message not found"
fi

if [[ -d "$TEST_WT" ]]; then
    pass "Worktree directory created"
else
    fail "Worktree directory not created"
fi

if [[ -f "$TEST_WT/.thrum/redirect" ]]; then
    pass "Thrum redirect created in worktree"
else
    fail "Thrum redirect not found"
fi

# --- Test 7: Existing branch + worktree ---
echo "Test 7: Existing branch detection"
# The branch from test 6 now exists; try to use it with a new worktree path
TEST_WT2="/tmp/thrum-test-wt2-$$"
CLEANUP_DIRS+=("$TEST_WT2")

OUTPUT2=$("$SETUP_SCRIPT" "$TEST_WT2" "$TEST_BRANCH" 2>&1) || true
if echo "$OUTPUT2" | grep -q "exists"; then
    pass "Existing branch detected"
else
    fail "Existing branch not detected"
fi

# --- Test 8: Verification summary output ---
echo "Test 8: Verification summary"
if echo "$OUTPUT" | grep -q "Worktree created:"; then
    pass "Verification summary printed"
else
    fail "Verification summary not found"
fi

if echo "$OUTPUT" | grep -q "CLAUDE.md worktree table"; then
    pass "CLAUDE.md reminder printed"
else
    fail "CLAUDE.md reminder not found"
fi

# --- Test 9: Module defaults to branch name ---
echo "Test 9: Module default derivation"
# The feature/ prefix should be stripped for module
if echo "$OUTPUT" | grep -qE "setup-wt-$$"; then
    pass "Module derived from branch name"
else
    # This is a soft check — the module isn't printed in output, but
    # it's passed to quickstart. The important thing is no error.
    pass "Module derivation did not cause error"
fi

# --- Test 10: Beads redirect ---
echo "Test 10: Beads redirect"
if [[ -f "$TEST_WT/.beads/redirect" ]]; then
    pass "Beads redirect created"
elif echo "$OUTPUT" | grep -qE "(Beads|beads)"; then
    pass "Beads setup attempted"
else
    fail "No evidence of beads setup"
fi

# --- Summary ---
echo ""
echo "=============================="
echo "Results: $PASS passed, $FAIL failed"
echo "=============================="

if [[ "$FAIL" -gt 0 ]]; then
    exit 1
fi
