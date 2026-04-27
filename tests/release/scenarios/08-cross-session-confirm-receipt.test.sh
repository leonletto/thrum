#!/usr/bin/env bash
# Scenario: cross-session-confirm-receipt (migrates full_test_plan.md § 8.4)
#
# Verifies that the coordinator sees the implementer's reply from scenario
# 07. Closes the bidirectional loop: 06 sent coord→impl, 07 received +
# replied impl→coord, 08 confirms coord→back. A regression here means
# impl→coord delivery is broken even though coord→impl works.
#
# Depends on scenario 07's reply: $CROSS_REPLY_MARKER must be set and the
# reply must already be queued in the daemon for @test_coordinator_main.
#
# Important: like scenario 07, the coord pane runs a real registered
# claude agent which may autonomously read inbound messages before this
# scenario's `!`-prefix query lands. We use `thrum inbox --json` (not
# --unread) so the assertion is valid regardless of read state.
#
# Read-only: no fixture mutation.

SID="08-cross-session-confirm-receipt"

if [ -z "${CROSS_REPLY_MARKER:-}" ]; then
  emit_fail "$SID" "marker-precondition" \
    "CROSS_REPLY_MARKER set by scenario 07" \
    "(empty — scenario 07 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Coord pane is likely mid-render: scenario 07's reply arrived as a
# daemon-nudged inbound message and claude in coord pane has begun
# autonomously processing it (read inbox, compose reply, etc). If we
# fire `!` while that render is in flight, send_command's default 10s
# pane-idle gate can return on timeout before bash-prefix mode engages,
# causing the keystroke to be typed as literal input instead of
# triggering the `<bash-stdout>` envelope. Same precedent + reasoning
# as scenario 03's pre-assertion `wait_for_pane_idle 60`.
wait_for_pane_idle "$COORD_PANE" 60

# coord pane's inbox should now contain the implementer's reply,
# identifiable by the RUNID-anchored reply marker.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum inbox --json" \
    "$CROSS_REPLY_MARKER" 60; then
  emit_pass "$SID" "coord-inbox-shows-reply"
else
  emit_fail "$SID" "coord-inbox-shows-reply" \
    "thrum inbox --json output containing the marker '${CROSS_REPLY_MARKER}'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
