#!/usr/bin/env bash
# Scenario: config-keys-init (migrates full_test_plan.md § 10D.9)
#
# Verifies `thrum init` populates `.thrum/config.json` with the
# orchestrator-infrastructure keys: `worktrees.base_path` and
# `orchestration.merge_target` are non-empty after a fresh init.
# These are the keys the orchestrator role + `thrum worktree`
# subcommands consume; if init drops them, the orchestrator surface
# silently degrades (worktree creates land in unexpected paths,
# merge automation has no target).
#
# Setup-repo's run-level fixture has already run init + then a
# scenario-targeted patch on `worktrees.base_path` (setup-repo.sh:107
# rewrites `worktrees` to point at $WORKTREE_BASE), so reading
# $COORD_REPO/.thrum/config.json directly would assert the patched
# state, not the init-time state. To pin the init-time contract we
# build a sub-fixture: fresh empty repo + thrum init + read the
# untouched config. au7k discipline: sub-daemon stopped at end.
#
# Three assertions:
#   1. worktrees-base-path-non-empty — `worktrees.base_path` is set.
#   2. orchestration-merge-target-non-empty — `orchestration.merge_target` is set.
#   3. orchestration-default-autonomy-non-empty — `orchestration.default_autonomy`
#      is set (markdown § 10D.9 lists it; pins the autonomy field
#      that the orchestrator role's preamble consumes).

SID="89-config-keys-init"
SUB_REPO="$BASE/kafm9-89-repo"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_89() {

# Build the sub-fixture: empty git repo + thrum init.
mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-89@thrum.local" \
    && git config user.name "Release Tests 89" \
    && echo "# 89 sub-fixture" > README.md \
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

local cfg="$SUB_REPO/.thrum/config.json"
if [ ! -f "$cfg" ]; then
  emit_fail "$SID" "config-file-present" \
    ".thrum/config.json after thrum init" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 1: worktrees.base_path is set.
local base_path
base_path=$(jq -r '.worktrees.base_path // ""' "$cfg" 2>/dev/null)
if [ -n "$base_path" ]; then
  emit_pass "$SID" "worktrees-base-path-non-empty"
else
  emit_fail "$SID" "worktrees-base-path-non-empty" \
    "worktrees.base_path non-empty in $cfg" \
    "(field empty or missing — got: '${base_path}')" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: orchestration.merge_target is set.
local merge_target
merge_target=$(jq -r '.orchestration.merge_target // ""' "$cfg" 2>/dev/null)
if [ -n "$merge_target" ]; then
  emit_pass "$SID" "orchestration-merge-target-non-empty"
else
  emit_fail "$SID" "orchestration-merge-target-non-empty" \
    "orchestration.merge_target non-empty in $cfg" \
    "(field empty or missing — got: '${merge_target}')" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: orchestration.default_autonomy is set.
local default_autonomy
default_autonomy=$(jq -r '.orchestration.default_autonomy // ""' "$cfg" 2>/dev/null)
if [ -n "$default_autonomy" ]; then
  emit_pass "$SID" "orchestration-default-autonomy-non-empty"
else
  emit_fail "$SID" "orchestration-default-autonomy-non-empty" \
    "orchestration.default_autonomy non-empty in $cfg" \
    "(field empty or missing — got: '${default_autonomy}')" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_89

_run_scenario_89

# Sub-fixture daemon cleanup (au7k discipline).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
