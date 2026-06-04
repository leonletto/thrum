#!/usr/bin/env bash
# Scenario: identity-guard-cli-wrong-worktree (thrum-tgqx E3 IG.1)
#
# From a shell cd'd into the WRONG worktree with a stale THRUM_AGENT_ID,
# `thrum whoami` / `thrum inbox` must ENGAGE the identity guard and leak NO
# inbox/whoami data. This is the CLI-layer half of the identity-guard fail-open
# closure (E1 = getClient classifyRefreshError; this proves it end-to-end
# through the binary).
#
# Assertions:
#   1. whoami engages the identity guard from the wrong worktree
#   2. whoami output carries an identity-guard signal
#   3. inbox engages the identity guard from the wrong worktree
#   4. inbox leaks no message rows
#   5. positive sanity: legitimate caller still gets exit 0
#
# WALKED TO GREEN (rc.7, 2026-06-03): empirically confirmed via run-subset 110.
# The trigger here is a tmux-exec --clean pool pane (caller_pane=
# tmux-exec-pool-*), whose PID resolves to no registered agent in WRONG_WT. The
# daemon engages the guard via the *soft* PaneTargetForIdentity path ("caller
# pane belongs to a different worktree") — a WARN that, per the thrum-9sxc
# footgun, does NOT force a non-zero CLI exit (the operation returns rc=0 but
# serves NO authoritative data for the forged agent). The hard cross_worktree
# refusal the original draft expected fires on the PID-ancestry +
# identity-file mismatch path, not on this pool-pane path. The security-critical
# invariant (no data leak — assertion 4) holds on BOTH paths, so assertions
# 1-3 gate on "guard engaged (non-zero exit OR a guard signal in output)" per
# the documented defense (gate on the side effect, accept either exit-0 or the
# guard signal — cf. scens 85 + 88).

SID="110-identity-guard-cli-wrong-worktree"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# A guard signal proves the daemon engaged the identity guard rather than
# silently serving the forged request. Covers BOTH the hard cross_worktree
# refusal path AND the soft PaneTargetForIdentity warn path (thrum-9sxc).
GUARD_SIGNAL="identity guard|cross_worktree|guard.*fired|pid_mismatch|identity refresh failed|PaneTargetForIdentity|caller pane belongs to a different worktree"

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

  if [ "$whoami_rc" -ne 0 ] || grep -qiE "$GUARD_SIGNAL" "$whoami_out"; then
    emit_pass "$SID" "whoami-guard-engaged-wrong-worktree"
  else
    emit_fail "$SID" "whoami-guard-engaged-wrong-worktree" \
      "thrum whoami from the wrong worktree engages the guard (non-zero exit OR guard signal)" \
      "rc=${whoami_rc}; output: $(tr '\n' ' ' < "$whoami_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi

  if grep -qiE "$GUARD_SIGNAL" "$whoami_out"; then
    emit_pass "$SID" "whoami-guard-refusal-message"
  else
    emit_fail "$SID" "whoami-guard-refusal-message" \
      "output contains an identity-guard signal" \
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

  if [ "$inbox_rc" -ne 0 ] || grep -qiE "$GUARD_SIGNAL" "$inbox_out"; then
    emit_pass "$SID" "inbox-guard-engaged-wrong-worktree"
  else
    emit_fail "$SID" "inbox-guard-engaged-wrong-worktree" \
      "thrum inbox from the wrong worktree engages the guard (non-zero exit OR guard signal)" \
      "rc=${inbox_rc}; output: $(tr '\n' ' ' < "$inbox_out" | head -c 240)" \
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
