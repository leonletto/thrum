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
# remains valid whether or not claude has auto-marked the message read.
#
# We exercise the actual `thrum reply` RPC path (NOT `thrum send`) to
# match the original markdown § 8.3's "reply to coordinator" semantics
# and exercise the daemon's reply-threading code (distinct from message
# send). Race against claude's autonomous mark-read is collapsed by
# extracting msg_id and invoking `thrum reply` inside the SAME bash
# subshell — single composite command, no inter-keystroke window in
# which claude can change inbox state between the lookup and the reply.
#
# Fixture mutation: writes a reply-direction message back to
# @test_coordinator_main carrying $CROSS_REPLY_MARKER.

SID="07-cross-session-receive-reply"

# Define + export CROSS_REPLY_MARKER unconditionally at the top, BEFORE
# any early-return path. This way, if assertion 1 fails and we return
# early, scenario 08's precondition guard still sees the marker set —
# 08 will fail its main assertion (correctly), but the cascade of a
# misleading `marker-precondition` FAIL on top of the real root cause
# goes away.
CROSS_REPLY_MARKER="cross-session-reply-${RUNID}"
export CROSS_REPLY_MARKER

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

# Assertion 2: impl uses `thrum reply` (the actual reply RPC) against the
# scenario-06 message_id to thread a response back to coord. The msg_id
# extraction (matching by CROSS_SEND_MARKER, not by .messages[0]) and
# the `thrum reply` invocation happen in a SINGLE bash subshell — by
# the time the inner pipeline runs, claude can't have marked-read the
# message in a way that affects this command, because `thrum inbox
# --json` returns all messages regardless of read state.
REPLY_CMD="MSG_ID=\$(thrum inbox --json | jq -r '[.messages[] | select(.body.content | contains(\"${CROSS_SEND_MARKER}\"))] | .[0].message_id') && thrum reply \"\$MSG_ID\" 'Received cross-session message (${CROSS_REPLY_MARKER})' --format plain --json"

if send_bash_and_wait "$IMPL_PANE" "$IMPL_REPO" \
    "$REPLY_CMD" \
    '"message_id": "msg_' 60; then
  emit_pass "$SID" "impl-reply-confirmed"
else
  emit_fail "$SID" "impl-reply-confirmed" \
    'thrum reply --json output containing "message_id": "msg_' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
