#!/usr/bin/env bash
# Scenario: mcp-waiter-broadcast (migrates full_test_plan.md § 5.4)
#
# Verifies that `thrum wait` blocked in one pane unblocks when a
# `--to @everyone` broadcast arrives from another pane. Routing-parity
# contract for the MCP waiter tool + CLI `thrum wait`: both surfaces
# subscribe to the same daemon push channel, so a regression in
# broadcast fanout breaks both simultaneously.
#
# Test approach:
#   1. Fire `! thrum wait --timeout 12s --json` on IMPL — fire-and-
#      forget from the driver's perspective (send_command returns
#      after the keystrokes land; the bash subshell on IMPL then
#      blocks for up to 12s waiting for a message).
#   2. Brief settle so the wait has actually subscribed.
#   3. Send `! thrum send 'Broadcast (...)' --to @everyone` from COORD.
#   4. Poll IMPL's JSONL for a bash-stdout entry containing the
#      RUNID-anchored marker — that's `thrum wait`'s --json output
#      after it received the broadcast and exited 0.
#
# Why --json on the wait: the default output shape includes the
# message body inline, which already contains the marker; --json
# gives a more deterministic substring match (the marker appears as
# a quoted JSON string field).
#
# Timeout choice: --timeout 12s gives 2s of headroom for the broadcast
# to arrive after the 1s post-fire settle. Driver-side polling
# ceiling is wait_for_bash_stdout_contains' 30s default — generous
# margin since the entire wait+broadcast round-trip is bounded by
# the wait's own timeout.
#
# Fixture mutation: writes one @everyone broadcast.

SID="25-mcp-waiter-broadcast"
MARKER="kafm2-25-broadcast-${RUNID}"

# Settle IMPL pane (claude may be auto-processing scenario 24's
# leftover messages).
wait_for_pane_idle "$IMPL_PANE" 60

# Step 1: fire wait on IMPL (fire-and-forget — bash subshell blocks
# but driver returns immediately after keystrokes land). send_command's
# `!` branch handles the keystroke split; --json so the marker shows
# up cleanly in the eventual bash-stdout output.
send_command "$IMPL_PANE" "! thrum wait --timeout 12s --json"

# Step 2: brief settle so the wait actually subscribes to the daemon
# push channel BEFORE the broadcast fires. Without this gap, the
# broadcast can land before the subscriber is registered.
sleep 2

# Settle COORD pane separately.
wait_for_pane_idle "$COORD_PANE" 60

# Step 3: broadcast from COORD.
send_command "$COORD_PANE" "! thrum send 'Broadcast for waiter (${MARKER})' --to @everyone"

# Step 4: poll IMPL's JSONL for `thrum wait`'s success-shape output.
# wait's --json output on a received message is not the message body
# itself — it's a fixed envelope shape:
#   {"action":"ACTION REQUIRED: You have unread messages...",
#    "status":"received"}
# The marker we sent goes into the daemon's inbox (later visible to
# `thrum inbox`), not into wait's own stdout. So the deterministic
# wait-completion signal is the literal substring `"status":
# "received"`. Combined with the fact we just sent a uniquely-marked
# broadcast (which is the only message in flight in this fixture),
# this is sufficient evidence the wait unblocked because of OUR
# broadcast — no other senders are active during the test window.
#
# Generous timeout — the wait's own --timeout 12s plus a few seconds
# of post-arrival render slack. We assert the second sub-assertion
# (marker actually delivered to inbox) separately so a "wait
# unblocked but on the wrong message" regression would still surface.
if wait_for_bash_stdout_contains "$IMPL_REPO" '"status": "received"' 30 >/dev/null; then
  emit_pass "$SID" "wait-receives-broadcast"
else
  emit_fail "$SID" "wait-receives-broadcast" \
    'thrum wait bash-stdout containing "status": "received" within 30s' \
    "(no matching bash-stdout entry — broadcast may not have routed to waiter)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Sub-assertion 2: the broadcast actually landed in IMPL's inbox
# with OUR marker. Defends against a "wait unblocked on some
# unrelated message" false positive.
#
# Drive the inbox check OUT OF PANE via tmux-exec — same rationale
# as scenarios 22/23/24: claude on IMPL is in autonomous-handling
# mode after receiving the broadcast nudge, and a `!`-bash query
# during that flurry races the keystroke-time bash-mode gate. The
# daemon's inbox state is authoritative; reading it via tmux-exec
# is deterministic regardless of what claude is doing.
# Write JSON to a host-accessible file inside the inner pane to
# sidestep tmux-exec's 80-col capture-pane wrap mangling JSON
# (see scenarios 22/23/24 rationale).
out_file="$(mktemp -t kafm2-25.XXXXXX).json"
_check_impl_inbox_for_broadcast() {
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
    bash -c "env THRUM_NAME=test_implementer thrum inbox --json > '${out_file}' 2>/dev/null" \
    >/dev/null 2>&1 || true
  if [ -s "$out_file" ]; then
    jq -r --arg m "$MARKER" \
      '[.messages[] | select(.body.content | contains($m))] | length' \
      < "$out_file" 2>/dev/null
  fi
}

elapsed=0
broadcast_delivered=false
while [ "$elapsed" -lt 30 ]; do
  N=$(_check_impl_inbox_for_broadcast || echo 0)
  if [ "${N:-0}" -ge 1 ]; then
    broadcast_delivered=true
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

if $broadcast_delivered; then
  emit_pass "$SID" "broadcast-marker-in-inbox"
else
  emit_fail "$SID" "broadcast-marker-in-inbox" \
    "impl inbox contains ≥ 1 message matching broadcast marker '${MARKER}' (within 30s)" \
    "(timeout or marker not delivered to impl inbox)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out_file"
