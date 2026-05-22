#!/usr/bin/env bash
# Scenario: claude-tmux-sessions-created (migrates full_test_plan.md § 7.1)
#
# Verifies the run-level setup brought up two managed claude sessions —
# coord and impl — and the daemon's `thrum tmux status` reports them
# both alive. Setup-repo.sh creates both via `thrum tmux start --name
# coord` (A.B step) and `thrum tmux create impl ... && thrum tmux launch
# impl` (C step), so this scenario is a read-only post-setup assertion
# pinning the §7.1 invariant: both managed sessions exist.
#
# Why scope this to a separate scenario instead of relying on
# setup-repo.sh's own checks: setup-repo.sh validates identity via
# whoami probes (which prove claude is up) but does NOT pin the
# specific contract `thrum tmux status` exposes — that the daemon's
# session bookkeeping reports both panes as managed and alive. A
# regression that registered the agent but lost the session row
# would slip past setup but break Step 7's slash-command paths
# downstream, so the assertion lives here.
#
# JSON capture: capture_thrum_json (drive.sh) wraps the
# tmux-exec → file-redirect → host-jq pattern that sidesteps tmux
# capture-pane's 80-col JSON-mangling wrap (memory:
# tmux-capture-pane-json-wrap).
#
# Read-only — no fixture mutation.

SID="51-claude-tmux-sessions-created"

_run_scenario_51() {

local status_file="/tmp/kafm3-51-status-${RUNID}.json"
capture_thrum_json "$COORD_REPO" "test_coordinator_main" "$status_file" \
  tmux status

# Assertion: coord session present and alive. Session names come from
# setup-repo.sh's exports (COORD_PANE/IMPL_PANE) rather than literals — the
# daemon names sessions by worktree basename, so the fixture uses the
# collision-proof test-repo / test-repo-worktree names.
if jq -e --arg n "$COORD_PANE" '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "coord-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "coord-session-alive" \
    "tmux status --json contains $COORD_PANE entry with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion: impl session present and alive
if jq -e --arg n "$IMPL_PANE" '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "impl-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "impl-session-alive" \
    "tmux status --json contains $IMPL_PANE entry with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$status_file"

}  # _run_scenario_51

_run_scenario_51
