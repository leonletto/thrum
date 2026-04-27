#!/usr/bin/env bash
# Scenario: worktree-create-path-traversal (migrates full_test_plan.md § 10D.7)
#
# Verifies that `thrum worktree create` rejects names containing
# parent-traversal sequences (`..`). § 10D.7 specifically pins the
# `../../../tmp/evil` and `../../escape` probes as the orchestrator
# infrastructure surface — distinct from § 10B.4 (scenario 16) which
# combined `../../../tmp/evil` with `path/with/slash` to cover the
# slash-separator branch. The two scenarios are intentionally
# overlapping on probe 1 but split on probe 2 so a regression that
# weakens only one branch of the validator surfaces against the
# scenario that pins that branch.
#
# Driven from COORD pane via `!`-bash. No worktree should land on
# disk; nothing to teardown.

SID="87-worktree-create-path-traversal"

wait_for_pane_idle "$COORD_PANE" 60

# Probe 1: explicit ancestor traversal.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../../tmp/evil' 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "deep-traversal-rejected-non-zero"
else
  emit_fail "$SID" "deep-traversal-rejected-non-zero" \
    'thrum worktree create with "../../../tmp/evil" exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

wait_for_pane_idle "$COORD_PANE" 30
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../../tmp/evil' 2>&1" \
    "invalid" 60; then
  emit_pass "$SID" "deep-traversal-error-message"
else
  emit_fail "$SID" "deep-traversal-error-message" \
    'thrum worktree create error output containing "invalid"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Probe 2: shallower traversal (`../../escape`). § 10D.7's distinct
# probe — pins that the validator catches relative-traversal at any
# depth, not just the deep-path form scenario 16 covers.
wait_for_pane_idle "$COORD_PANE" 30
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../escape' 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "shallow-traversal-rejected-non-zero"
else
  emit_fail "$SID" "shallow-traversal-rejected-non-zero" \
    'thrum worktree create with "../../escape" exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

wait_for_pane_idle "$COORD_PANE" 30
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../escape' 2>&1" \
    "invalid" 60; then
  emit_pass "$SID" "shallow-traversal-error-message"
else
  emit_fail "$SID" "shallow-traversal-error-message" \
    'thrum worktree create error output containing "invalid"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
