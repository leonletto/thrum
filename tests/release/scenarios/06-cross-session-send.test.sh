#!/usr/bin/env bash
# Scenario: cross-session-send (migrates full_test_plan.md § 8.2)
#
# Verifies that the coordinator can send a thrum message addressed to a
# DIFFERENT agent registered in a DIFFERENT pane and tmux session. A
# regression here means the multi-agent send path is broken — agents can
# no longer talk to each other across sessions.
#
# Fixture mutation: writes one user message to the daemon's inbox addressed
# to @test_implementer. Read-back happens in scenario 07. The message body
# carries a unique RUNID-anchored substring so scenario 07 can match it
# without colliding with any pre-existing inbox content (impl pane has
# none anyway, but the marker keeps the chain clear under future changes).

SID="06-cross-session-send"

# Substring is RUNID-anchored so 07 can match exactly this message even
# if other senders/messages enter the daemon between scenarios.
CROSS_SEND_MARKER="cross-session-test-${RUNID}"
# Export for scenario 07's matcher.
export CROSS_SEND_MARKER

# Send from coord pane to @test_implementer. --json so the response is
# machine-parsable; we assert on the message_id substring which only
# appears in the success-path JSON envelope (`"message_id": "msg_..."`).
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum send 'Cross-session test from coordinator (${CROSS_SEND_MARKER})' --to @test_implementer --json" \
    '"message_id": "msg_' 60; then
  emit_pass "$SID" "coord-send-confirmed"
else
  emit_fail "$SID" "coord-send-confirmed" \
    'thrum send --json output containing "message_id": "msg_' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
