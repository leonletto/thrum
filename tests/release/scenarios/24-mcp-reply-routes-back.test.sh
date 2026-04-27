#!/usr/bin/env bash
# Scenario: mcp-reply-routes-back (migrates full_test_plan.md § 5.3)
#
# Verifies that `thrum reply <msg_id> "..."` routes the reply back
# to the original sender's inbox. Routing-parity contract for MCP
# `thrum_reply` + CLI `thrum reply`: both surfaces translate to the
# same daemon RPC, so a regression here breaks reply threading for
# both simultaneously.
#
# Distinct from scenarios 06-08: those exercise reply routing as
# the back-half of a multi-scenario chain (06 sends, 07 replies,
# 08 verifies). This scenario isolates the contract — original
# send + reply + verify-at-original-sender all in ONE scenario,
# with RUNID-anchored markers that don't share state with 06-08.
# Lets a § 5.3 regression be attributed cleanly without
# cross-scenario debugging.
#
# Test approach (all out-of-pane for the receiver side, see
# scenario 22 for the rationale on out-of-pane reads): COORD sends
# the original via its pane, IMPL replies via tmux-exec
# (msg_id extract + reply in one composite bash command), COORD
# inbox is polled via tmux-exec. The two pane-driven sends are
# kept on COORD because COORD is the stable driver surface; the
# recipient-side reads run via tmux-exec to avoid claude
# autonomous-handling races on the recipient panes.
#
# Fixture mutation: writes original (coord→impl) and reply
# (impl→coord). Doesn't depend on 22/23's markers but follows
# them in run order.

SID="24-mcp-reply-routes-back"
ORIG_MARKER="kafm2-24-orig-${RUNID}"
REPLY_MARKER="kafm2-24-reply-${RUNID}"

# Step 1: COORD sends original to IMPL.
send_command "$COORD_PANE" "! thrum send 'Original message (${ORIG_MARKER})' --to @test_implementer"
wait_for_pane_idle "$COORD_PANE" 30

# Step 2: IMPL extracts msg_id + replies via a single tmux-exec
# composite. Both inbox JSON and reply output go to host-
# accessible files inside the inner pane (tmux-exec capture-pane
# wraps lines at 80 cols and breaks JSON parsing — see scenarios
# 22/23 rationale). The composite reads inbox, extracts msg_id
# with jq INSIDE the pane (where it sees an unwrapped pipe), and
# writes the reply's JSON output to a separate file we jq from
# the host.
inbox_file="$(mktemp -t kafm2-24-inbox.XXXXXX).json"
reply_file="$(mktemp -t kafm2-24-reply.XXXXXX).json"
_impl_reply() {
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
    bash -c "
      env THRUM_NAME=test_implementer thrum inbox --json > '${inbox_file}' 2>/dev/null
      MSG_ID=\$(jq -r --arg m '${ORIG_MARKER}' \
        '[.messages[] | select(.body.content | contains(\$m))] | .[0].message_id // \"\"' \
        < '${inbox_file}' 2>/dev/null)
      if [ -z \"\$MSG_ID\" ] || [ \"\$MSG_ID\" = 'null' ]; then
        echo MSG_ID_NOT_FOUND > '${reply_file}'
        exit 1
      fi
      env THRUM_NAME=test_implementer thrum reply \"\$MSG_ID\" 'Reply back (${REPLY_MARKER})' --json > '${reply_file}' 2>/dev/null
    " >/dev/null 2>&1 || true
  if [ -s "$reply_file" ]; then
    cat "$reply_file"
  fi
}

# Brief poll for the original to land in impl inbox before reply.
elapsed=0
reply_output=""
while [ "$elapsed" -lt 30 ]; do
  reply_output="$(_impl_reply)"
  if echo "$reply_output" | grep -q '"message_id": "msg_'; then
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

if echo "$reply_output" | grep -q '"message_id": "msg_'; then
  emit_pass "$SID" "impl-reply-confirmed"
else
  got="$(echo "$reply_output" | tr '\n' ' ' | head -c 200)"
  emit_fail "$SID" "impl-reply-confirmed" \
    'thrum reply --json output containing "message_id": "msg_"' \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$inbox_file" "$reply_file"
  return 0
fi

# Step 3: poll COORD's inbox for the reply marker via tmux-exec.
coord_inbox_file="$(mktemp -t kafm2-24-coord.XXXXXX).json"
_check_coord_inbox() {
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
    bash -c "env THRUM_NAME=test_coordinator_main thrum inbox --json > '${coord_inbox_file}' 2>/dev/null" \
    >/dev/null 2>&1 || true
  if [ -s "$coord_inbox_file" ]; then
    jq -r --arg m "$REPLY_MARKER" \
      '[.messages[] | select(.body.content | contains($m))] | length' \
      < "$coord_inbox_file" 2>/dev/null
  fi
}

elapsed=0
delivered=false
while [ "$elapsed" -lt 30 ]; do
  N=$(_check_coord_inbox || echo 0)
  if [ "${N:-0}" -ge 1 ]; then
    delivered=true
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

if $delivered; then
  emit_pass "$SID" "coord-receives-reply"
else
  emit_fail "$SID" "coord-receives-reply" \
    "coord inbox contains reply marker '${REPLY_MARKER}' (within 30s)" \
    "(timeout — reply did not route back to coord)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$inbox_file" "$reply_file" "$coord_inbox_file"
