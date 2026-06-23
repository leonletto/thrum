#!/usr/bin/env bash
# Scenario: orchestrator-quickstart-role (migrates full_test_plan.md § 10D.10)
#
# Verifies `thrum quickstart --role orchestrator` registers an agent
# whose role is reflected in `thrum team --json` as "orchestrator".
# Pins the role-string round-trip — quickstart-write → team-read —
# for the orchestrator role specifically (the new role added in this
# release cycle). Regression here would mean the orchestrator role
# either doesn't persist or doesn't surface in team listings, both
# of which would break orchestrator-focused workflows that consume
# `thrum team` output.
#
# Sub-fixture: own daemon at $BASE/kafm9-90-repo/ so the new role
# registration is independent of the run-level fixture's
# coordinator/implementer agents. Sub-daemon stopped at end (au7k
# discipline).
#
# Two assertions:
#   1. quickstart-success — quickstart exits 0.
#   2. team-shows-orchestrator-role — team --json contains a member
#      with the registered name AND role == "orchestrator".

SID="90-orchestrator-quickstart-role"
SUB_REPO="$BASE/kafm9-90-repo"
ORCH_AGENT="kafm9_90_orch"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_90() {

# Build the sub-fixture: empty git repo + thrum init (which starts
# its own daemon) + thrum quickstart with --role orchestrator.
mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-90@thrum.local" \
    && git config user.name "Release Tests 90" \
    && echo "# 90 sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

local qs_out qs_rc
qs_out=$(
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    thrum quickstart \
      --name "$ORCH_AGENT" \
      --role orchestrator \
      --module testing \
      --intent "Release test 90 orchestrator role" 2>&1
)
qs_rc=$?

if [ "$qs_rc" -eq 0 ]; then
  emit_pass "$SID" "quickstart-success"
else
  emit_fail "$SID" "quickstart-success" \
    "thrum quickstart --role orchestrator exits 0" \
    "exit ${qs_rc}; output: $(printf '%s' "$qs_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll: team.list reads from the SQLite agents table; the
# quickstart write may flush slightly after the success print.
local team_file="/tmp/kafm9-90-team-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  capture_thrum_json "$SUB_REPO" "$ORCH_AGENT" "$team_file" team --all
  if jq -e --arg n "$ORCH_AGENT" --arg r "orchestrator" \
      '.members[]? | select(.agent_id == $n and .role == $r)' \
      "$team_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 2: team --json contains the registered agent with role
# orchestrator.
if jq -e --arg n "$ORCH_AGENT" --arg r "orchestrator" \
    '.members[]? | select(.agent_id == $n and .role == $r)' \
    "$team_file" >/dev/null 2>&1; then
  emit_pass "$SID" "team-shows-orchestrator-role"
else
  local got
  got=$(tr '\n' ' ' < "$team_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "team-shows-orchestrator-role" \
    "team --json contains member with agent_id=${ORCH_AGENT} and role=orchestrator" \
    "${got:-<no team output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$team_file"

}  # _run_scenario_90

_run_scenario_90

# Sub-fixture daemon cleanup (au7k discipline).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
