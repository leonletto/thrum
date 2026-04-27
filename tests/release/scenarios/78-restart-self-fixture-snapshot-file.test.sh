#!/usr/bin/env bash
# Scenario: restart-self-fixture-snapshot-file (migrates full_test_plan.md § 10.9)
#
# Pins the on-disk shape of the self-saved restart snapshot from
# scenario 77: file exists at .thrum/restart/<agent>.md, has the
# canonical "# Restart Snapshot — <agent>" header, and the body
# carries the reason marker scenario 77 supplied via --reason.
#
# Three assertions:
#   1. snapshot-file-present — non-empty file at the expected path.
#   2. snapshot-header — first line is "# Restart Snapshot — <agent>".
#   3. snapshot-reason-marker — body contains "Reason:.*<marker>".
#
# Same shape as scenario 09's three save-* assertions but against
# the self-restart sub-fixture (different agent + different worktree).
#
# Depends on scenario 77 (which exports KAFM6_S2_SNAPSHOT_FILE,
# KAFM6_S2_SAVE_REASON, KAFM6_S2_AGENT).

SID="78-restart-self-fixture-snapshot-file"

if [ -z "${KAFM6_S2_SNAPSHOT_FILE:-}" ] || [ -z "${KAFM6_S2_SAVE_REASON:-}" ] || [ -z "${KAFM6_S2_AGENT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 77 fixture identifiers exported" \
    "(missing — scenario 77 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll for the snapshot file in case the disk write trails the
# success-line emission (mirrors scenario 09's same poll).
elapsed=0
while [ ! -s "$KAFM6_S2_SNAPSHOT_FILE" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 1: snapshot file present.
if [ -s "$KAFM6_S2_SNAPSHOT_FILE" ]; then
  emit_pass "$SID" "snapshot-file-present"
else
  emit_fail "$SID" "snapshot-file-present" \
    "non-empty snapshot file at ${KAFM6_S2_SNAPSHOT_FILE}" \
    "(file missing or empty after 10s)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 2: canonical header.
if head -1 "$KAFM6_S2_SNAPSHOT_FILE" | grep -q "^# Restart Snapshot — ${KAFM6_S2_AGENT}"; then
  emit_pass "$SID" "snapshot-header"
else
  got=$(head -1 "$KAFM6_S2_SNAPSHOT_FILE")
  emit_fail "$SID" "snapshot-header" \
    "\"# Restart Snapshot — ${KAFM6_S2_AGENT}\" as first line" \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: reason marker in body.
if grep -q "Reason:.*${KAFM6_S2_SAVE_REASON}" "$KAFM6_S2_SNAPSHOT_FILE"; then
  emit_pass "$SID" "snapshot-reason-marker"
else
  emit_fail "$SID" "snapshot-reason-marker" \
    "snapshot body containing 'Reason: ${KAFM6_S2_SAVE_REASON}'" \
    "(reason marker not found in snapshot file)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
