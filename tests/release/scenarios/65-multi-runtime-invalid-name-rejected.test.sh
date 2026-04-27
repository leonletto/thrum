#!/usr/bin/env bash
# Scenario: multi-runtime-invalid-name-rejected (migrates full_test_plan.md § 10C.4)
#
# Pins the runtime-name validation contract: a launch with a runtime
# name containing characters outside `[a-zA-Z0-9_-]+` (e.g. spaces, a
# command-injection-like "rm -rf") must be rejected with non-zero
# exit AND an error message containing "invalid". The validation
# guard sits between the CLI parser and the daemon's runtime
# resolution, and a regression here would mean a path-traversal-shape
# runtime name reaches downstream exec code.
#
# Reuses the rt-scratch worktree from scenario 62 as --cwd for a
# scratch --no-agent session. Per-scenario session name keeps the
# rejection assertion isolated from other rt-scratch users (66, 68).
#
# Two assertions:
#   1. launch-rejects-invalid-name — `thrum tmux launch
#      invalid-rt-test --runtime "rm -rf"` exits non-zero.
#   2. error-mentions-invalid — the launch output contains the
#      literal substring "invalid" (the daemon's rejection message
#      shape — markdown § 10C.4 documents the regex; the substring
#      check is robust to wording drift while still pinning the
#      negative-path branch).
#
# Depends on scenario 62 (rt-scratch fixture).

SID="65-multi-runtime-invalid-name-rejected"
INVALID_SESSION="invalid-rt-test"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_65() {

# Step 1: scratch --no-agent session in rt-scratch. --force tolerates
# leftover state from a partial prior run.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$INVALID_SESSION" \
    --cwd "$RT_WT" \
    --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-invalid-rt-test" \
      "thrum tmux create $INVALID_SESSION succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Step 2: attempt launch with a runtime name containing a space —
# fails the [a-zA-Z0-9_-]+ guard. Capture both stdout and stderr.
local reject_out reject_rc
reject_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$INVALID_SESSION" \
      --runtime "rm -rf" 2>&1
)
reject_rc=$?

# Cleanup the scratch session immediately. Don't leak it past this
# scenario — 66 and 68 use their own session names but share the
# rt-scratch worktree.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux kill "$INVALID_SESSION" >/dev/null 2>&1 || true

if [ "$reject_rc" -ne 0 ]; then
  emit_pass "$SID" "launch-rejects-invalid-name"
else
  emit_fail "$SID" "launch-rejects-invalid-name" \
    "thrum tmux launch with invalid --runtime exits non-zero" \
    "exit 0; output: $(printf '%s' "$reject_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# `grep -qi` covers both "Invalid runtime" and "invalid runtime name"
# wording variants the daemon might emit. The substring is the
# stable contract; exact wording is internal.
if printf '%s' "$reject_out" | grep -qi "invalid"; then
  emit_pass "$SID" "error-mentions-invalid"
else
  emit_fail "$SID" "error-mentions-invalid" \
    "rejection error output contains 'invalid' (case-insensitive)" \
    "$(printf '%s' "$reject_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_65

_run_scenario_65
