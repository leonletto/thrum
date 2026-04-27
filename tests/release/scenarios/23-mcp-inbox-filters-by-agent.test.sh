#!/usr/bin/env bash
# Scenario: mcp-inbox-filters-by-agent (migrates full_test_plan.md § 5.2)
#
# Verifies that a message addressed to @test_implementer lands in the
# implementer's inbox AND does NOT leak into the coordinator's inbox.
# Routing-parity contract for MCP `check_messages` + CLI `thrum inbox`:
# both surfaces filter by caller identity at the daemon level, so a
# regression here (per-agent filtering broken) would mean inbox
# isolation collapsed for both surfaces simultaneously.
#
# Two assertions:
#   1. NEGATIVE — coord's inbox does NOT contain the marker. We assert
#      a count of 0 marker-matching messages (deterministic, robust
#      to other content scenario 22 may have left).
#   2. POSITIVE — impl's inbox DOES contain the marker. Same count
#      shape (≥ 1).
#
# Both queries use `thrum inbox --json` (not --unread) so they match
# regardless of whether claude has autonomously marked-read.
#
# Fixture mutation: writes one user message to @test_implementer.

SID="23-mcp-inbox-filters-by-agent"
MARKER="kafm2-23-filter-${RUNID}"

# Step 1: send from coord pane. Plain send_command — we don't gate
# on response; the recipient-side assertions verify the routed shape.
send_command "$COORD_PANE" "! thrum send 'For implementer only (${MARKER})' --to @test_implementer"

# Step 2: settle COORD before its inbox query — claude may still be
# rendering the send response.
wait_for_pane_idle "$COORD_PANE" 60

# Assertion 1: NEGATIVE — coord's inbox should have zero matches.
# Compose the jq inside a bash subshell that emits "VERIFIED_NO_LEAK"
# on count==0 and "FAILED_LEAK" otherwise. assert via marker substring
# in bash-stdout.
NEG_QUERY="N=\$(thrum inbox --json | jq -r '[.messages[] | select(.body.content | contains(\"${MARKER}\"))] | length'); if [ \"\$N\" = \"0\" ]; then echo VERIFIED_NO_LEAK_${RUNID}; else echo FAILED_LEAK_\$N; fi"

if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "$NEG_QUERY" \
    "VERIFIED_NO_LEAK_${RUNID}" 60; then
  emit_pass "$SID" "coord-inbox-no-leak"
else
  emit_fail "$SID" "coord-inbox-no-leak" \
    "coord inbox contains 0 messages matching '${MARKER}' (no cross-routing leak)" \
    "(timeout or marker leaked into coord inbox)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: POSITIVE — impl's inbox should have ≥ 1 match.
# Drive the impl-side check OUT OF PANE via tmux-exec rather than
# `! thrum inbox` on the IMPL pane. Reason: the inbound nudge from
# step 1 wakes claude on IMPL into autonomous-handling mode (read +
# reply + mark-read), and that flurry of Bash tool calls keeps the
# pane non-idle for 30-90s. send_bash_and_wait's keystroke-time
# bash-mode gate races with that handling and intermittently misses
# (observed flake: claude lands a Bash call mid-render and `!` gets
# typed as a literal char). Out-of-pane tmux-exec sidesteps the
# race entirely — the daemon's inbox state is the authoritative
# truth, and reading it doesn't depend on the recipient pane being
# idle. Polls in case of brief delivery latency.
# Write raw JSON to a host-accessible file inside the tmux-exec
# invocation, then jq it from the host. tmux-exec's capture-pane
# runs at the default 80-col width and wraps long JSON strings
# with literal \n that breaks jq parsing.
out_file="$(mktemp -t kafm2-23.XXXXXX).json"
_check_impl_inbox() {
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
delivered=false
while [ "$elapsed" -lt 30 ]; do
  N=$(_check_impl_inbox || echo 0)
  if [ "${N:-0}" -ge 1 ]; then
    delivered=true
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

if $delivered; then
  emit_pass "$SID" "impl-inbox-delivered"
else
  emit_fail "$SID" "impl-inbox-delivered" \
    "impl inbox contains ≥ 1 message matching '${MARKER}' (within 30s)" \
    "(timeout or marker not delivered to impl inbox)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out_file"
