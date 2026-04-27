#!/usr/bin/env bash
# Scenario: cross-session-receive-reply (migrates full_test_plan.md § 8.3)
#
# Verifies that the implementer pane sees the message scenario 06 sent to it
# AND can send a response back to the coordinator. The response carries a
# distinct RUNID-anchored marker so scenario 08 can prove it landed in
# coord's inbox.
#
# Depends on scenario 06's daemon-side state: scenario 06 wrote a message
# addressed to @test_implementer with body containing $CROSS_SEND_MARKER.
# Without scenario 06, the inbox-content assertion below will time out.
#
# Important: the impl pane runs a real registered claude agent which may
# autonomously process inbound thrum messages (read inbox + reply +
# mark-read) before this scenario's `!`-prefix query lands. We therefore
# use `thrum inbox --json` (all messages, NOT --unread) so the assertion
# remains valid whether or not claude has auto-marked the message read,
# and we send a NEW outbound message via `thrum send` rather than
# `thrum reply` so we don't depend on extracting a msg_id from an inbox
# that may already have been emptied by autonomous handling.
#
# Fixture mutation: writes a reply-direction message back to
# @test_coordinator_main carrying $CROSS_REPLY_MARKER.

SID="07-cross-session-receive-reply"

if [ -z "${CROSS_SEND_MARKER:-}" ]; then
  emit_fail "$SID" "marker-precondition" \
    "CROSS_SEND_MARKER set by scenario 06" \
    "(empty — scenario 06 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Impl pane received scenario 06's send via daemon nudge and claude is
# likely mid-render handling it autonomously. Wait for the pane to settle
# before firing `!` so bash-prefix mode engages cleanly. Same precedent
# as scenario 03's pre-assertion `wait_for_pane_idle 60`.
wait_for_pane_idle "$IMPL_PANE" 60

# Assertion 1: impl pane's inbox contains the cross-session message.
# Use --json (not --unread) so we match the message regardless of whether
# claude has autonomously already read it. The unique RUNID-anchored
# marker prevents false-positives on pre-existing inbox content.
if send_bash_and_wait "$IMPL_PANE" "$IMPL_REPO" \
    "thrum inbox --json" \
    "$CROSS_SEND_MARKER" 60; then
  emit_pass "$SID" "impl-inbox-shows-message"
else
  emit_fail "$SID" "impl-inbox-shows-message" \
    "thrum inbox --json output containing the marker '${CROSS_SEND_MARKER}'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 2: impl can send a message back to the coordinator. We use
# `thrum send --to @test_coordinator_main` rather than `thrum reply
# <msg_id>` because msg_id extraction from impl's inbox would race
# claude's autonomous mark-as-read; `thrum send` is independent of inbox
# state. The semantic intent of full_test_plan.md § 8.3 ("implementer
# replies to coordinator") is preserved — the response message reaches
# the same recipient and carries a distinct marker for scenario 08 to
# match.
CROSS_REPLY_MARKER="cross-session-reply-${RUNID}"
export CROSS_REPLY_MARKER

if send_bash_and_wait "$IMPL_PANE" "$IMPL_REPO" \
    "thrum send 'Received cross-session message (${CROSS_REPLY_MARKER})' --to @test_coordinator_main --json" \
    '"message_id": "msg_' 60; then
  emit_pass "$SID" "impl-reply-confirmed"
else
  emit_fail "$SID" "impl-reply-confirmed" \
    'thrum send --json output containing "message_id": "msg_' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
