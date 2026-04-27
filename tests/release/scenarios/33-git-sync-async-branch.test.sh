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
#   3. The a-sync HEAD tree contains an `events.jsonl` blob whose
#      content is non-empty (parseability is asserted in scenarios
#      26/06 — here we just gate "branch is populated")

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

# Assertion 3: events.jsonl present + non-empty on a-sync.
events_blob="$(git -C "$COORD_REPO" show a-sync:events.jsonl 2>/dev/null || true)"
if [ -n "$events_blob" ]; then
  emit_pass "$SID" "events-jsonl-present"
else
  emit_fail "$SID" "events-jsonl-present" \
    "git show a-sync:events.jsonl returns non-empty content" \
    "(empty or path missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
