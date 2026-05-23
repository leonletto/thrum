#!/usr/bin/env bash
# Scenario: worktree-flags (migrates full_test_plan.md § 4.17)
#
# Verifies `thrum worktree create --branch <name>` (custom branch
# name, NOT the default `feature/<name>`) and
# `thrum worktree create --detach` (detached HEAD, no branch).
# Driven via tmux-exec with THRUM_NAME pinned to the run-level
# coord identity (mirrors scenario 14's auth pattern: out-of-pane
# tmux-exec resolves caller via THRUM_NAME, not peercred PID walk).
#
# Worktrees land at $WORKTREE_BASE/$COORD_BASENAME/<name> after thrum's
# auto-append (see setup-repo.sh:113 + cmd/thrum/main.go:2680).
#
# Four assertions:
#   1. --branch create exits 0
#   2. created worktree's HEAD branch is the custom name
#   3. --detach create exits 0
#   4. created worktree is in detached-HEAD state (empty current
#      branch from `git branch --show-current`)
#
# Cleanup: teardown both worktrees at scenario end.

SID="43-worktree-flags"
WT_BRANCH_NAME="kafm1-43-custom-branch"
WT_BRANCH_BRANCH="my-custom-${RUNID}"
WT_BRANCH_PATH="$WORKTREE_BASE/$COORD_BASENAME/${WT_BRANCH_NAME}"
WT_DETACH_NAME="kafm1-43-detach"
WT_DETACH_PATH="$WORKTREE_BASE/$COORD_BASENAME/${WT_DETACH_NAME}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_43() {

# A1: --branch create.
br_out="$(mktemp -t kafm1-43-br.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree create "$WT_BRANCH_NAME" --branch "$WT_BRANCH_BRANCH" \
  > "$br_out" 2>&1
br_rc=$?
if [ "$br_rc" -eq 0 ]; then
  emit_pass "$SID" "create-custom-branch-success"
else
  got="$(tr '\n' ' ' < "$br_out" | head -c 240)"
  emit_fail "$SID" "create-custom-branch-success" \
    "thrum worktree create --branch exits 0" \
    "rc=${br_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$br_out"

# Brief poll for worktree dir to exist.
elapsed=0
while [ ! -d "$WT_BRANCH_PATH" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# A2: branch matches custom name. Always emit PASS or FAIL — silent
# skip on A1 failure would produce false-green.
if [ -d "$WT_BRANCH_PATH" ]; then
  current_branch="$(git -C "$WT_BRANCH_PATH" branch --show-current 2>/dev/null || true)"
  if [ "$current_branch" = "$WT_BRANCH_BRANCH" ]; then
    emit_pass "$SID" "custom-branch-name"
  else
    emit_fail "$SID" "custom-branch-name" \
      "git branch --show-current == '${WT_BRANCH_BRANCH}'" \
      "got: '${current_branch}'" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "custom-branch-name" \
    "worktree dir at ${WT_BRANCH_PATH}" \
    "(create failed above; cannot check branch name)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# A3: --detach create.
det_out="$(mktemp -t kafm1-43-det.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree create "$WT_DETACH_NAME" --detach \
  > "$det_out" 2>&1
det_rc=$?
if [ "$det_rc" -eq 0 ]; then
  emit_pass "$SID" "create-detach-success"
else
  got="$(tr '\n' ' ' < "$det_out" | head -c 240)"
  emit_fail "$SID" "create-detach-success" \
    "thrum worktree create --detach exits 0" \
    "rc=${det_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$det_out"

# Brief poll.
elapsed=0
while [ ! -d "$WT_DETACH_PATH" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# A4: detached HEAD — git branch --show-current returns empty. Always
# emit PASS or FAIL — silent skip on A3 failure would produce false-green.
if [ -d "$WT_DETACH_PATH" ]; then
  current_branch="$(git -C "$WT_DETACH_PATH" branch --show-current 2>/dev/null || true)"
  if [ -z "$current_branch" ]; then
    emit_pass "$SID" "detach-empty-branch"
  else
    emit_fail "$SID" "detach-empty-branch" \
      "git branch --show-current empty (detached HEAD)" \
      "got: '${current_branch}'" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "detach-empty-branch" \
    "worktree dir at ${WT_DETACH_PATH}" \
    "(create failed above; cannot check detached HEAD)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_43

_run_scenario_43

# Cleanup: teardown both worktrees. `|| true` so a failed teardown (e.g.
# create itself failed) doesn't pollute EXIT.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree teardown "$WT_BRANCH_NAME" >/dev/null 2>&1 || true
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree teardown "$WT_DETACH_NAME" >/dev/null 2>&1 || true
# Delete the dangling local branch from --branch test (teardown may not
# remove the branch, only the worktree pointer).
git -C "$COORD_REPO" branch -D "$WT_BRANCH_BRANCH" >/dev/null 2>&1 || true
