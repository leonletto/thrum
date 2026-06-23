#!/usr/bin/env bash
# Scenario: git-sync-async-branch (migrates full_test_plan.md § 4.7)
#
# Verifies the run-level fixture's repo has an `a-sync` branch
# created by `thrum init`, and that the branch carries the sync
# event log + at least one commit. Pure git read against $COORD_REPO,
# no daemon RPCs needed.
#
# Three assertions:
#   1. `git branch` includes "a-sync"
#   2. `git log --oneline a-sync` returns at least one commit
#   3. The a-sync HEAD tree contains a populated `state/` directory.
#      Per internal/sync/merge.go:101-102 new code does NOT write
#      to events.jsonl (it's kept empty for read-fallback merge from
#      old peers — see spec §4.6 legacy-read horizon). The v0.10.6+
#      canonical sync write paths are state/, messages-v2/, and
#      receipts/. state/ is the most stable marker for "branch is
#      populated by the current sync mechanism" because every
#      registered agent writes a state/agents/<name>.json entry on
#      registration (test_coordinator_main + test_implementer both
#      populate state/agents/ during setup-repo.sh).

SID="33-git-sync-async-branch"

# Assertion 1: branch exists.
branches="$(git -C "$COORD_REPO" branch --list a-sync 2>/dev/null || true)"
if echo "$branches" | grep -q "a-sync"; then
  emit_pass "$SID" "branch-exists"
else
  all_branches="$(git -C "$COORD_REPO" branch 2>&1 | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "branch-exists" \
    "git branch lists 'a-sync'" \
    "got: ${all_branches:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: a-sync has at least one commit.
log_out="$(git -C "$COORD_REPO" log --oneline a-sync -5 2>&1 || true)"
if [ -n "$log_out" ] && echo "$log_out" | grep -qE "^[a-f0-9]{7,}"; then
  emit_pass "$SID" "log-has-commits"
else
  emit_fail "$SID" "log-has-commits" \
    "git log --oneline a-sync produces at least one commit hash" \
    "got: $(echo "$log_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: state/ tree exists on a-sync (v0.10.6+ canonical
# sync write location — events.jsonl is intentionally empty under
# the new sharded layout, see header comment).
state_tree="$(git -C "$COORD_REPO" ls-tree a-sync state/ 2>/dev/null || true)"
if [ -n "$state_tree" ]; then
  emit_pass "$SID" "state-tree-populated"
else
  state_root="$(git -C "$COORD_REPO" ls-tree a-sync 2>&1 | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "state-tree-populated" \
    "git ls-tree a-sync state/ returns at least one entry (v0.10.6+ canonical sync write path)" \
    "got root tree: ${state_root:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
