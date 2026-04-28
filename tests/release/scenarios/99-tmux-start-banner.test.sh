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

# Assertion 1: banner header present in pane scrollback. tmux
# capture-pane -S -1000 -p reads the last 1000 lines of scrollback (vs.
# capture-pane -p which reads only the current screen, where claude's
# TUI has long since taken over). The banner's `Agent: @<id>` line
# is the highest-signal anchor; pinning the @-prefixed form catches a
# regression where the banner was emitted but agent_id rendered empty.
capture=$(tmux capture-pane -t "$PANE" -S -1000 -p 2>/dev/null || true)
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
# a post-launch /thrum:prime send. Search the coord agent's JSONL for
# the routing tag claude emits when it processes a slash command.
# Filter is the same shape as scenario 54's positive-routing assertion
# (`<command-name>/thrum:prime</command-name>`) — here we want ZERO
# matches. Reuse wait_for_jsonl_match's negative form: if the filter
# matches anything within a short poll window, the assertion FAILS.
project_dir="$HOME/.claude/projects/$(encode_cwd "$REPO")"
prime_hits=0
if [ -d "$project_dir" ]; then
  prime_hits=$(grep -hF '<command-name>/thrum:prime</command-name>' \
    "$project_dir"/*.jsonl 2>/dev/null | wc -l | tr -d ' ')
fi
case "${prime_hits:-0}" in
  ''|*[!0-9]*) prime_hits=0 ;;
esac

if [ "$prime_hits" = "0" ]; then
  emit_pass "$SID" "no-post-launch-prime-for-claude"
else
  emit_fail "$SID" "no-post-launch-prime-for-claude" \
    "0 occurrences of <command-name>/thrum:prime</command-name> in coord JSONL (claude has SessionStart hook — daemon should skip the post-launch prime send)" \
    "got: ${prime_hits} occurrences" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
