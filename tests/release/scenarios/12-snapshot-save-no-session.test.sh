#!/usr/bin/env bash
# Scenario: snapshot-save-no-session (migrates full_test_plan.md § 10G.4)
#
# Verifies that `thrum tmux snapshot save` errors clearly with a
# non-zero exit when invoked against an agent that has NO running
# claude session (no agent_pid). The CLI must not fabricate a snapshot
# or silently succeed — that would let /thrum:restart inject empty
# Resume Plans on next session.
#
# Sub-fixture: $BASE/no-session-snapshot/ is its own thrum project with
# a registered agent BUT no claude pane was launched. Snapshot save
# walks the identity file, finds no agent_pid, and surfaces the error.
#
# Sub-fixture cleanup: `thrum init` in $BASE/no-session-snapshot/
# auto-starts a daemon scoped to that thrum_dir. teardown.sh only stops
# the run-level $REPO daemon, so without an explicit stop here the
# sub-fixture's daemon would orphan. The scenario body is wrapped in a
# function so we can reliably stop the sub-daemon AFTER the function
# returns, regardless of which early-return path it took.

SID="12-snapshot-save-no-session"
NO_SESSION_DIR="$BASE/no-session-snapshot"
NO_SESSION_AGENT="kafm12_no_session"
NO_SESSION_RESTART_DIR="$NO_SESSION_DIR/.thrum/restart"

_run_scenario_12() {

# Create the sub-fixture: git repo + thrum init + thrum quickstart, but
# NO `thrum tmux start` / `thrum tmux launch`. The agent is registered
# but has no live claude process.
mkdir -p "$NO_SESSION_DIR"
(
  cd "$NO_SESSION_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-12@thrum.local" \
    && git config user.name "Release Tests 12" \
    && echo "# 12 no-session-snapshot" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $NO_SESSION_DIR" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$NO_SESSION_DIR" --clean -- \
  thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $NO_SESSION_DIR" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$NO_SESSION_DIR" --clean -- \
  thrum quickstart \
    --name "$NO_SESSION_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 12 no-session sub-fixture" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $NO_SESSION_DIR" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Run the under-test command via tmux-exec. THRUM_NAME explicitly names
# the registered-but-not-running agent; cwd is the sub-fixture so the
# right .thrum/identities/ is consulted. Capture combined stdout+stderr
# AND exit code — both are part of the contract.
SAVE_OUTPUT=$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$NO_SESSION_DIR" --clean -- \
    env "THRUM_NAME=$NO_SESSION_AGENT" thrum tmux snapshot save \
      --reason 'no-session-test' 2>&1
)
SAVE_EXIT=$?

# Assertion 1: non-zero exit code.
if [ "$SAVE_EXIT" -ne 0 ]; then
  emit_pass "$SID" "save-non-zero-exit"
else
  emit_fail "$SID" "save-non-zero-exit" \
    "non-zero exit from snapshot save with no live agent" \
    "exit code 0 (incorrectly succeeded)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: error mentions the missing agent / no PID condition.
# Markdown § 10G.4 authorizes exactly two wording variants ("no agent
# PID found" / "no running agent"). We intentionally do NOT widen the
# regex past the spec — adding speculative variants ("agent not
# running", "no live session", etc.) increases false-positive risk
# against unrelated error paths that happen to share those substrings.
# When/if a new wording becomes canonical, add the variant here AND
# update the markdown spec in lockstep.
if printf '%s' "$SAVE_OUTPUT" | grep -qiE "no agent pid|no running agent"; then
  emit_pass "$SID" "save-error-message"
else
  emit_fail "$SID" "save-error-message" \
    'stderr containing one of: "no agent PID found", "no running agent" (markdown § 10G.4 spec)' \
    "${SAVE_OUTPUT:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: no snapshot file was written.
if [ ! -d "$NO_SESSION_RESTART_DIR" ] || [ -z "$(ls -A "$NO_SESSION_RESTART_DIR" 2>/dev/null)" ]; then
  emit_pass "$SID" "save-no-file-written"
else
  files="$(ls -A "$NO_SESSION_RESTART_DIR" 2>/dev/null)"
  emit_fail "$SID" "save-no-file-written" \
    "empty (or missing) ${NO_SESSION_RESTART_DIR}" \
    "files present: ${files}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_12

_run_scenario_12

# Sub-fixture daemon cleanup: thrum init in $NO_SESSION_DIR auto-started
# its own daemon. teardown.sh only stops $REPO's daemon; without this
# explicit stop the sub-daemon would orphan and the next ps-grep based
# preflight cleanup would have to find it. Always run, regardless of
# scenario assertion outcome — that's why this sits AFTER the function
# returns rather than inside it. `|| true` keeps a stop-failure (e.g.
# daemon already crashed) from polluting EXIT.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$NO_SESSION_DIR" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
