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

# Settle BOTH panes BEFORE firing wait. Critical ordering: previously
# COORD was settled BETWEEN firing wait and broadcasting, but
# wait_for_pane_idle can take up to 60s if COORD's claude is rendering
# a long response from a prior scenario (e.g. scen 24's autonomous
# inbox handling can leave COORD busy for 30+s). When that gap ate
# into the wait's 12s --timeout budget, the broadcast fired AFTER the
# wait had already timed out — observed in v0.10.6 RC1 gate where
# IMPL pane shows `NO_MESSAGES_TIMEOUT` followed by the broadcast
# arrival nudge. Settling both panes up-front decouples the wait's
# timer from COORD's rendering state.
wait_for_pane_idle "$IMPL_PANE" 60
wait_for_pane_idle "$COORD_PANE" 60

# Pre-clear IMPL's unread queue out-of-pane so `thrum wait`'s only
# viable trigger is OUR broadcast. Without this, a slowly-delivered
# message from an earlier scenario (22-24) could land in IMPL's
# subscription window right after wait subscribes and unblock it
# before our broadcast arrives — primary assertion `"status":
# "received"` would then false-positive (wait DID receive, just on
# the wrong message). The secondary inbox-marker check below
# catches that, but tightening primary too keeps the failure mode
# attributable.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum message read --all \
  >/dev/null 2>&1 || true

# Step 1: fire wait on IMPL (fire-and-forget — bash subshell blocks
# but driver returns immediately after keystrokes land). send_command's
# `!` branch handles the keystroke split; --json so the completion
# envelope is parseable from the JSONL bash-stdout entry.
send_command "$IMPL_PANE" "! thrum wait --timeout 12s --json"

# Step 2: brief settle so the wait actually subscribes to the daemon
# push channel BEFORE the broadcast fires. Without this gap, the
# broadcast can land before the subscriber is registered.
sleep 2

# Step 3: broadcast from COORD immediately. COORD was already settled
# above, so this send fires within ~2-3s of the wait subscription,
# well inside the 12s --timeout window.
send_command "$COORD_PANE" "! thrum send 'Broadcast for waiter (${MARKER})' --to @everyone"

# Step 4: poll IMPL's JSONL for `thrum wait`'s success-shape output.
# wait's --json output on a received message is a fixed envelope
# shape — verified at cmd/thrum/main.go:1461-1465:
#   {"status":"received",
#    "action":"ACTION REQUIRED: You have unread messages..."}
# It does NOT include the message body or any RUNID marker. So
# the deterministic wait-completion signal is the literal substring
# `"status": "received"`. Strengthening the primary filter to include
# the RUNID marker is not possible at this layer — the marker
# isn't in wait's stdout. Instead, primary tightness comes from the
# pre-wait inbox clear above (wait can only unblock on a message
# arriving AFTER subscription) plus the secondary inbox-marker
# check (proves OUR broadcast specifically delivered).
#
# Generous timeout — the wait's own --timeout 12s plus a few seconds
# of post-arrival render slack.
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
