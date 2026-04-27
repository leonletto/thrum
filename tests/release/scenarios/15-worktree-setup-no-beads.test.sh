#!/usr/bin/env bash
# Scenario: worktree-setup-no-beads (migrates full_test_plan.md § 10B.3)
#
# Inverse of scenario 14: when the main repo has NO `.beads/`,
# `thrum worktree create` must NOT write a `.beads/redirect` in the
# new worktree. Skipping is the correct behavior — a project without
# beads should not have a phantom .beads/ created in its worktrees.
# Together with scenario 14 these pin both branches of the
# beads-redirect contract.
#
# Sub-fixture: $BASE/kafm7-15-no-beads-repo/ — same scaffolding as 14
# but WITHOUT a .beads/ directory. Sub-daemon is stopped at scenario
# end (au7k-class discipline mirrored from kafm.12).

SID="15-worktree-setup-no-beads"
SUB_REPO="$BASE/kafm7-15-no-beads-repo"
SUB_WT_BASE="$BASE/kafm7-15-no-beads-wt"
SUB_AGENT="kafm7_15_agent"
WT_NAME="kafm7-15-wt"
# After thrum's auto-append of repo basename:
WT_PATH="$SUB_WT_BASE/$(basename "$SUB_REPO")/${WT_NAME}"

_run_scenario_15() {

# Build the sub-fixture WITHOUT .beads/. Otherwise identical to 14.
mkdir -p "$SUB_REPO" "$SUB_WT_BASE"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-15@thrum.local" \
    && git config user.name "Release Tests 15" \
    && echo "# 15 no-beads sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$SUB_REPO" --clean -- \
  thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 15 no-beads sub-fixture" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

jq --arg bp "$SUB_WT_BASE/" \
  '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
  "$SUB_REPO/.thrum/config.json" > "$SUB_REPO/.thrum/config.json.tmp" \
  && mv "$SUB_REPO/.thrum/config.json.tmp" "$SUB_REPO/.thrum/config.json" \
  || { emit_fail "$SID" "config-patch" "patch worktrees.base_path in $SUB_REPO" "(failed)" \
       "scenarios/${SID}.test.sh:$LINENO"
       return 0; }

output=$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$SUB_REPO" --clean -- \
    env "THRUM_NAME=$SUB_AGENT" thrum worktree create "$WT_NAME" \
      --branch "feature/${WT_NAME}" 2>&1
)
exit_code=$?

if [ "$exit_code" -ne 0 ]; then
  emit_fail "$SID" "create-success" \
    "thrum worktree create exits 0" \
    "exit ${exit_code}; output: ${output}" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief settle: filesystem updates may trail stdout flush.
elapsed=0
while [ ! -d "$WT_PATH/.thrum" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion: .beads/ MUST NOT exist in the new worktree. The presence
# of even an empty .beads/ would mean EnsureWorktreeRedirects
# unconditionally created it — defeating the "skip when not present"
# contract.
if [ ! -e "$WT_PATH/.beads" ]; then
  emit_pass "$SID" "no-beads-in-worktree"
else
  contents="$(ls -A "$WT_PATH/.beads" 2>/dev/null || echo '<exists>')"
  emit_fail "$SID" "no-beads-in-worktree" \
    ".beads/ absent at ${WT_PATH}" \
    ".beads exists with: ${contents}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_15

_run_scenario_15

# Sub-fixture daemon cleanup (au7k-class).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
