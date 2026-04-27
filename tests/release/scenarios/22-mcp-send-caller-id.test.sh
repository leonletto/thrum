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

# Step 1: send from coord pane.
send_command "$COORD_PANE" "! thrum send 'CallerID test (${MARKER})' --to @test_implementer"

# Step 2: settle COORD before potentially using it in subsequent
# scenarios — the send response render is short.
wait_for_pane_idle "$COORD_PANE" 30

# Step 3: poll IMPL's inbox via tmux-exec for the message's
# agent_id. Write the raw JSON to a host-accessible file path
# inside the tmux-exec invocation, then jq it from the host —
# tmux-exec's `capture-pane` runs at the default 80-col width and
# wraps long JSON strings with literal \n that breaks jq. The
# tmux-exec→file→host-jq detour avoids the wrap. Brief poll
# because daemon delivery is fast (<1s in fixture).
out_file="$(mktemp -t kafm2-22.XXXXXX).json"
_check_caller_id() {
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
    bash -c "env THRUM_NAME=test_implementer thrum inbox --json > '${out_file}' 2>/dev/null" \
    >/dev/null 2>&1 || true
  if [ -s "$out_file" ]; then
    jq -r --arg m "$MARKER" \
      '[.messages[] | select(.body.content | contains($m))] | .[0].agent_id // ""' \
      < "$out_file" 2>/dev/null
  fi
}

elapsed=0
agent_id=""
while [ "$elapsed" -lt 30 ]; do
  agent_id="$(_check_caller_id)"
  if [ -n "$agent_id" ]; then
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

if [ "$agent_id" = "test_coordinator_main" ]; then
  emit_pass "$SID" "agent-id-matches-sender"
else
  emit_fail "$SID" "agent-id-matches-sender" \
    "marker-message .agent_id equals 'test_coordinator_main'" \
    "got: '${agent_id:-<empty>}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out_file"
