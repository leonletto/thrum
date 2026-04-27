#!/usr/bin/env bash
# Scenario: fallback-paths
#
# Verifies the three degraded paths in claude-plugin/scripts/inject-prime-context.sh:
#   4A — thrum binary not on PATH         → silent no-op (no briefing)
#   4B — thrum installed, no agent regd   → historical "Run /thrum:prime" nudge
#   4C — daemon down (agent reachable)    → "Auto-injection failed" + "daemon may be unreachable"
#
# Each sub-case spawns its own tmux session in a UNIQUE sub-directory so each
# encodes to its own ~/.claude/projects/<encoded>/ JSONL bucket. Assertions
# use default cwd-mode against the specific bucket — no --source=any here per
# plan D1 (run-level coord/impl panes also produce briefing-bearing
# SessionStart attachments and would false-positive a negative scan).
#
# Sub-case cwd choices (plan D2, D6) + implementation discoveries:
#   4A: $BASE/fallback-4a   — empty dir, NOT a thrum project. PATH-stripped
#                             claude → hook's `command -v thrum` fails → silent.
#   4B: $BASE/fallback-4b   — `thrum init`'d sub-dir, no agent quickstarted →
#                             hook walks up to local .thrum, finds NO identity,
#                             AGENT_ID empty → historical nudge.
#   4C: $BASE/fallback-4c   — `thrum init`'d AND quickstart'd, then THIS dir's
#                             own daemon stopped before claude launches. Tests
#                             CURRENT BUG BEHAVIOR (tracked in thrum-br6t):
#                             the hook's intended `if [ -z "$PRIME_OUTPUT" ]`
#                             fallback branch (inject-prime-context.sh:42) is
#                             dead code because `thrum prime` always emits to
#                             stdout — even with daemon down it returns
#                             "Thrum not initialized…" on stdout (exit 0).
#                             So the hook wraps that text in the briefing
#                             envelope instead of emitting "Auto-injection
#                             failed". 4C asserts the OBSERVED behavior. When
#                             br6t lands, this scenario must be updated to
#                             assert the new "Auto-injection failed" path.

SID="04-fallback-paths"

mkdir -p "$BASE/fallback-4a" "$BASE/fallback-4b" "$BASE/fallback-4c"

# 4B and 4C both need `thrum init` to succeed, which requires a git-anchored
# cwd (identity guard: non_git_bootstrap). Init each as its own git repo +
# thrum project. 4C also needs `thrum quickstart` so a cached identity exists
# when its daemon is later stopped.
for sub in 4b 4c; do
  ( cd "$BASE/fallback-${sub}" \
      && git init --initial-branch=main >/dev/null \
      && git config user.email "release-tests-${sub}@thrum.local" \
      && git config user.name "Release Tests ${sub}" \
      && echo "# ${sub}" > README.md \
      && git add . && git commit -m "init" >/dev/null ) \
    || emit_fail "$SID" "${sub}-git-init" "git init in $BASE/fallback-${sub}" "(failed)" "scenarios/${SID}.test.sh:$LINENO"

  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$BASE/fallback-${sub}" --clean -- \
    thrum init --runtime claude >/dev/null \
    || emit_fail "$SID" "${sub}-init" "thrum init in $BASE/fallback-${sub}" "(failed)" "scenarios/${SID}.test.sh:$LINENO"
done

# 4C-only: register a fake agent so `thrum whoami` returns an agent_id even
# after the local daemon is stopped (whoami reads identity from disk).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$BASE/fallback-4c" --clean -- \
  thrum quickstart \
    --name release_test_4c \
    --role implementer \
    --module all \
    --intent "Release test 4C daemon-down sub-fixture" >/dev/null \
  || emit_fail "$SID" "4C-quickstart" "thrum quickstart in $BASE/fallback-4c" "(failed)" "scenarios/${SID}.test.sh:$LINENO"

# 4A needs a PATH that contains `claude` but NOT `thrum`. Both live in the
# same directory on this dev machine (~/.local/bin), so we can't just strip
# one path entry. Build a synthetic PATH dir with only a claude symlink.
THRUM_RELEASE_4A_PATHDIR="$BASE/fallback-4a-path"
mkdir -p "$THRUM_RELEASE_4A_PATHDIR"
CLAUDE_BIN="$(command -v claude || true)"
if [ -z "$CLAUDE_BIN" ]; then
  emit_fail "$SID" "4A-setup" "claude on PATH" "(claude not found)" "scenarios/${SID}.test.sh:$LINENO"
fi
ln -sf "$CLAUDE_BIN" "$THRUM_RELEASE_4A_PATHDIR/claude"

run_4a_no_thrum_binary() {
  local tmux_name="fallback-no-thrum"
  local cwd="$BASE/fallback-4a"

  # Sub-fixtures are NOT thrum worktrees, so `thrum tmux create` is rejected
  # by the worktree-identity guard ("caller pane belongs to a different
  # worktree"). Use raw `tmux new-session` instead and rely on
  # wait_for_pane_idle to gate trust-dialog handling.
  tmux new-session -d -s "$tmux_name" -x 500 -y 50 -c "$cwd"
  # Wait for the freshly-created shell to settle before typing.
  wait_for_pane_idle "$tmux_name" 10
  # Synthetic PATH: only claude (via symlink), no thrum. Hook's
  # `command -v thrum` therefore fails → script exits 0 with no stdout.
  # SessionStart attachment still lands (claude always emits one) but
  # contains no briefing markers.
  tmux send-keys -t "$tmux_name" "env PATH='$THRUM_RELEASE_4A_PATHDIR:/usr/bin:/bin' claude"
  sleep 0.5
  tmux send-keys -t "$tmux_name" Enter
  # Each fresh cwd is unknown to Claude Code, which shows a "trust this
  # folder?" dialog (option 1 pre-highlighted). Wait for the dialog to
  # render (pane goes idle once it's drawn) then send Enter to confirm.
  wait_for_pane_idle "$tmux_name" 30
  tmux send-keys -t "$tmux_name" Enter

  # Kick the session: claude writes ZERO JSONL until first user input,
  # but check-context-value.sh ERRORs out immediately if the project dir
  # doesn't exist (no JSONL flushed yet). Send a no-op `!` command to
  # trigger SessionStart + flush the project dir, then wait for the
  # SessionStart attachment to confirm the JSONL is on disk.
  send_command "$tmux_name" "! true"
  wait_for_session_start "$cwd" 60

  # Claude writes ZERO JSONL until first user input lands a session message
  # (see setup-repo.sh § B comment on whoami being the session-kicker).
  # The send_command below acts both as the session-kicker AND the
  # assertion probe: it triggers SessionStart (firing the hook + writing
  # the attachment) while ALSO emitting the bash-stdout entry assert_jsonl
  # polls for. Both end up in the same JSONL, default cwd-mode bucket.
  #
  # Negative assertion: the briefing header MUST NOT appear in this pane's
  # SessionStart attachment.
  send_command "$tmux_name" "! cd \"$cwd\" && $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh 4A_no_briefing \"# Thrum Session Briefing\" SessionStart:startup"
  assert_jsonl "$tmux_name" "$cwd" "$SID" "4A-no-briefing" "FAILED 4A_no_briefing" \
    "scenarios/${SID}.test.sh:$LINENO"

  tmux kill-session -t "$tmux_name" 2>/dev/null || true
}

run_4b_no_agent_registered() {
  local tmux_name="fallback-no-agent"
  local cwd="$BASE/fallback-4b"

  # Raw tmux + send-keys — sub-fixture cwd isn't a thrum worktree so the
  # worktree-identity guard rejects `thrum tmux create`. Trust dialog is
  # handled by wait_for_pane_idle (waits for the dialog to render) then
  # Enter (confirms the pre-highlighted option 1).
  tmux new-session -d -s "$tmux_name" -x 500 -y 50 -c "$cwd"
  wait_for_pane_idle "$tmux_name" 10
  tmux send-keys -t "$tmux_name" "claude"
  sleep 0.5
  tmux send-keys -t "$tmux_name" Enter
  wait_for_pane_idle "$tmux_name" 30
  tmux send-keys -t "$tmux_name" Enter

  # Kick the session: claude writes ZERO JSONL until first user input,
  # but check-context-value.sh ERRORs out immediately if the project dir
  # doesn't exist (no JSONL flushed yet). Send a no-op `!` command to
  # trigger SessionStart + flush the project dir, then wait for the
  # SessionStart attachment to confirm the JSONL is on disk.
  send_command "$tmux_name" "! true"
  wait_for_session_start "$cwd" 60

  # First send_command triggers SessionStart + emits bash-stdout in the
  # same JSONL. Hook detects thrum present + whoami empty (no identities
  # under the local .thrum) → emits historical nudge.
  send_command "$tmux_name" "! cd \"$cwd\" && $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh 4B_nudge \"Run /thrum:prime to load\" SessionStart:startup"
  assert_jsonl "$tmux_name" "$cwd" "$SID" "4B-nudge-present" "VERIFIED 4B_nudge" \
    "scenarios/${SID}.test.sh:$LINENO"

  send_command "$tmux_name" "! cd \"$cwd\" && $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh 4B_no_briefing \"# Thrum Session Briefing\" SessionStart:startup"
  assert_jsonl "$tmux_name" "$cwd" "$SID" "4B-briefing-absent" "FAILED 4B_no_briefing" \
    "scenarios/${SID}.test.sh:$LINENO"

  tmux kill-session -t "$tmux_name" 2>/dev/null || true
}

run_4c_daemon_down() {
  local tmux_name="fallback-daemon-down"
  local cwd="$BASE/fallback-4c"

  # Stop fallback-4c's OWN daemon (not the run-level $REPO daemon — each
  # .thrum has its own socket so $REPO's daemon is unaffected). With this
  # daemon down, `thrum prime` from $cwd hits the unreachable-daemon
  # codepath, which (per discovery thrum-br6t) emits "Thrum not
  # initialized" to stdout rather than empty. The hook wraps that text
  # in the briefing envelope.
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$cwd" --clean -- \
    thrum daemon stop >/dev/null

  tmux new-session -d -s "$tmux_name" -x 500 -y 50 -c "$cwd"
  wait_for_pane_idle "$tmux_name" 10
  tmux send-keys -t "$tmux_name" "claude"
  sleep 0.5
  tmux send-keys -t "$tmux_name" Enter
  # Trust-dialog confirm: wait for the dialog to render then send Enter.
  wait_for_pane_idle "$tmux_name" 30
  tmux send-keys -t "$tmux_name" Enter

  # Kick the session: claude writes ZERO JSONL until first user input,
  # but check-context-value.sh ERRORs out immediately if the project dir
  # doesn't exist (no JSONL flushed yet). Send a no-op `!` command to
  # trigger SessionStart + flush the project dir, then wait for the
  # SessionStart attachment to confirm the JSONL is on disk.
  send_command "$tmux_name" "! true"
  wait_for_session_start "$cwd" 60

  # CURRENT BUG BEHAVIOR (thrum-br6t): hook's intended "Auto-injection
  # failed" branch is dead code because `thrum prime` always emits to
  # stdout. The OBSERVED behavior is that the briefing envelope wraps
  # the degraded "Thrum not initialized" prime output. Assert against
  # what actually lands. When br6t is fixed, swap to "Auto-injection
  # failed" + "daemon may be unreachable".
  send_command "$tmux_name" "! cd \"$cwd\" && $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh 4C_envelope \"# Thrum Session Briefing (auto-loaded)\" SessionStart:startup"
  assert_jsonl "$tmux_name" "$cwd" "$SID" "4C-envelope-present" "VERIFIED 4C_envelope" \
    "scenarios/${SID}.test.sh:$LINENO"

  send_command "$tmux_name" "! cd \"$cwd\" && $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh 4C_degraded \"Thrum not initialized\" SessionStart:startup"
  assert_jsonl "$tmux_name" "$cwd" "$SID" "4C-degraded-prime-output" "VERIFIED 4C_degraded" \
    "scenarios/${SID}.test.sh:$LINENO"

  tmux kill-session -t "$tmux_name" 2>/dev/null || true

  # Restart the local daemon so any teardown logic that walks daemon
  # processes sees a clean state.
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$cwd" --clean -- \
    thrum daemon start >/dev/null || true
}

# Run order: 4A and 4B first while daemon is healthy. 4C last because it
# stops + restarts the run-level daemon.
run_4a_no_thrum_binary || true
run_4b_no_agent_registered || true
run_4c_daemon_down || true
