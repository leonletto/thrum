#!/usr/bin/env bash
# Scenario: identity-guard-cli-wrong-worktree (thrum-tgqx E3 IG.1)
#
# From a shell cd'd into the WRONG worktree with a stale THRUM_AGENT_ID,
# `thrum whoami` / `thrum inbox` must exit NON-ZERO with a guard refusal and
# leak NO inbox/whoami data. This is the CLI-layer half of the identity-guard
# fail-open closure (E1 = getClient classifyRefreshError; this proves it
# end-to-end through the binary).
#
# Assertions:
#   1. whoami non-zero exit from the wrong worktree
#   2. whoami output mentions the identity guard refusal
#   3. inbox non-zero exit from the wrong worktree
#   4. inbox leaks no message rows
#   5. positive sanity: legitimate caller still gets exit 0
#
# NOTE (first-time-green): this scenario has not yet been walked to green on the
# harness. The cross_worktree guard fires on PID-ancestry + identity-file
# mismatch; the exact reliable trigger from a tmux-exec --clean pane (claiming
# test_coordinator_main from the impl worktree, OR a stale THRUM_AGENT_ID in a
# no-.thrum dir) needs empirical confirmation via run-subset.sh 110.

SID="110-identity-guard-cli-wrong-worktree"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# IMPL_REPO is a registered worktree owned by test_implementer; claiming
# test_coordinator_main from there is the canonical cross-worktree forgery.
WRONG_WT="$IMPL_REPO"

_run_scenario_110() {

  # Assertion 1 + 2: whoami from the wrong worktree claiming another agent.
  local whoami_out whoami_rc
  whoami_out="$(mktemp -t kafm-IG1-whoami.XXXXXX)"
  "$TE" exec --cwd "$WRONG_WT" --clean -- \
    env THRUM_AGENT_ID="test_coordinator_main" thrum whoami \
    > "$whoami_out" 2>&1
  whoami_rc=$?

  if [ "$whoami_rc" -ne 0 ]; then
    emit_pass "$SID" "whoami-nonzero-exit-wrong-worktree"
  else
    emit_fail "$SID" "whoami-nonzero-exit-wrong-worktree" \
      "thrum whoami exits non-zero from the wrong worktree" \
      "rc=0; output: $(tr '\n' ' ' < "$whoami_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi

  if grep -qiE "identity guard|cross_worktree|guard.*fired|pid_mismatch|identity refresh failed" "$whoami_out"; then
    emit_pass "$SID" "whoami-guard-refusal-message"
  else
    emit_fail "$SID" "whoami-guard-refusal-message" \
      "output contains an identity-guard refusal" \
      "$(tr '\n' ' ' < "$whoami_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$whoami_out"

  # Assertion 3 + 4: inbox from the wrong worktree.
  local inbox_out inbox_rc
  inbox_out="$(mktemp -t kafm-IG1-inbox.XXXXXX)"
  "$TE" exec --cwd "$WRONG_WT" --clean -- \
    env THRUM_AGENT_ID="test_coordinator_main" thrum inbox \
    > "$inbox_out" 2>&1
  inbox_rc=$?

  if [ "$inbox_rc" -ne 0 ]; then
    emit_pass "$SID" "inbox-nonzero-exit-wrong-worktree"
  else
    emit_fail "$SID" "inbox-nonzero-exit-wrong-worktree" \
      "thrum inbox exits non-zero from the wrong worktree" \
      "rc=0; output: $(tr '\n' ' ' < "$inbox_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi

  # Guard refusals never print message bodies — assert no leak.
  if ! grep -qE '"body"|"content"|"body_content"' "$inbox_out"; then
    emit_pass "$SID" "inbox-no-data-leaked"
  else
    emit_fail "$SID" "inbox-no-data-leaked" \
      "inbox output contains no message body/content (no data leak)" \
      "$(tr '\n' ' ' < "$inbox_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$inbox_out"

  # Assertion 5: positive sanity — legitimate caller still works.
  local legit_out legit_rc
  legit_out="$(mktemp -t kafm-IG1-legit.XXXXXX)"
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum whoami \
    > "$legit_out" 2>&1
  legit_rc=$?

  if [ "$legit_rc" -eq 0 ]; then
    emit_pass "$SID" "legit-caller-still-works"
  else
    emit_fail "$SID" "legit-caller-still-works" \
      "legitimate caller (COORD_REPO, test_coordinator_main) exits 0" \
      "rc=${legit_rc}; output: $(tr '\n' ' ' < "$legit_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$legit_out"

}

_run_scenario_110
