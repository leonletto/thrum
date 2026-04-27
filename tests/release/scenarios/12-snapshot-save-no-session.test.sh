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
# Read-only at the fixture level: the sub-fixture is created in this
# scenario and torn down by the run-level teardown.

SID="12-snapshot-save-no-session"
NO_SESSION_DIR="$BASE/no-session-snapshot"
NO_SESSION_AGENT="kafm12_no_session"
NO_SESSION_RESTART_DIR="$NO_SESSION_DIR/.thrum/restart"

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
  thrum init --runtime claude >/dev/null 2>&1 || {
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
# The markdown spec says "no agent PID found" or "no running agent" —
# match either substring (case-insensitive on key tokens) so the test
# tolerates minor wording polish without a flaky teardown.
if printf '%s' "$SAVE_OUTPUT" | grep -qiE "no agent pid|no running agent|agent.*not.*running|no live (agent|session)"; then
  emit_pass "$SID" "save-error-message"
else
  emit_fail "$SID" "save-error-message" \
    'stderr containing one of: "no agent pid", "no running agent", "agent not running", "no live agent/session"' \
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
