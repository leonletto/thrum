#!/bin/bash
# Verify JSON output coverage for all CLI commands.
# Usage: bash scripts/verify-json-output.sh
#
# Requires: thrum daemon running, jq installed.
# Exit codes: 0=all pass, 1=failure

set -e

PASS=0
FAIL=0
SKIP=0

check_json() {
    local desc="$1"
    shift
    local output
    local exit_code=0

    output=$("$@" 2>&1) || exit_code=$?

    # For wait command, exit 1 (timeout) is expected
    if [ "$exit_code" -ne 0 ] && ! echo "$desc" | grep -q "wait"; then
        echo "  FAIL: $desc (exit code $exit_code)"
        echo "        $output" | head -3
        FAIL=$((FAIL + 1))
        return
    fi

    # Verify output is valid JSON
    if echo "$output" | jq . > /dev/null 2>&1; then
        echo "  PASS: $desc"
        PASS=$((PASS + 1))
    else
        echo "  FAIL: $desc (invalid JSON)"
        echo "        $output" | head -3
        FAIL=$((FAIL + 1))
    fi
}

echo "Testing JSON output for all commands..."
echo ""

# Check daemon is running
if ! thrum daemon status > /dev/null 2>&1; then
    echo "Starting daemon..."
    thrum daemon start || true
    sleep 1
fi

# Register a test agent for commands that need one
thrum quickstart --name json_test --role tester --module test --json > /dev/null 2>&1 || true

echo "Agent commands:"
check_json "agent list" thrum agent list --json
check_json "agent list --context" thrum agent list --context --json

echo ""
echo "Session commands:"
check_json "status" thrum status --json
check_json "overview" thrum overview --json

echo ""
echo "Messaging commands:"
check_json "inbox" thrum inbox --json
check_json "inbox --unread" thrum inbox --unread --json

echo ""
echo "Coordination commands:"
check_json "who-has" thrum who-has test.go --json

echo ""
echo "Daemon commands:"
check_json "daemon status" thrum daemon status --json

echo ""
echo "Wait command (expect timeout):"
check_json "wait --timeout 1s" thrum wait --timeout 1s --json

echo ""
echo "---"
echo "Results: $PASS passed, $FAIL failed, $SKIP skipped"

if [ "$FAIL" -gt 0 ]; then
    echo "FAILED: Some commands do not support --json output"
    exit 1
fi

echo "All commands support JSON output"
