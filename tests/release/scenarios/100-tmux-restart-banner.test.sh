#!/usr/bin/env bash
# Scenario: tmux-restart-banner (migrates thrum-6hqy AC #3 +
# thrum-6hqy.1 post-launch-slot move)
#
# Pins the daemon-side identity banner emitted at `thrum tmux restart`
# time. Per thrum-6hqy.1, the banner now lives in the post-launch
# goroutine slot (10s after launchCmd) for hook runtimes — replacing
# the redundant /thrum:prime that inject-prime-context.sh already
# satisfies. So the contract under test is: claude (hook=true) gets
# banner-via-goroutine 10s after the post-restart launchCmd; the
# banner lands AFTER claude has rendered the briefing in the pane.
#
# Per-scenario sub-fixture (worktree + branch-backed inline-registered
# agent + claude runtime). Claude (not shell) is mandatory — shell
# has HasSessionStartHook=false so the daemon's goroutine takes the
# non-hook branch and never emits the banner. Mirrors kafm.6's
# scenario 70-76 rationale: the contract is hook-runtime banner;
# testing it requires a hook runtime.
#
# Two assertions:
#   1. Restart RPC succeeds (canonical "restarted" success-line).
#   2. Pane scrollback contains the banner's `Agent: @<agent_id>`
#      header line within ~35s after restart returns. Budget covers
#      the daemon goroutine's 10s sleep + claude's ~10-15s startup
#      + the SendKeys settle + scrollback-render time. Capture via
#      `tmux capture-pane -S -1000 -p` so the banner is visible even
#      after the briefing has scrolled past the visible screen.
#
# Per-scenario teardown of session+worktree. Defensive sweep in
# helpers/teardown.sh catches partial failures.

SID="100-tmux-restart-banner"
RST_WT_NAME="hqy-100-restart-wt"
RST_WT="$WORKTREE_BASE/$COORD_BASENAME/$RST_WT_NAME"
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
    "thrum tmux create ${RST_SESSION} --cwd ${RST_WT} --name ${RST_AGENT} --role tester --module testing --intent 'thrum-6hqy.1 restart fixture'" \
    "Session created" 60; then
  emit_fail "$SID" "subfixture-tmux-create" \
    "thrum tmux create ${RST_SESSION} succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi

# Step 3: launch claude via COORD pane drive. Claude (default runtime)
# has HasSessionStartHook=true → daemon goroutine emits banner
# (replaces /thrum:prime). Mirrors scenario 91/69's COORD-driven
# launch pattern.
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux launch ${RST_SESSION}" \
    "Launched" 60; then
  emit_fail "$SID" "subfixture-tmux-launch" \
    "thrum tmux launch ${RST_SESSION} (claude) driven from COORD pane outputs 'Launched'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi

# Step 4: wait for claude to fully boot before restarting. The launch
# goroutine sleeps 10s before its prime/banner action; claude itself
# typically takes ~10-15s from `claude` keystroke to ready prompt. Use
# wait_for_session_start as the boot-complete anchor (mirrors
# setup-repo.sh's pattern for impl).
if ! wait_for_session_start "$RST_WT" 60; then
  emit_fail "$SID" "subfixture-claude-booted" \
    "claude SessionStart attachment in ${RST_WT}'s JSONL within 60s" \
    "(no SessionStart entry observed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_100_cleanup
  return 0
fi

# Step 5: restart the session and assert the success-line contract.
# --force skips the graceful snapshot-save phase (no conversation to
# checkpoint here; matches scenarios 02/03/21/69's --force precedent).
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

# Step 6: poll the post-restart pane for the banner. Per thrum-6hqy.1,
# the daemon's HandleRestart fires the banner from a goroutine 10s
# after launchCmd (replacing /thrum:prime for hook runtimes). Budget:
# 10s daemon sleep + ~10-15s claude startup + send-keys + render.
# 35s wall-clock with 1s polling interval gives enough headroom for
# slow boots without making a healthy run wait the full window.
local pane_capture banner_seen=0
local elapsed=0
while [ "$elapsed" -lt 35 ]; do
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
    "tmux pane '${RST_SESSION}' scrollback contains 'Agent: @${RST_AGENT}' within 35s after restart (post-launch goroutine fires at 10s; claude boot adds ~10-15s)" \
    "(not found in last 1000 lines; sample: $(printf '%s' "$pane_capture" | tail -c 240 | tr '\n' ' '))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

_scenario_100_cleanup

}  # _run_scenario_100

_run_scenario_100
