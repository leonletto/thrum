#!/usr/bin/env bash
# Scenario: snapshot-restore-cli (migrates full_test_plan.md § 10G.3)
#
# Verifies that `thrum tmux snapshot restore` (i) outputs the snapshot
# markdown content to stdout AND (ii) consumes (deletes) the snapshot
# file after read, so a subsequent `thrum tmux snapshot check` reports
# exit 1.
#
# Depends on scenario 09's snapshot file. Scenario 10 was non-mutating
# so the file is still present when we get here.
#
# Fixture mutation: removes $IMPL_REPO/.thrum/restart/test_implementer.md
# (the consume-on-restore behavior under test).

SID="11-snapshot-restore-cli"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"
SNAPSHOT_FILE="$REPO/.thrum/restart/test_implementer.md"

if [ -z "${SNAPSHOT_SAVE_REASON:-}" ]; then
  emit_fail "$SID" "marker-precondition" \
    "SNAPSHOT_SAVE_REASON exported by scenario 09" \
    "(empty — scenario 09 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

if [ ! -s "$SNAPSHOT_FILE" ]; then
  emit_fail "$SID" "snapshot-precondition" \
    "non-empty snapshot file at ${SNAPSHOT_FILE} (left by scenario 09)" \
    "(file missing or empty)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

wait_for_pane_idle "$PANE" 60

# Assertion 1: `thrum tmux snapshot restore` outputs the snapshot
# content. Match on the reason marker scenario 09 saved — proves
# we got OUR snapshot, not some other one.
if send_bash_and_wait "$PANE" "$REPO" \
    "thrum tmux snapshot restore" \
    "${SNAPSHOT_SAVE_REASON}" 60; then
  emit_pass "$SID" "restore-emits-content"
else
  emit_fail "$SID" "restore-emits-content" \
    "thrum tmux snapshot restore stdout containing reason marker '${SNAPSHOT_SAVE_REASON}'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 2: snapshot file consumed (deleted) after restore. Driver-
# side filesystem check; poll briefly because the unlink may trail
# stdout flushing very slightly.
elapsed=0
while [ -e "$SNAPSHOT_FILE" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -e "$SNAPSHOT_FILE" ]; then
  emit_pass "$SID" "restore-consumes-file"
else
  emit_fail "$SID" "restore-consumes-file" \
    "snapshot file removed after restore: ${SNAPSHOT_FILE}" \
    "(file still present after 10s)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: after consumption, `thrum tmux snapshot check` reports
# exit 1 (no snapshot). This is the inverse of scenario 10's exit-0
# assertion — together they pin both branches of the check contract.
wait_for_pane_idle "$PANE" 30

if send_bash_and_wait "$PANE" "$REPO" \
    'thrum tmux snapshot check; echo "exit: $?"' \
    "exit: 1" 60; then
  emit_pass "$SID" "check-exit-one-after-restore"
else
  emit_fail "$SID" "check-exit-one-after-restore" \
    'thrum tmux snapshot check stdout containing "exit: 1"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
