#!/usr/bin/env bash
# Scenario: snapshot-check-cli (migrates full_test_plan.md § 10G.2)
#
# Verifies that `thrum tmux snapshot check` returns exit 0 when a snapshot
# file is present for the calling agent. This is the contract that
# /thrum:restart depends on to detect whether a fresh session should
# auto-load a Resume Plan.
#
# Depends on scenario 09's snapshot file at
# $IMPL_REPO/.thrum/restart/test_implementer.md.
#
# Read-only: no fixture mutation.

SID="10-snapshot-check-cli"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"
SNAPSHOT_FILE="$REPO/.thrum/restart/test_implementer.md"

# Driver-side precondition: scenario 09 must have left a snapshot file.
# If 09 was skipped or failed, surface that as a precondition error
# instead of letting check report exit-1 and giving misleading
# "snapshot detection broken" output.
if [ ! -s "$SNAPSHOT_FILE" ]; then
  emit_fail "$SID" "snapshot-precondition" \
    "non-empty snapshot file at ${SNAPSHOT_FILE} (left by scenario 09)" \
    "(file missing or empty)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

wait_for_pane_idle "$PANE" 60

# Assertion: `thrum tmux snapshot check` exits 0 when a snapshot exists.
# Append `; echo "exit: $?"` to the bash command so the exit code lands
# inside the <bash-stdout> envelope — the same trick the markdown spec
# uses ("echo exit: $?"). Match on the literal "exit: 0" string.
if send_bash_and_wait "$PANE" "$REPO" \
    'thrum tmux snapshot check; echo "exit: $?"' \
    "exit: 0" 60; then
  emit_pass "$SID" "check-exit-zero-when-present"
else
  emit_fail "$SID" "check-exit-zero-when-present" \
    'thrum tmux snapshot check stdout containing "exit: 0"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
