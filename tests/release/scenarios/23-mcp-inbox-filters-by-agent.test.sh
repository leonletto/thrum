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

# Step 1: send from coord pane. Use send_bash_and_wait (gates on
# "Message sent" bash-stdout) instead of plain send_command. Reason:
# under heavy gate load, send_command's pane-idle-gated typing can
# race with a subsequent send_command's `!` prefix — the next `!`
# keystroke splices into the trailing position of this command's
# body BEFORE this command's Enter has fully submitted, producing a
# corrupted `--to @test_implementer!` arg that the daemon rejects
# as "unknown recipient" (root cause of v0.10.6 RC1 gate
# reltest-48589 failure; see thrum-rbp6 for the underlying
# send_command race). Gating on the actual send-confirmation
# bash-stdout forces this command to fully complete before any
# subsequent operation can interleave its keystrokes.
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum send 'For implementer only (${MARKER})' --to @test_implementer" \
    "Message sent" 30; then
  emit_fail "$SID" "impl-inbox-delivered" \
    "thrum send completes with 'Message sent' confirmation within 30s" \
    "(send command may have raced — see thrum-rbp6)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

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
# Drive the impl-side check OUT OF PANE via assert_inbox_contains
# rather than `! thrum inbox` on the IMPL pane. Reason: the inbound
# nudge from step 1 wakes claude on IMPL into autonomous-handling
# mode (read + reply + mark-read), and that flurry of Bash tool
# calls keeps the pane non-idle for 30-90s. send_bash_and_wait's
# keystroke-time bash-mode gate races with that handling and
# intermittently misses (observed flake: claude lands a Bash call
# mid-render and `!` gets typed as a literal char). Out-of-pane
# tmux-exec sidesteps the race entirely — the daemon's inbox state
# is the authoritative truth, and reading it doesn't depend on the
# recipient pane being idle.
#
# assert_inbox_contains handles the mktemp/jq/poll boilerplate AND
# the `.messages[]?` null-safety against hints-only daemon responses
# that the inline version was vulnerable to.
if assert_inbox_contains test_implementer "$IMPL_REPO" "$MARKER" 30; then
  emit_pass "$SID" "impl-inbox-delivered"
else
  emit_fail "$SID" "impl-inbox-delivered" \
    "impl inbox contains ≥ 1 message matching '${MARKER}' (within 30s)" \
    "(timeout or marker not delivered to impl inbox)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
