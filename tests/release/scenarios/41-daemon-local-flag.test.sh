#!/usr/bin/env bash
# Scenario: daemon-local-flag (migrates full_test_plan.md § 4.15)
#
# Verifies `thrum daemon start --local` brings up a local-only
# daemon (no a-sync push, no remote sync) successfully. Sub-fixture
# isolates from the run-level fixture's daemon — the spec's
# main-repo daemon-stop is replaced by stopping the sub-fixture's
# auto-started daemon and re-starting it explicitly with --local.
#
# Four assertions:
#   1. thrum daemon stop (auto-started by init) exits 0
#   2. thrum daemon start --local exits 0
#   3. After --local start, ws.port file is populated
#   4. thrum daemon status reports running
#
# au7k discipline: sub-daemon stopped at scenario end.

SID="41-daemon-local-flag"
SUB_REPO="$BASE/kafm1-41-local"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_41() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-41@thrum.local" \
    && git config user.name "Release Tests 41" \
    && echo "# 41" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum init --runtime claude >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-thrum-init" "thrum init" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# Brief settle for auto-started daemon to fully come up before stop.
sleep 2

# A1: stop the auto-started daemon so the next start exercises --local.
stop_out="$(mktemp -t kafm1-41-stop.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop \
  > "$stop_out" 2>&1
stop_rc=$?
if [ "$stop_rc" -eq 0 ]; then
  emit_pass "$SID" "stop-auto-started-daemon"
else
  got="$(tr '\n' ' ' < "$stop_out" | head -c 240)"
  emit_fail "$SID" "stop-auto-started-daemon" \
    "thrum daemon stop exits 0" \
    "rc=${stop_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$stop_out"

sleep 1  # let stop settle

# A2: start --local.
start_out="$(mktemp -t kafm1-41-start.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon start --local \
  > "$start_out" 2>&1
start_rc=$?
if [ "$start_rc" -eq 0 ]; then
  emit_pass "$SID" "start-local-success"
else
  got="$(tr '\n' ' ' < "$start_out" | head -c 240)"
  emit_fail "$SID" "start-local-success" \
    "thrum daemon start --local exits 0" \
    "rc=${start_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$start_out"

# A3: ws.port populated.
PORT_FILE="$SUB_REPO/.thrum/var/ws.port"
elapsed=0
while [ ! -s "$PORT_FILE" ] && [ "$elapsed" -lt 15 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ -s "$PORT_FILE" ]; then
  emit_pass "$SID" "ws-port-populated"
else
  emit_fail "$SID" "ws-port-populated" \
    ".thrum/var/ws.port populated within 15s of --local start" \
    "(file missing or empty)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# A4: status reports running.
status_out="$(mktemp -t kafm1-41-status.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon status \
  > "$status_out" 2>&1
status_rc=$?
if [ "$status_rc" -eq 0 ] && grep -qiE "running|active|ok" "$status_out"; then
  emit_pass "$SID" "status-shows-running"
else
  got="$(tr '\n' ' ' < "$status_out" | head -c 240)"
  emit_fail "$SID" "status-shows-running" \
    "exit 0 + status output indicating running" \
    "rc=${status_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_out"

}  # _run_scenario_41

_run_scenario_41

# au7k cleanup.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop >/dev/null 2>&1 || true
