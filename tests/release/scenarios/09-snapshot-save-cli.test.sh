#!/usr/bin/env bash
# Scenario: snapshot-save-cli (migrates full_test_plan.md § 10G.1)
#
# Verifies that `thrum tmux snapshot save` invoked as a direct CLI command
# (not through `/thrum:restart` / `thrum tmux restart`) writes a snapshot
# file at .thrum/restart/<agent>.md and prints the canonical
# "Restart snapshot saved for <agent> (<N> lines)" success line.
#
# Deviation from markdown § 10G.1: the original spec uses the COORD agent.
# We use IMPL because (i) the previously-shipped scenario 03 also uses
# COORD for its restart-snapshot test and would already have left/mutated
# COORD's snapshot file, while scenario 02 leaves IMPL's restart dir
# clean post-restart (the file is consumed); (ii) the CLI contract under
# test is agent-agnostic — the choice of pane is implementation detail.
# The marker text + file-content assertions still cover every check the
# markdown spec lists.
#
# Per-scenario isolation: the snapshot file is consumed by scenario 11
# (snapshot-restore-cli), so by the end of the 09→10→11 chain the impl
# restart dir is clean. We pre-clean any leftover snapshot at the top of
# 09 so prior scenarios' state can't pollute our marker assertions.
#
# Fixture mutation: writes $IMPL_REPO/.thrum/restart/test_implementer.md.

SID="09-snapshot-save-cli"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"
SNAPSHOT_FILE="$REPO/.thrum/restart/test_implementer.md"
SAVE_REASON="cli-direct-test-${RUNID}"

# Pre-clean any leftover snapshot so the assertions below see ONLY the
# state we just produced. This also guards against scenario 02 leaving
# a residual snapshot if the restart machinery's consume-step regresses.
rm -f "$SNAPSHOT_FILE"

# Impl pane may be mid-render after preceding scenarios — settle first.
wait_for_pane_idle "$PANE" 60

# Assertion 1: `thrum tmux snapshot save` emits the success line.
if send_bash_and_wait "$PANE" "$REPO" \
    "thrum tmux snapshot save --reason '${SAVE_REASON}'" \
    "Restart snapshot saved for test_implementer" 60; then
  emit_pass "$SID" "save-success-line"
else
  emit_fail "$SID" "save-success-line" \
    'thrum tmux snapshot save stdout containing "Restart snapshot saved for test_implementer"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 2: snapshot file exists on disk. Driver-side filesystem
# check (no `!`-bash needed). Poll briefly because the file write may
# trail the success-line emission very slightly.
elapsed=0
while [ ! -s "$SNAPSHOT_FILE" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ -s "$SNAPSHOT_FILE" ]; then
  emit_pass "$SID" "save-file-present"
else
  emit_fail "$SID" "save-file-present" \
    "non-empty snapshot file at ${SNAPSHOT_FILE}" \
    "(file missing or empty after 10s)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 3: snapshot file content has the canonical agent-name header.
# Driver-side grep (cheap; no `!`-bash round-trip needed).
if head -1 "$SNAPSHOT_FILE" | grep -q '^# Restart Snapshot — test_implementer'; then
  emit_pass "$SID" "save-header"
else
  got="$(head -1 "$SNAPSHOT_FILE")"
  emit_fail "$SID" "save-header" \
    '"# Restart Snapshot — test_implementer" as first line' \
    "${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 4: snapshot file content includes the reason marker we passed.
if grep -q "Reason:.*${SAVE_REASON}" "$SNAPSHOT_FILE"; then
  emit_pass "$SID" "save-reason-marker"
else
  emit_fail "$SID" "save-reason-marker" \
    "snapshot body containing 'Reason: ${SAVE_REASON}'" \
    "(reason marker not found in snapshot file)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Export the reason so scenario 11 can match it in restore stdout.
export SNAPSHOT_SAVE_REASON="$SAVE_REASON"
