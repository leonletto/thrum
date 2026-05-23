#!/usr/bin/env bash
# Scenario: mcp-send-caller-id (migrates full_test_plan.md § 5.1)
#
# Verifies that a `thrum send` from one registered pane to another
# stamps the caller's `agent_id` at the message root in the recipient's
# inbox JSON — non-empty, non-null, equal to the sender's identity.
# Routing-parity contract between MCP `thrum_send` + CLI `thrum send`:
# both translate to the same daemon path, so an empty/null agent_id
# at the root would mean callerID attribution broke for both
# surfaces simultaneously.
#
# Test approach:
#   1. COORD sends a marker-bearing message to @test_implementer
#      via the COORD pane (`! thrum send`).
#   2. The recipient-side inbox lookup runs OUT OF PANE via tmux-exec
#      (THRUM_NAME=test_implementer pinned). Out-of-pane is necessary
#      here because the inbound nudge wakes claude on IMPL into
#      autonomous-handling mode (read + reply + mark-read), and that
#      flurry of Bash tool calls keeps the pane non-idle for 30-90s.
#      A `!`-bash query against IMPL during that window races claude's
#      handling and intermittently misses bash mode. The daemon's
#      inbox state is the authoritative truth; reading it via
#      tmux-exec is deterministic regardless of what claude on IMPL
#      is doing.
#   3. Assert via `jq` that the marker-matched message's `.agent_id`
#      equals the literal sender identity. Stronger than "non-empty":
#      a regression where agent_id gets populated but with the WRONG
#      identity (e.g. recipient instead of sender) still fails.
#
# Fixture mutation: writes one user message addressed to
# @test_implementer.

SID="22-mcp-send-caller-id"
MARKER="kafm2-22-callerid-${RUNID}"

# Step 1: send from coord pane. Use send_bash_and_wait gated on
# the "Message sent" bash-stdout. Reason: raw send_command returns
# after typing keystrokes, with no guarantee that claude has
# fully processed the Enter / rendered the daemon's response.
# When the next scenario's send_command fires its `!` prefix
# while this command's input handling is still mid-cycle in
# claude's UI, the `!` can splice into the trailing position of
# this command's body — producing `--to @test_implementer!`
# which the daemon rejects as "unknown recipient" (thrum-rbp6
# keystroke race; observed in v0.10.6 RC1 gates reltest-48589
# and reltest-58187 corrupting scen 23's downstream send). The
# send_bash_and_wait gate forces this command to fully complete
# (success line landed in JSONL) before returning, closing the
# transition-race window into scen 23.
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum send 'CallerID test (${MARKER})' --to @test_implementer" \
    "Message sent" 30; then
  emit_fail "$SID" "agent-id-matches-sender" \
    "thrum send completes with 'Message sent' confirmation within 30s" \
    "(send command may have raced — see thrum-rbp6)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: settle COORD before potentially using it in subsequent
# scenarios — the send response render is short.
wait_for_pane_idle "$COORD_PANE" 30

# Step 3: poll IMPL's inbox until the marker arrives, then extract
# the .agent_id field from the matching message and assert it
# equals the sender's identity.
#
# Two-stage flow (since assert_inbox_contains is a presence
# predicate that doesn't return fields): first gate on delivery
# via the helper, then run a single jq pass to pull .agent_id
# from the (now-known-present) message.
if ! assert_inbox_contains test_implementer "$IMPL_REPO" "$MARKER" 30; then
  emit_fail "$SID" "agent-id-matches-sender" \
    "marker-message .agent_id equals 'test_coordinator_main'" \
    "(marker '${MARKER}' never delivered to impl inbox within 30s)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Marker is present — pull the field. Single read, no polling.
out_file="$(mktemp -t kafm2-22.XXXXXX)"
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
  bash -c "env THRUM_NAME=test_implementer thrum inbox --json > '${out_file}' 2>/dev/null" \
  >/dev/null 2>&1 || true
agent_id=""
if [ -s "$out_file" ]; then
  agent_id="$(jq -r --arg m "$MARKER" \
    '[.messages[]? | select(.body.content // "" | contains($m))] | .[0].agent_id // ""' \
    < "$out_file" 2>/dev/null)"
fi
rm -f "$out_file"

if [ "$agent_id" = "test_coordinator_main" ]; then
  emit_pass "$SID" "agent-id-matches-sender"
else
  emit_fail "$SID" "agent-id-matches-sender" \
    "marker-message .agent_id equals 'test_coordinator_main'" \
    "got: '${agent_id:-<empty>}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
