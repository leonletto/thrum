#!/usr/bin/env bash
# Scenario: claude-launched-in-panes (migrates full_test_plan.md § 7.2)
#
# Verifies that BOTH coord and impl panes have a live claude process
# that has produced JSONL — i.e. the agents booted past the welcome
# screen and started writing transcripts. Concrete signal: each pane's
# Claude project dir contains a SessionStart attachment.
#
# Why this is its own scenario instead of merging with 51: 51 pins
# the daemon-side bookkeeping (managed session row), 52 pins the
# claude-side existence (transcript JSONL on disk). A regression in
# claude-launch (e.g. CLAUDECODE env leak, broken `claude` shim,
# trust-dialog stalling) would leave the daemon row alive but produce
# no JSONL — slipping past 51, surfaced here.
#
# Both panes' SessionStart attachments are already on disk by the
# time setup-repo.sh returns (it explicitly waits for impl's
# SessionStart and the whoami probe in coord forces coord's
# SessionStart by acting as the first user input). Read-only.

SID="52-claude-launched-in-panes"

_run_scenario_52() {

# Coord pane: SessionStart already landed during setup's whoami probe.
# wait_for_session_start with a short timeout doubles as both an
# existence check and an upper-bounded retry on race conditions.
if wait_for_session_start "$COORD_REPO" 10; then
  emit_pass "$SID" "coord-claude-jsonl-present"
else
  emit_fail "$SID" "coord-claude-jsonl-present" \
    "SessionStart attachment in coord pane's JSONL within 10s" \
    "(no SessionStart attachment under coord project dir)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Impl pane: setup-repo.sh waited for this explicitly, so it should
# be present immediately.
if wait_for_session_start "$IMPL_REPO" 10; then
  emit_pass "$SID" "impl-claude-jsonl-present"
else
  emit_fail "$SID" "impl-claude-jsonl-present" \
    "SessionStart attachment in impl pane's JSONL within 10s" \
    "(no SessionStart attachment under impl project dir)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_52

_run_scenario_52
