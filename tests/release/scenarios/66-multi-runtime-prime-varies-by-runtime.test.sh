#!/usr/bin/env bash
# Scenario: multi-runtime-prime-varies-by-runtime (migrates full_test_plan.md § 10C.5)
#
# Pins the contract that auto-prime is runtime-conditional: when
# `tmux launch` is called with --runtime shell, the daemon does NOT
# auto-type a `/thrum:prime` keystroke into the launched pane (shell
# has no slash-command runtime). The claude / opencode runtimes do
# auto-prime; shell explicitly skips it.
#
# A regression here would either send `/thrum:prime` into a bash
# shell (where it would error and pollute the pane) or skip
# auto-prime for runtimes that need it (silently breaking the
# session-briefing chain). The pane-capture assertion catches both
# directions for the shell case.
#
# Reuses the rt-scratch worktree from scenario 62 as --cwd.
#
# Single assertion:
#   1. no-auto-prime-for-shell — `thrum tmux capture <session>`
#      after a shell launch contains 0 occurrences of "thrum:prime"
#      OR "thrum-prime". Brief 3s settle gives the daemon's
#      HandleLaunch goroutine time to type any auto-prime sequence
#      before we capture.
#
# Depends on scenario 62 (rt-scratch fixture).

SID="66-multi-runtime-prime-varies-by-runtime"
PRIME_SESSION="prime-rt-test"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_66() {

# Step 1: scratch --no-agent session in rt-scratch.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux create "$PRIME_SESSION" \
    --cwd "$RT_WT" \
    --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-prime-rt-test" \
      "thrum tmux create $PRIME_SESSION succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Step 2: launch with --runtime shell. THRUM_NAME pin keeps audit
# uniform across the kafm.8 batch.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux launch "$PRIME_SESSION" \
    --runtime shell >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-launch-shell" \
      "thrum tmux launch $PRIME_SESSION --runtime shell succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    "$TE" exec --cwd "$COORD_REPO" --clean -- \
      env THRUM_NAME=test_coordinator_main thrum tmux kill "$PRIME_SESSION" >/dev/null 2>&1 || true
    return 0
  }

# Settle: HandleLaunch's keystroke goroutine fires async (claude
# runtimes wait 10s before typing /thrum:prime). For shell, the
# absence of any keystroke is what we verify — 3s is generous for
# the shell-branch decision to land + pane to settle.
sleep 3

# Step 3: capture pane content via the daemon-aware
# `thrum tmux capture` (works regardless of which tmux socket the
# managed session lives on; raw `tmux -L default capture-pane` would
# fail if the daemon used a non-default socket). 100 lines is enough
# to catch any in-band auto-prime keystroke trace.
local capture_out
capture_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux capture "$PRIME_SESSION" --lines 100 2>&1
)

# Cleanup the scratch session immediately.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux kill "$PRIME_SESSION" >/dev/null 2>&1 || true

# Negative assertion: count occurrences of "thrum:prime" OR
# "thrum-prime" — should be 0 for shell runtime. grep -cE returns
# the count; we check it's exactly 0.
local hits
hits=$(printf '%s' "$capture_out" | grep -cE "thrum:prime|thrum-prime" || true)
if [ "${hits:-0}" -eq 0 ]; then
  emit_pass "$SID" "no-auto-prime-for-shell"
else
  emit_fail "$SID" "no-auto-prime-for-shell" \
    "0 occurrences of 'thrum:prime' or 'thrum-prime' in pane capture" \
    "got ${hits} hits; capture: $(printf '%s' "$capture_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_66

_run_scenario_66
