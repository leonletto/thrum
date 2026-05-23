#!/usr/bin/env bash
# Scenario: worktree-create-thrum-beads-redirects (migrates full_test_plan.md § 10D.5)
#
# Verifies the full canonical-shape contract for `thrum worktree create`
# when the main repo has both `.thrum/` AND `.beads/` directories: the
# new worktree gets a `.thrum/redirect` pointer, a `.beads/redirect`
# pointer, a per-worktree `.thrum/identities/` directory, AND the git
# branch defaults to `feature/<name>`.
#
# Overlap with scenarios 13/14: those split the contract across two
# scenarios (thrum-side and beads-side) using single-purpose
# sub-fixtures. § 10D.5 deliberately bundles them — the orchestrator
# infrastructure invariant is "all four pieces land together on one
# create call". Asserting all four in a single create catches a
# regression where the create succeeds for one redirect but bails
# silently before writing the other (which 13+14 would not detect
# because each runs against its own fresh fixture).
#
# Sub-fixture (au7k discipline): own daemon at $BASE/kafm9-85-repo/
# so the .beads/ + .thrum/ shape is independent of the run-level
# fixture (which has .thrum/ only). Sub-daemon stopped at end.
#
# Worktree branch: defaulted (no --branch flag) to verify the
# default-branch-name path. Spec § 10D.5 doesn't pass --branch; the
# CLI's default is feature/<name>.

SID="85-worktree-create-thrum-beads-redirects"
SUB_REPO="$BASE/kafm9-85-repo"
SUB_WT_BASE="$BASE/kafm9-85-wt"
SUB_AGENT="kafm9_85_agent"
WT_NAME="test-orchestrator"
# After thrum's auto-append of repo basename:
WT_PATH="$SUB_WT_BASE/$(basename "$SUB_REPO")/${WT_NAME}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_85() {

# Build the sub-fixture: git repo + .beads/ + thrum init + quickstart.
mkdir -p "$SUB_REPO" "$SUB_WT_BASE"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-85@thrum.local" \
    && git config user.name "Release Tests 85" \
    && echo "# 85 sub-fixture" > README.md \
    && mkdir .beads \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 85 sub-fixture" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Patch worktrees config so the create lands under SUB_WT_BASE.
jq --arg bp "$SUB_WT_BASE/" \
  '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
  "$SUB_REPO/.thrum/config.json" > "$SUB_REPO/.thrum/config.json.tmp" \
  && mv "$SUB_REPO/.thrum/config.json.tmp" "$SUB_REPO/.thrum/config.json" \
  || { emit_fail "$SID" "config-patch" "patch worktrees.base_path in $SUB_REPO" "(failed)" \
       "scenarios/${SID}.test.sh:$LINENO"
       return 0; }

# Drive the worktree create via tmux-exec with THRUM_NAME pinned.
# No --branch flag → CLI's default-branch path (feature/<name>).
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    env "THRUM_NAME=$SUB_AGENT" thrum worktree create "$WT_NAME" 2>&1
)
create_rc=$?

if [ "$create_rc" -ne 0 ]; then
  emit_fail "$SID" "create-success" \
    "thrum worktree create $WT_NAME exits 0" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll for redirect files (filesystem may trail success print).
elapsed=0
while [ "$elapsed" -lt 10 ]; do
  if [ -f "$WT_PATH/.thrum/redirect" ] && [ -f "$WT_PATH/.beads/redirect" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 1: thrum redirect exists.
if [ -f "$WT_PATH/.thrum/redirect" ]; then
  emit_pass "$SID" "thrum-redirect-present"
else
  emit_fail "$SID" "thrum-redirect-present" \
    ".thrum/redirect file at ${WT_PATH}/.thrum/redirect" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: beads redirect exists.
if [ -f "$WT_PATH/.beads/redirect" ]; then
  emit_pass "$SID" "beads-redirect-present"
else
  emit_fail "$SID" "beads-redirect-present" \
    ".beads/redirect file at ${WT_PATH}/.beads/redirect" \
    "(file missing — beads redirect was not created despite main repo having .beads/)" \
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

# Assertion 4: git branch defaults to feature/<name>.
if [ -d "$WT_PATH" ]; then
  branch=$(git -C "$WT_PATH" branch --show-current 2>/dev/null)
  if [ "$branch" = "feature/${WT_NAME}" ]; then
    emit_pass "$SID" "default-branch-name"
  else
    emit_fail "$SID" "default-branch-name" \
      "git branch in $WT_PATH == feature/${WT_NAME}" \
      "got: '${branch}'" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "default-branch-name" \
    "worktree directory ${WT_PATH}" \
    "(directory missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_85

_run_scenario_85

# Sub-fixture daemon cleanup (au7k discipline). Worktree teardown is
# best-effort — sub-daemon stop below removes the worktree's parent
# project state; the on-disk directory is reaped by run_teardown's
# rm -rf "$BASE".
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env "THRUM_NAME=$SUB_AGENT" thrum worktree teardown "$WT_NAME" >/dev/null 2>&1 || true
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
