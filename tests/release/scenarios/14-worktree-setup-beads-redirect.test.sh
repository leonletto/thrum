#!/usr/bin/env bash
# Scenario: worktree-setup-beads-redirect (migrates full_test_plan.md § 10B.2)
#
# Verifies that `thrum worktree create` writes a `.beads/redirect`
# pointer in the new worktree when the main repo has a `.beads/`
# directory (i.e., the project uses beads). Regression here means a
# beads-using project's worktrees lose access to centralized issue
# state.
#
# Sub-fixture: $BASE/kafm7-14-beads-repo/ is its own thrum project with
# a registered agent + a `.beads/` dir. The worktree lands at
# $BASE/kafm7-14-beads-wt/<basename>/<wt-name> after thrum's
# auto-append. Sub-daemon is stopped at scenario end (au7k-class
# discipline mirrored from kafm.12).

SID="14-worktree-setup-beads-redirect"
SUB_REPO="$BASE/kafm7-14-beads-repo"
SUB_WT_BASE="$BASE/kafm7-14-beads-wt"
SUB_AGENT="kafm7_14_agent"
WT_NAME="kafm7-14-wt"
# After thrum's auto-append of repo basename:
WT_PATH="$SUB_WT_BASE/$(basename "$SUB_REPO")/${WT_NAME}"

_run_scenario_14() {

# Build the sub-fixture: git repo + .beads/ + thrum init + quickstart.
# `.beads/` is what triggers the EnsureWorktreeRedirects code path that
# writes .beads/redirect in the new worktree.
mkdir -p "$SUB_REPO" "$SUB_WT_BASE"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-14@thrum.local" \
    && git config user.name "Release Tests 14" \
    && echo "# 14 beads sub-fixture" > README.md \
    && mkdir .beads \
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
    --intent "Release test 14 beads sub-fixture" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Patch worktrees config to point at our scenario-controlled base_path
# (mirrors setup-repo.sh:113). thrum auto-appends the repo basename.
# beads_enabled=false here is intentional and NOT a test bug: per
# internal/worktree/worktree.go:123, EnsureRedirects detects beads by
# checking `$mainRepo/.beads` on disk, NOT by reading this flag. The
# scenario specifically asserts the on-disk-driven detection path.
jq --arg bp "$SUB_WT_BASE/" \
  '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
  "$SUB_REPO/.thrum/config.json" > "$SUB_REPO/.thrum/config.json.tmp" \
  && mv "$SUB_REPO/.thrum/config.json.tmp" "$SUB_REPO/.thrum/config.json" \
  || { emit_fail "$SID" "config-patch" "patch worktrees.base_path in $SUB_REPO" "(failed)" \
       "scenarios/${SID}.test.sh:$LINENO"
       return 0; }

# Drive the worktree create against the sub-fixture's daemon. THRUM_NAME
# names the registered agent so the daemon resolves the caller via the
# identity file (not via peercred PID walk, which would yield anonymous
# from an ephemeral tmux-exec pane).
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

# Brief poll: redirect file write may trail the success print slightly.
elapsed=0
while [ ! -f "$WT_PATH/.beads/redirect" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion: .beads/redirect exists in the new worktree, pointing at
# the sub-fixture repo's .beads/. (Per markdown § 10B.2 the file
# content target is the sub-fixture's .beads/ — same shape as
# .thrum/redirect's pointer pattern.)
if [ -f "$WT_PATH/.beads/redirect" ]; then
  emit_pass "$SID" "beads-redirect-present"
else
  emit_fail "$SID" "beads-redirect-present" \
    ".beads/redirect file at ${WT_PATH}/.beads/redirect" \
    "(file missing — beads redirect was not created)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_14

_run_scenario_14

# Sub-fixture daemon cleanup: thrum init in $SUB_REPO auto-started its
# own daemon. Without explicit stop the sub-daemon would orphan past
# run-level teardown. Same pattern as kafm.12's no-session-snapshot
# cleanup; au7k-class discipline.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
