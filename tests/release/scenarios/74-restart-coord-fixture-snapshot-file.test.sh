#!/usr/bin/env bash
# Scenario: restart-coord-fixture-snapshot-file (migrates full_test_plan.md § 10.5)
#
# Asserts the snapshot file scenario 73 staged has the canonical
# "# Restart Snapshot — <agent>" header. Scenario 73 captured the
# file's contents to a /tmp path before the new claude session's
# auto-prime could consume the original — we read the staged copy here
# instead of $KAFM6_S1_SNAPSHOT_FILE (which by now is consumed and
# gone).
#
# One assertion: staged snapshot file's first line matches the
# expected agent-name header. Same shape as scenario 09's
# `save-header` assertion but against the daemon-side restart-RPC
# write path (not the direct CLI write path).
#
# Depends on scenario 73 (which exports KAFM6_S1_SNAPSHOT_STAGING).

SID="74-restart-coord-fixture-snapshot-file"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_SNAPSHOT_STAGING:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 73 staging path (KAFM6_S1_SNAPSHOT_STAGING) exported" \
    "(missing — scenarios 70 + 73 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

if [ ! -s "$KAFM6_S1_SNAPSHOT_STAGING" ]; then
  emit_fail "$SID" "snapshot-header" \
    "non-empty staged snapshot at ${KAFM6_S1_SNAPSHOT_STAGING}" \
    "(file missing or empty — scenario 73 staging may have failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

if head -1 "$KAFM6_S1_SNAPSHOT_STAGING" | grep -q "^# Restart Snapshot — ${KAFM6_S1_AGENT}"; then
  emit_pass "$SID" "snapshot-header"
else
  got=$(head -1 "$KAFM6_S1_SNAPSHOT_STAGING")
  emit_fail "$SID" "snapshot-header" \
    "\"# Restart Snapshot — ${KAFM6_S1_AGENT}\" as first line of staged snapshot" \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
