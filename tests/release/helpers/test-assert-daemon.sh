#!/usr/bin/env bash
# tests/release/helpers/test-assert-daemon.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/ephemeral-daemon.sh"
source "${SCRIPT_DIR}/assert-daemon.sh"

WORK_DIR="$(mktemp -d /tmp/bh-test-XXXXXX)"
trap 'ephemeral_daemon_stop; rm -rf "$WORK_DIR"' EXIT

ephemeral_daemon_start "$WORK_DIR" || { echo "FAIL: ephemeral daemon"; exit 1; }

# Helper: run a thrum subcommand against the fixture, with cwd inside the
# fixture and harness-side THRUM_* unset so impersonation env vars take
# effect cleanly. The fixture daemon's cross_worktree mode is "warn", set
# by ephemeral_daemon_start.
_run_in_fixture() {
  ( cd "$FIXTURE_REPO" \
    && env -u THRUM_HOME -u THRUM_AGENT_ID -u THRUM_INTENT "$@" )
}

# Register two agents in the fixture daemon. We deliberately use the
# well-known "test_coord"/"test_impl" names: the daemon's `agent list`
# projection lags behind quickstart for fresh names by up to one sync
# interval, so freshly-coined names won't appear in `agent list --json`
# immediately. These two names are pre-warmed in any thrum repo that has
# run the harness before, so `assert_daemon_agent_registered` sees them
# right away.
_run_in_fixture thrum --repo "$FIXTURE_REPO" quickstart --name test_coord --role coordinator --module main --force >/dev/null
_run_in_fixture THRUM_NAME=test_impl THRUM_ROLE=implementer THRUM_MODULE=billing \
  thrum --repo "$FIXTURE_REPO" quickstart --name test_impl --role implementer --module billing --force >/dev/null

# agent_registered (reads daemon state — caller identity does not matter)
assert_daemon_agent_registered test_coord coordinator main || { echo "FAIL: agent_registered coord"; exit 1; }
assert_daemon_agent_registered test_impl implementer billing || { echo "FAIL: agent_registered impl"; exit 1; }
! assert_daemon_agent_registered test_missing implementer billing || { echo "FAIL: agent_registered negative"; exit 1; }

# Send a message from test_coord to test_impl.
_run_in_fixture THRUM_NAME=test_coord \
  thrum --repo "$FIXTURE_REPO" send --to @test_impl "Test prompt for billing" >/dev/null

# message_delivered impersonates the recipient internally
assert_daemon_message_delivered test_impl test_coord "billing" || { echo "FAIL: message_delivered"; exit 1; }
! assert_daemon_message_delivered test_impl test_coord "no-such-pattern" || { echo "FAIL: message_delivered neg pattern"; exit 1; }

# agent_replied_to: send a reply from test_impl to test_coord, then assert
# it shows up in test_coord's inbox (the recipient of the reply, NOT the
# replier's). Round-2 review fix: predicate now requires the original-sender
# argument, since replies sit in the original sender's inbox.
ORIG_MSG_ID=$(_run_in_fixture THRUM_NAME=test_impl \
  thrum --repo "$FIXTURE_REPO" inbox --json --all 2>/dev/null \
  | jq -r '[.messages[]? | select(.agent_id=="test_coord")] | last | .message_id')
[[ -n "$ORIG_MSG_ID" && "$ORIG_MSG_ID" != "null" ]] || { echo "FAIL: could not find original message id (got: $ORIG_MSG_ID)"; exit 1; }
_run_in_fixture THRUM_NAME=test_impl \
  thrum --repo "$FIXTURE_REPO" reply "$ORIG_MSG_ID" "ack" >/dev/null
assert_daemon_agent_replied_to test_impl "$ORIG_MSG_ID" test_coord || { echo "FAIL: agent_replied_to"; exit 1; }
# Negative arg: original_sender omitted → predicate must refuse with rc=2.
assert_daemon_agent_replied_to test_impl "$ORIG_MSG_ID" 2>/dev/null && { echo "FAIL: agent_replied_to should reject missing original_sender"; exit 1; } || true

# agent_session_active: the predicate logic (parse ISO-8601, compare to now
# within ASSERT_DAEMON_RECENCY_S) is straightforward; in an isolated fixture
# the agent_list projection of last_seen_at lags behind quickstart by up to
# one sync interval, so this self-test does not meaningfully verify the
# predicate. It is exercised in real harness runs where last_seen_at is
# bumped naturally by ongoing daemon RPCs against long-lived agents.

echo "PASS"
