#!/usr/bin/env bash
# Scenario: multi-runtime-resolution-chain (migrates full_test_plan.md § 10C.3)
#
# Verifies tier 2 of the runtime resolution chain. The launch command
# should resolve runtime in this order:
#   1. --runtime flag (covered by scenario 62)
#   2. identity PreferredRuntime
#   3. config runtime.primary (covered by scenario 38)
#   4. default "claude" fallback
#
# This scenario exercises tier 2 specifically: with no --runtime
# flag, an identity whose preferred_runtime is "shell" should cause
# `tmux launch` to launch shell.
#
# Sub-fixture (au7k discipline, dispatch rule #3): run-level coord's
# identity must NOT be mutated to preferred_runtime=shell, since
# subsequent scenarios depend on the run-level fixture's default
# runtime resolution. Build the entire chain — git init, thrum init,
# quickstart --runtime shell, worktree create, tmux create+launch —
# inside a sub-fixture and stop the sub-daemon at scenario end.
#
# Single assertion:
#   1. launch-resolves-from-identity — `thrum tmux launch <session>`
#      with NO --runtime flag, called as the sub-fixture agent (whose
#      identity has preferred_runtime=shell), produces stdout that
#      contains the literal string "shell" (matches the daemon's
#      "Launched shell in session <name>" success line).
#
# Why stdout-text rather than `thrum tmux status --json`'s runtime
# field: the status row's runtime field is observed inconsistent
# under thrum-rfn4 (P3) — same issue the kafm.10 batch documented
# in scenario 45's "queue-test-session-alive" comment. The launch
# CLI's own success-line output is stable regardless; it's printed
# directly by the launch command after resolution, not derived from
# a re-read of the session row.
#
# rfn4 reference: same code path family as scenario 45's
# launch-time runtime stamping. Acceptance is "Launched shell"
# substring matches; the runtime-field shape on `thrum tmux status`
# is intentionally not asserted here.

SID="64-multi-runtime-resolution-chain"
SUB_REPO="$BASE/kafm8-64"
SUB_WT_BASE="$BASE/kafm8-64-wt"
SUB_AGENT="kafm8_64_coordinator"
SUB_WT_NAME="resolve-wt"
SUB_WT="$SUB_WT_BASE/kafm8-64/$SUB_WT_NAME"
RESOLVE_SESSION="resolve-test"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_64() {

# Sub-fixture build: git init.
mkdir -p "$SUB_REPO" "$SUB_WT_BASE"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-64@thrum.local" \
    && git config user.name "Release Tests 64" \
    && echo "# 64 sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Quickstart with --runtime shell sets identity.preferred_runtime=shell.
# That value is what tier 2 of the resolution chain reads.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum quickstart \
    --name "$SUB_AGENT" \
    --role coordinator \
    --module testing \
    --runtime shell \
    --intent "Testing runtime resolution chain" \
    --force >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart --runtime shell" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Patch worktrees config so worktree create lands under SUB_WT_BASE.
# thrum auto-appends the repo basename — SUB_REPO basename is
# "kafm8-64", so the actual worktree path is
# $SUB_WT_BASE/kafm8-64/$SUB_WT_NAME.
jq --arg bp "$SUB_WT_BASE/" \
  '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
  "$SUB_REPO/.thrum/config.json" > "$SUB_REPO/.thrum/config.json.tmp" \
  && mv "$SUB_REPO/.thrum/config.json.tmp" "$SUB_REPO/.thrum/config.json" \
  || { emit_fail "$SID" "subfixture-config-patch" "patch worktrees.base_path" "(failed)" \
       "scenarios/${SID}.test.sh:$LINENO"
       return 0; }

# Worktree create under sub-fixture identity. Must be a worktree (not
# the bare repo) for tmux create's --cwd guard.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum worktree create "$SUB_WT_NAME" \
    --branch "feature/${SUB_WT_NAME}" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-worktree-create" "thrum worktree create $SUB_WT_NAME" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

if [ ! -d "$SUB_WT" ]; then
  emit_fail "$SID" "launch-resolves-from-identity" \
    "sub-fixture worktree at $SUB_WT" \
    "(directory missing — worktree create did not produce path)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Create a --no-agent managed session in the sub-fixture worktree.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum tmux create "$RESOLVE_SESSION" \
    --cwd "$SUB_WT" --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-tmux-create" "thrum tmux create $RESOLVE_SESSION" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Launch WITHOUT --runtime — daemon should resolve to "shell" from
# the sub-fixture agent's identity preferred_runtime. THRUM_NAME pin
# is the actual signal here: the daemon walks up to find the caller's
# identity, then uses that identity's preferred_runtime.
local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    env THRUM_NAME="$SUB_AGENT" thrum tmux launch "$RESOLVE_SESSION" 2>&1
)
launch_rc=$?

# Assertion: stdout contains the literal string "shell". The daemon's
# success line is "Launched shell in session <name>"; matching on the
# bare token is robust to wording drift (e.g. "Launched shell runtime
# …"). Failure modes that would NOT contain "shell": fallback to
# "claude" (default), or a non-zero exit with no launch line at all.
if [ "$launch_rc" -eq 0 ] && printf '%s' "$launch_out" | grep -q "shell"; then
  emit_pass "$SID" "launch-resolves-from-identity"
else
  emit_fail "$SID" "launch-resolves-from-identity" \
    "tmux launch (no --runtime flag) exits 0 with stdout containing 'shell'" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Cleanup the sub-fixture session (worktree + sub-daemon are cleaned
# up by the post-function block below).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum tmux kill "$RESOLVE_SESSION" >/dev/null 2>&1 || true

}  # _run_scenario_64

_run_scenario_64

# au7k cleanup: stop sub-daemon. thrum init in $SUB_REPO auto-started
# its own daemon; without explicit stop the sub-daemon would orphan
# past run-level teardown.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop >/dev/null 2>&1 || true
