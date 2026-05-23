#!/usr/bin/env bash
# Scenario: daemon-restart-port (migrates full_test_plan.md § 4.5)
#
# Verifies `thrum daemon restart` preserves the WebSocket port
# (.thrum/var/ws.port). The contract: restarting MUST reuse the same
# port so existing clients (UI tabs, WebSocket-attached agents) don't
# need to rediscover the daemon.
#
# Sub-fixture: $BASE/kafm1-31-restart/ — its own thrum project with
# its own daemon, so restarting it doesn't touch the run-level coord/
# impl daemon. au7k discipline: sub-daemon stopped at scenario end.

SID="31-daemon-restart-port"
SUB_REPO="$BASE/kafm1-31-restart"
SUB_AGENT="kafm1_31_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_31() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-31@thrum.local" \
    && git config user.name "Release Tests 31" \
    && echo "# 31 daemon restart sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 31" >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# thrum init/quickstart auto-starts the daemon. Wait for ws.port file.
PORT_FILE="$SUB_REPO/.thrum/var/ws.port"
elapsed=0
while [ ! -s "$PORT_FILE" ] && [ "$elapsed" -lt 30 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -s "$PORT_FILE" ]; then
  emit_fail "$SID" "ws-port-pre-restart" \
    "ws.port populated within 30s" \
    "(file missing or empty: $PORT_FILE)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

port_before="$(tr -d '[:space:]' < "$PORT_FILE")"

# Restart the sub-daemon.
restart_out="$(mktemp -t kafm1-31-restart.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon restart \
  > "$restart_out" 2>&1
restart_rc=$?

if [ "$restart_rc" -ne 0 ]; then
  got="$(tr '\n' ' ' < "$restart_out" | head -c 240)"
  emit_fail "$SID" "daemon-restart-success" \
    "thrum daemon restart exits 0" \
    "rc=${restart_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$restart_out"
  return 0
fi
emit_pass "$SID" "daemon-restart-success"

# Status check. Allow brief settle for the new daemon.
sleep 2
status_out="$(mktemp -t kafm1-31-status.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon status \
  > "$status_out" 2>&1
status_rc=$?

if [ "$status_rc" -eq 0 ] && grep -qiE "running|active|ok" "$status_out"; then
  emit_pass "$SID" "daemon-status-running"
else
  got="$(tr '\n' ' ' < "$status_out" | head -c 240)"
  emit_fail "$SID" "daemon-status-running" \
    "exit 0 + status output indicating running daemon" \
    "rc=${status_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Re-read port. May trail the restart by a moment.
elapsed=0
port_after=""
while [ "$elapsed" -lt 15 ]; do
  if [ -s "$PORT_FILE" ]; then
    port_after="$(tr -d '[:space:]' < "$PORT_FILE")"
    [ -n "$port_after" ] && break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$port_before" = "$port_after" ] && [ -n "$port_before" ]; then
  emit_pass "$SID" "ws-port-preserved"
else
  emit_fail "$SID" "ws-port-preserved" \
    "ws.port unchanged across restart" \
    "before='${port_before}' after='${port_after}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$restart_out" "$status_out"

}  # _run_scenario_31

_run_scenario_31

# au7k cleanup: stop the sub-daemon.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon stop >/dev/null 2>&1 || true
