#!/usr/bin/env bash
# Scenario: restart-coord-fixture-agent-pid (migrates full_test_plan.md § 10.3)
#
# Pins the post-prime contract: after `/thrum:prime` runs in the launched
# pane, the agent's identity file has `agent_pid` reclaimed to the
# runtime's actual PID (>0). Pre-prime `agent_pid` is 0 by design
# (thrum-nu16: subshell-PID write was removed; reclaim happens inside
# `thrum prime`). § 10.3 guards that the prime-time reclaim is intact
# and that tmux_session remains populated alongside it.
#
# Two assertions:
#   1. agent-pid-reclaimed — identity.json has agent_pid > 0.
#   2. tmux-session-still-set — identity.json still has a non-empty
#      tmux_session (regression guard against an over-eager reclaim
#      path nuking adjacent fields).
#
# Depends on scenario 70 (fixture exists; claude has booted and run
# /thrum:prime at least once).

SID="72-restart-coord-fixture-agent-pid"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers exported" \
    "(missing — scenario 70 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

_run_scenario_72() {

local id_file="$KAFM6_S1_WT/.thrum/identities/$KAFM6_S1_AGENT.json"
if [ ! -s "$id_file" ]; then
  emit_fail "$SID" "agent-pid-reclaimed" \
    "non-empty identity file at ${id_file}" \
    "(file missing or empty)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Poll briefly for agent_pid > 0. Reclaim runs as part of /thrum:prime,
# which fires at SessionStart-ish time — by the time scenario 71 has
# driven a `!` bash through the pane, prime has long since completed.
# 10s headroom is generous for the (rare) case where the identity
# write trails the prime emission.
local pid tmux_session elapsed=0
while [ "$elapsed" -lt 10 ]; do
  pid=$(jq -r '.agent_pid // 0' "$id_file" 2>/dev/null)
  if [ "${pid:-0}" -gt 0 ] 2>/dev/null; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "${pid:-0}" -gt 0 ] 2>/dev/null; then
  emit_pass "$SID" "agent-pid-reclaimed"
else
  emit_fail "$SID" "agent-pid-reclaimed" \
    "identity.agent_pid > 0 after /thrum:prime" \
    "agent_pid='${pid}' (prime did not reclaim — check claude booted and /thrum:prime ran)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

tmux_session=$(jq -r '.tmux_session // ""' "$id_file" 2>/dev/null)
if [ -n "$tmux_session" ]; then
  emit_pass "$SID" "tmux-session-still-set"
else
  emit_fail "$SID" "tmux-session-still-set" \
    "identity.tmux_session non-empty after /thrum:prime reclaim" \
    "tmux_session='${tmux_session}' (reclaim nuked tmux_session — regression)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_72

_run_scenario_72
