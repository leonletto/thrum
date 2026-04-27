#!/usr/bin/env bash
# Scenario: worktree-setup-name-validation (migrates full_test_plan.md § 10B.4)
#
# Verifies that `thrum worktree create` rejects names containing path
# traversal sequences (`..`) or path separators (`/`). Regression here
# means a malicious or sloppy `--name` could create worktrees outside
# the intended base_path — escaping the project's worktree boundary.
#
# Two probes:
#   - "../../../tmp/evil"  — explicit ancestor traversal
#   - "path/with/slash"    — forward slash in name
#
# Both must exit non-zero with an error message naming the validation
# rule (the markdown spec says "invalid worktree name … must not
# contain /, \, or parent references").
#
# Drives via the COORD pane like scenario 13 — registered-agent caller,
# matches setup-repo.sh's existing pattern. No worktree should land on
# disk; nothing to teardown.

SID="16-worktree-setup-name-validation"

_run_scenario_16() {

wait_for_pane_idle "$COORD_PANE" 60

# Probe 1: parent-traversal name. Append `; echo "exit: $?"` so the
# exit code lands inside the same <bash-stdout> envelope. Match a
# substring that's specific to the validation error.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../../tmp/evil' 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "traversal-rejected-non-zero"
else
  emit_fail "$SID" "traversal-rejected-non-zero" \
    'thrum worktree create with "../../../tmp/evil" exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Probe 1b: error message mentions name validity. Markdown § 10B.4
# specifies "invalid worktree name … must not contain /, \, or
# parent references". Match the prefix "invalid" + "name" — the
# minimal stable substring that pins the validation error path
# without coupling to wording polish.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create '../../../tmp/evil' 2>&1" \
    "invalid" 60; then
  emit_pass "$SID" "traversal-error-message"
else
  emit_fail "$SID" "traversal-error-message" \
    'thrum worktree create error output containing "invalid"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Probe 2: forward-slash name. Same exit-1 + invalid-message pair.
wait_for_pane_idle "$COORD_PANE" 30

if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create 'path/with/slash' 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "slash-rejected-non-zero"
else
  emit_fail "$SID" "slash-rejected-non-zero" \
    'thrum worktree create with "path/with/slash" exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_16

_run_scenario_16
