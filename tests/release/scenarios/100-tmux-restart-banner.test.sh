#!/usr/bin/env bash
# Scenario: tmux-restart-banner (migrates thrum-6hqy AC #3)
#
# Pins the daemon-side identity banner emitted at `thrum tmux restart`
# time. Same shape as scenario 99's start-time banner, but the
# orientation event is the post-relaunch pane (not the initial start).
# Restart is the same identity-display moment as start from the
# human-watching-tmux perspective.
#
# Per-scenario sub-fixture (worktree + branch-backed inline-registered
# agent + shell runtime, mirrors scenario 91's pattern). Shell runtime
# is intentional: claude would burn API budget on the restart-snapshot
# preamble work for no contract gain — the contract under test is
# the daemon's banner emission, which is runtime-independent.
#
# Two assertions:
#   1. Restart RPC succeeds (canonical "restarted" success-line).
#   2. After restart, pane scrollback contains the banner's
#      `Agent: @<agent_id>` header line. capture-pane -S -1000 reads
#      scrollback so the banner is reachable even if subsequent prompt
#      output scrolled past the visible screen.
#
# Per-scenario teardown of session+worktree at scenario end (same
# discipline as scenario 91).

SID="100-tmux-restart-banner"
RST_WT_NAME="hqy-100-restart-wt"
RST_WT="$WORKTREE_BASE/repo/$RST_WT_NAME"
RST_SESSION="hqy-100-restart"
RST_AGENT="hqy_100_restart_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_scenario_100_cleanup() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux kill "$RST_SESSION" \
    >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$RST_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_100() {

wait_for_pane_idle "$COORD_PANE" 60

# Step 1: branch-backed worktree (mirrors scenario 91 — inline-register
# tmux create requires a non-detached worktree under the post-nu16
# caller-identity guard).
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$RST_WT_NAME" \
      --branch "feature/${RST_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$RST_WT" ]; then
  emit_fail "$SID" "subfixture-worktree" \
    "thrum worktree create $RST_WT_NAME succeeds and produces $RST_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: inline-register agent via thrum tmux create from COORD pane
# (caller-identity guard — tmux-exec ephemeral callers get refused).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create ${RST_SESSION} --cwd ${RST_WT} --name ${RST_AGENT} --role tester --module testing --intent 'thrum-6hqy restart fixture'" \
    "Session created" 60; then
  emit_fail "$SID" "subfixture-tmux-create" \
    "thrum tmux create ${RST_SESSION} succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi

# Step 3: launch shell runtime via COORD pane drive (mirrors 91's
# rationale — caller-identity guard refuses tmux-exec ephemeral
# callers for HandleLaunch on cross-worktree create).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux launch ${RST_SESSION} --runtime shell" \
    "Launched shell" 60; then
  emit_fail "$SID" "subfixture-tmux-launch" \
    "thrum tmux launch ${RST_SESSION} --runtime shell driven from COORD pane outputs 'Launched shell'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi

# Brief settle: launch's HandleLaunch goroutine writes the row +
# starts the shell async. 3s mirrors scenario 45/69/91 timing.
sleep 3

# Step 4: restart the session and assert the success-line contract.
# --force skips the graceful snapshot-save phase (no claude in this
# fixture so there's no snapshot to capture; matches scenarios
# 02/03/21/69's --force precedent).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux restart ${RST_SESSION} --force" \
    "restarted" 60; then
  emit_fail "$SID" "restart-success-line" \
    "thrum tmux restart ${RST_SESSION} --force outputs 'restarted'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi
emit_pass "$SID" "restart-success-line"

# Step 5: poll the post-restart pane for the banner. The daemon's
# HandleRestart calls h.emitIdentityBanner BEFORE launchCmd — the
# banner shell-printf executes at the fresh shell prompt and then
# the runtime takes over. capture-pane -S -1000 reads scrollback so
# we see lines that scrolled past the current screen. Allow up to
# 10s for the send-keys + Enter chain to land + the shell to render
# the printf output.
local pane_capture banner_seen=0
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  pane_capture=$(tmux capture-pane -t "$RST_SESSION" -S -1000 -p 2>/dev/null || true)
  if printf '%s' "$pane_capture" | grep -qE "^Agent: @${RST_AGENT}$"; then
    banner_seen=1
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$banner_seen" = "1" ]; then
  emit_pass "$SID" "restart-banner-agent-line"
else
  emit_fail "$SID" "restart-banner-agent-line" \
    "tmux pane '${RST_SESSION}' scrollback contains 'Agent: @${RST_AGENT}' after restart" \
    "(not found in last 1000 lines within 10s; sample: $(printf '%s' "$pane_capture" | tail -c 240 | tr '\n' ' '))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

_scenario_100_cleanup

}  # _run_scenario_100

_run_scenario_100
