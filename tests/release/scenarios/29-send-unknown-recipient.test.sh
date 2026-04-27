#!/usr/bin/env bash
# Scenario: send-unknown-recipient (migrates full_test_plan.md § 4.2)
#
# Verifies that `thrum send "..." --to @ghost_agent` against an
# unregistered recipient is rejected with an error containing
# "unknown recipient" and a non-zero exit. Driven via tmux-exec
# (out-of-pane) so we get a clean process exit code — driving
# through a claude pane would surface the error inside a
# bash-stdout/bash-stderr tag pair without preserving exit code.
#
# Read-only at the fixture level (the send is rejected pre-delivery
# so no message is ever written).

SID="29-send-unknown-recipient"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

out_file="$(mktemp -t kafm1-29.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum send "Ghost test" --to @ghost_agent \
  > "$out_file" 2>&1
rc=$?

# Assertion 1: non-zero exit (the error must propagate as a hard
# failure, not be hidden behind exit 0).
if [ "$rc" -ne 0 ]; then
  emit_pass "$SID" "non-zero-exit"
else
  got="$(tr '\n' ' ' < "$out_file" | head -c 240)"
  emit_fail "$SID" "non-zero-exit" \
    "thrum send to @ghost_agent exits non-zero" \
    "rc=0; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: error message contains "unknown recipient".
if grep -qi "unknown recipient" "$out_file"; then
  emit_pass "$SID" "unknown-recipient-error"
else
  got="$(tr '\n' ' ' < "$out_file" | head -c 240)"
  emit_fail "$SID" "unknown-recipient-error" \
    "stderr/stdout containing 'unknown recipient'" \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$out_file"
