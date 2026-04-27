#!/usr/bin/env bash
# Scenario: context-persists-restart (migrates full_test_plan.md § 9.5)
#
# Verifies thrum context survives a session kill + restart: a context
# blob saved before the restart is still readable after the agent's
# pane is torn down and recreated. The persistence contract belongs
# to the storage layer (DB-backed, agent-identity-keyed — independent
# of session lifetime), so the assertion targets that contract.
#
# Why it matters: the whole point of /thrum:load-context after
# auto-compaction OR a /thrum:restart is that the agent's prior
# context is recoverable. If context were session-scoped instead of
# agent-scoped, a restart would wipe it and the recovery flow would
# be useless.
#
# Test approach:
#   1. Save a unique marker into the IMPL agent's context via
#      tmux-exec (out of pane → deterministic CLI write).
#   2. Restart the IMPL pane via `thrum tmux restart impl --force`
#      (mirrors scenario 02's restart mechanism, which is the
#      framework-supported way to kill + recreate a managed pane).
#   3. Wait for the new SessionStart attachment (proves the pane
#      came back up, claude is alive).
#   4. Read context back via tmux-exec → assert marker present.
#
# Deviation from markdown § 9.5: spec uses bare `tmux kill-session`
# + `tmux new-session` to nuke and rebuild the coord pane manually,
# then sends /thrum:load-context for visual confirmation. We use
# `thrum tmux restart impl --force` because (i) it's the framework-
# supported restart path that real users invoke, (ii) it preserves
# the snapshot/restore plumbing scenarios 02/03 already cover, and
# (iii) a manual kill+new-session would orphan the daemon's session
# bookkeeping in the fixture and could corrupt subsequent runs.
# We use IMPL pane instead of COORD because COORD was just driven
# by scenarios 17 and 20 — IMPL gives a cleaner pane state and
# parallels the impl-restart pattern from scenario 02.
#
# The persistence assertion targets the storage layer directly —
# what /thrum:load-context's underlying `thrum prime` would read.
# Scenarios 17/18 already cover save→show on the same code path;
# scenario 20 covers /thrum:load-context invocation. This scenario
# adds the restart-survival dimension on top.
#
# Fixture mutation: writes context for test_implementer, restarts
# impl pane. Other scenarios that depend on impl pane stability
# should run BEFORE 21 (current ordering puts all impl-touching
# scenarios at 02/05/07/etc. earlier in sort order).

SID="21-context-persists-restart"
MARKER="kafm5-21-marker-${RUNID}"

_run_scenario_21() {

# Pre-step: save the marker into IMPL's context store. tmux-exec
# runs commands inside an ephemeral tmux pane (not a child process),
# so shell-pipe stdin doesn't reach the inner command — use the
# --file flag instead, which `thrum context save` accepts as an
# explicit content source.
marker_file="$(mktemp -t kafm5-21.XXXXXX)"
printf '%s\n' "$MARKER" > "$marker_file"
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum context save --file "$marker_file" \
  >/dev/null 2>&1 || true
rm -f "$marker_file"

# Sanity check the precondition: the marker should be in IMPL's
# context BEFORE the restart. If this fails we can't tell whether
# the restart broke persistence or save itself broke.
pre_check="$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
    env THRUM_NAME=test_implementer thrum context show --session 2>&1 || true
)"
if ! echo "$pre_check" | grep -q "$MARKER"; then
  emit_fail "$SID" "marker-saved-precondition" \
    "context show --session output containing '${MARKER}' BEFORE restart" \
    "(marker not present in pre-restart show output)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Restart IMPL via the framework-supported restart path. Driver-side
# thrum calls must wrap through tmux-exec to break the PID chain
# (same rationale as scenario 02).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
  thrum tmux restart impl --force >/dev/null 2>&1 || true

# Wait for the NEW SessionStart attachment to land in IMPL JSONL.
# Same race-condition guard as scenarios 02/03: 5s sleep before
# polling so the new claude has time to create its new JSONL file.
sleep 5
if ! wait_for_session_start "$IMPL_REPO" 60; then
  emit_fail "$SID" "post-restart-session-start" \
    "new SessionStart attachment within 60s of restart" \
    "(none observed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion: marker is still readable from the storage layer AFTER
# the restart. Same out-of-pane tmux-exec invocation as the pre-
# check — proves agent-identity-keyed persistence (not session-
# scoped) and proves the data didn't get cleared by the restart.
post_check="$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$IMPL_REPO" --clean -- \
    env THRUM_NAME=test_implementer thrum context show --session 2>&1 || true
)"
if echo "$post_check" | grep -q "$MARKER"; then
  emit_pass "$SID" "context-survives-restart"
else
  got="$(echo "$post_check" | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "context-survives-restart" \
    "context show --session output containing '${MARKER}' AFTER restart" \
    "${got:-<empty output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_21

_run_scenario_21
