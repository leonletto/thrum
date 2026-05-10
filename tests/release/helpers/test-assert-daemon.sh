#!/usr/bin/env bash
# tests/release/helpers/test-assert-daemon.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/ephemeral-daemon.sh"
source "${SCRIPT_DIR}/assert-daemon.sh"

FIXTURE="$(mktemp -d)"
trap 'ephemeral_daemon_stop; rm -rf "$FIXTURE"' EXIT

ephemeral_daemon_start "$FIXTURE" || { echo "FAIL: ephemeral daemon"; exit 1; }

# Register two agents in the fixture daemon via tmux-exec so they are
# associated with the clean-PID-chain pool pane (not the Claude session pane).
# This is required for inbox/send impersonation to pass the cross_worktree guard.
ephemeral_te_exec THRUM_NAME=test_coord THRUM_ROLE=coordinator THRUM_MODULE=main -- \
  thrum quickstart --name test_coord --role coordinator --module main --force >/dev/null
ephemeral_te_exec THRUM_NAME=test_impl THRUM_ROLE=implementer THRUM_MODULE=billing -- \
  thrum quickstart --name test_impl --role implementer --module billing --force >/dev/null

# agent_registered (reads daemon state — caller identity does not matter)
assert_daemon_agent_registered test_coord coordinator main || { echo "FAIL: agent_registered coord"; exit 1; }
assert_daemon_agent_registered test_impl implementer billing || { echo "FAIL: agent_registered impl"; exit 1; }
! assert_daemon_agent_registered test_missing implementer billing || { echo "FAIL: agent_registered negative"; exit 1; }

# Send a message from test_coord to test_impl (also via tmux-exec for clean PID chain)
ephemeral_te_exec THRUM_NAME=test_coord THRUM_ROLE=coordinator THRUM_MODULE=main -- \
  thrum send --to @test_impl "Test prompt for billing" >/dev/null

# message_delivered impersonates the recipient internally
assert_daemon_message_delivered test_impl test_coord "billing" || { echo "FAIL: message_delivered"; exit 1; }
! assert_daemon_message_delivered test_impl test_coord "no-such-pattern" || { echo "FAIL: message_delivered neg pattern"; exit 1; }

# agent_session_active
assert_daemon_agent_session_active test_coord || { echo "FAIL: agent_session_active"; exit 1; }

echo "PASS"
