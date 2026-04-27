#!/usr/bin/env bash
# Scenario: worktree-setup-thrum-redirect (migrates full_test_plan.md § 10B.1)
#
# Verifies that `thrum worktree create <name>` produces the canonical
# worktree-side .thrum/ scaffolding: a `redirect` pointer at the main
# repo's .thrum/, plus per-worktree `identities/` and `context/`
# directories. Regression here means future agents in newly-created
# worktrees would drift away from the central registration store.
#
# Drives via the COORD pane (`!`-bash) rather than `tmux-exec --cwd
# $REPO`. The 10C-era note in markdown § 10C ("main-repo is an
# anonymous caller and will get 'anonymous caller cannot invoke
# tmux.create'") applies to worktree.create too — anonymous callers
# are rejected. The coord pane is a registered-agent caller and
# matches setup-repo.sh's existing pattern for creating the run-level
# impl worktree.
#
# Worktree base_path was patched in setup-repo.sh to $WORKTREE_BASE/,
# and thrum auto-appends the repo basename ("repo"), so the new
# worktree lands at $WORKTREE_BASE/repo/kafm7-13/.
#
# Per-scenario isolation: teardown the worktree at scenario end so
# subsequent scenarios start clean. Function-wrapped + cleanup-after-
# return so cleanup runs on success AND on early-return failures.

SID="13-worktree-setup-thrum-redirect"
WT_NAME="kafm7-13"
WT_BRANCH="feature/${WT_NAME}"
WT_PATH="$WORKTREE_BASE/repo/${WT_NAME}"

_run_scenario_13() {

# Settle the coord pane in case prior scenarios left rendering in flight.
wait_for_pane_idle "$COORD_PANE" 60

# Drive the worktree create from coord pane. Match the success line
# in stdout — the contract is that the CLI prints "Worktree created"
# on success.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create ${WT_NAME} --branch ${WT_BRANCH}" \
    "Worktree created" 60; then
  emit_pass "$SID" "create-success-line"
else
  emit_fail "$SID" "create-success-line" \
    'thrum worktree create stdout containing "Worktree created"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll: filesystem may trail stdout flush slightly.
elapsed=0
while [ ! -d "$WT_PATH/.thrum" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 2: .thrum/redirect file exists and points at the main
# repo's .thrum/ (which is $REPO/.thrum/ in the fixture).
REDIRECT_FILE="$WT_PATH/.thrum/redirect"
if [ -f "$REDIRECT_FILE" ]; then
  redirect_target="$(cat "$REDIRECT_FILE")"
  # Anchor on $COORD_REPO (not $REPO) so we're robust to scenarios
  # 02/03/09-11 reassigning the bare $REPO variable as a per-scenario
  # convenience (they use `REPO="$COORD_REPO"` / `REPO="$IMPL_REPO"`,
  # which leaks because scenarios are sourced). $COORD_REPO is the
  # canonical run-level export and is never shadowed.
  expected="$COORD_REPO/.thrum"
  # filepath.Clean / realpath equivalence: macOS resolves /var → /private/var
  # for the run-level $BASE root. Compare resolved forms via shell `cd && pwd`.
  if [ "$(cd "$redirect_target" 2>/dev/null && pwd)" = "$(cd "$expected" 2>/dev/null && pwd)" ]; then
    emit_pass "$SID" "redirect-target"
  else
    emit_fail "$SID" "redirect-target" \
      "redirect target resolves to ${expected}" \
      "got: ${redirect_target}" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "redirect-target" \
    "redirect file at ${REDIRECT_FILE}" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: per-worktree .thrum/identities/ exists.
if [ -d "$WT_PATH/.thrum/identities" ]; then
  emit_pass "$SID" "identities-dir-present"
else
  emit_fail "$SID" "identities-dir-present" \
    "directory ${WT_PATH}/.thrum/identities" \
    "(directory missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 4: per-worktree .thrum/context/ exists.
if [ -d "$WT_PATH/.thrum/context" ]; then
  emit_pass "$SID" "context-dir-present"
else
  emit_fail "$SID" "context-dir-present" \
    "directory ${WT_PATH}/.thrum/context" \
    "(directory missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_13

_run_scenario_13

# Teardown the worktree. `|| true` so a teardown failure (e.g. the
# create itself failed and there's nothing to teardown) doesn't pollute
# EXIT. The wt teardown handles git worktree removal + .thrum/redirect
# cleanup atomically.
wait_for_pane_idle "$COORD_PANE" 30
send_command "$COORD_PANE" "! thrum worktree teardown ${WT_NAME} 2>/dev/null || true"
