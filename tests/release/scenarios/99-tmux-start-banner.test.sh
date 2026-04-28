#!/usr/bin/env bash
# Scenario: tmux-start-banner (migrates thrum-6hqy AC #1 + #2)
#
# Pins the daemon-side identity banner emitted at `thrum tmux start` /
# `thrum tmux launch` time. Two assertions against the COORD pane that
# setup-repo.sh's `thrum tmux start --name coord` produced:
#
#   1. Banner present — pane scrollback contains "Agent: @<agent_id>"
#      and other banner lines (Role/Worktree/Branch). The fixture
#      coord agent registers as test_coordinator_main with role
#      coordinator. Captured via `tmux capture-pane -S -1000` so the
#      banner is reachable even after claude takes over the visible
#      screen — banner lands at the shell prompt before the runtime
#      launches and stays in scrollback.
#
#   2. /thrum:prime NOT sent post-launch for claude (has SessionStart
#      hook). Pre-thrum-6hqy the daemon's HandleLaunch goroutine sent
#      "/thrum:prime" + Enter 10s after launchCmd; xupf+2qe2 made that
#      redundant since inject-prime-context.sh auto-injects the
#      briefing. The fix gates the prime-send on
#      runtimeHasSessionStartHook(runtime) — claude's preset has the
#      flag set, so the goroutine should skip the send. Assertion: the
#      coord agent's JSONL has zero <command-name>/thrum:prime
#      </command-name> entries (the slash-routing tag claude emits when
#      it processes a slash command — the same anchor scenarios
#      54/55/57 use for routing assertions).
#
# Read-only against the run-level fixture's coord pane. No fixture
# mutation — both assertions are post-hoc reads of the existing setup.

SID="99-tmux-start-banner"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

# Assertion 1: banner header present in pane scrollback. Per
# thrum-6hqy.1 the daemon emits the banner via a goroutine 10s AFTER
# launchCmd, so by the time scenario 99 runs (well after setup-repo's
# `thrum tmux start --name coord` + the 10s sleep) the banner is
# safely in scrollback — but possibly hundreds of lines back, since
# claude's TUI redraws + setup-repo's whoami probe + downstream
# scenarios accumulate output continuously after the banner fires.
# Capture all available history (`-S -`) so the banner remains
# reachable regardless of tmux's history-limit.
capture=$(tmux capture-pane -t "$PANE" -S - -p 2>/dev/null || true)
if printf '%s' "$capture" | grep -qE '^Agent: @test_coordinator_main$'; then
  emit_pass "$SID" "banner-agent-line"
else
  emit_fail "$SID" "banner-agent-line" \
    "tmux pane '${PANE}' scrollback contains line 'Agent: @test_coordinator_main'" \
    "(not found in last 1000 lines; sample: $(printf '%s' "$capture" | tail -c 240 | tr '\n' ' '))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Banner role line — second-highest anchor. Fixture coord registers as
# role=coordinator; the banner renders this verbatim from
# config.IdentityFile.Agent.Role.
if printf '%s' "$capture" | grep -qE '^Role:  coordinator$'; then
  emit_pass "$SID" "banner-role-line"
else
  emit_fail "$SID" "banner-role-line" \
    "tmux pane '${PANE}' scrollback contains line 'Role:  coordinator'" \
    "(not found; sample: $(printf '%s' "$capture" | tail -c 240 | tr '\n' ' '))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: claude (has SessionStart hook) should NOT have received
# a post-launch /thrum:prime send during the launch window. The daemon's
# HandleLaunch goroutine fires `time.Sleep(10s)` after launchCmd, so a
# regression that re-introduced the prime-send would show up as a
# `<command-name>/thrum:prime</command-name>` tag in coord's JSONL
# within the first ~30s of the session.
#
# Scoping is required: scenario 54 explicitly sends /thrum:prime to the
# COORD pane via send_slash_command, producing a tag in COORD JSONL,
# AND scenario 54 may run before scenario 99 in a full-suite run
# (alphabetic sort puts "54-" before "99-"). An unscoped grep would
# always be ≥1 in that ordering. Filter to entries within
# (session_start, session_start + LAUNCH_WINDOW_SEC). Anything outside
# that window is from a deliberate later /thrum:prime send by another
# scenario and irrelevant to thrum-6hqy.
LAUNCH_WINDOW_SEC=120
project_dir="$HOME/.claude/projects/$(encode_cwd "$REPO")"
launch_window_hits=0
if [ -d "$project_dir" ] && compgen -G "$project_dir/*.jsonl" >/dev/null; then
  # Single slurp-mode jq pass over every JSONL file in the project
  # dir: derive session_start as the earliest .timestamp seen, then
  # count user messages within session_start + LAUNCH_WINDOW_SEC
  # whose content contains the /thrum:prime tag. Robust to claude's
  # multiple-file session rotation.
  launch_window_hits=$(jq -s --argjson win "$LAUNCH_WINDOW_SEC" '
    ([.[] | select(.timestamp != null) | .timestamp] | min) as $start
    | if $start == null then 0
      else
        ($start | fromdateiso8601) as $start_epoch
        | [.[]
            | select(.type == "user"
                     and (.message.content | type == "string")
                     and (.message.content | contains("<command-name>/thrum:prime</command-name>"))
                     and (.timestamp != null))
            | select(((.timestamp | fromdateiso8601) - $start_epoch) < $win)]
        | length
      end' \
    "$project_dir"/*.jsonl 2>/dev/null)
fi
case "${launch_window_hits:-0}" in
  ''|*[!0-9]*) launch_window_hits=0 ;;
esac

if [ "$launch_window_hits" = "0" ]; then
  emit_pass "$SID" "no-post-launch-prime-for-claude"
else
  emit_fail "$SID" "no-post-launch-prime-for-claude" \
    "0 occurrences of <command-name>/thrum:prime</command-name> in coord JSONL within ${LAUNCH_WINDOW_SEC}s of session start (claude has SessionStart hook — daemon should skip the post-launch prime send)" \
    "got: ${launch_window_hits} occurrences inside the launch window" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
