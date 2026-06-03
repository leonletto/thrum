#!/usr/bin/env bash
# Scenario: identity-guard-daemon-forged-caller (thrum-tgqx E3 IG.2)
#
# A raw unix-socket JSON-RPC message.list carrying a forged caller_agent_id
# (the name of a REGISTERED agent, sent from a process whose peercred does NOT
# match that agent) must get an RPC error / no message rows — the daemon-layer
# half of the fail-open closure (E2: resolveAgentOnly propagates *guard.Error,
# HandleList refuses). Uses an ephemeral sub-daemon so the run-level fixture is
# untouched.
#
# Why a REGISTERED agent name: ephemeral_daemon_start sets cross_worktree=warn,
# so the cross-worktree path won't block. The hard refusal comes from the
# peercred-vs-caller_agent_id FORGERY check, which fires when caller_agent_id
# names a real agent but the connecting PID resolves to a different (or no)
# identity. An unregistered forged name may fall to the anonymous/warn path and
# merely return an empty inbox rather than an error — so we forge the
# registered fixture agent's id.
#
# Assertions:
#   1. forged caller_agent_id → JSON-RPC response carries an "error" (no result)
#   2. response leaks no message rows
#   3. positive sanity: legitimate CLI caller still works
#
# NOTE (first-time-green): not yet walked to green on the harness. The exact
# refusal behavior of a raw-socket caller (peercred sees the python3 PID, which
# is unregistered) needs empirical confirmation via run-subset.sh 111 — if the
# anonymous path yields an empty result instead of an error, assertion 1 should
# be relaxed to "no message rows" (assertion 2 already covers the no-leak
# invariant, which is the security-critical property).

SID="111-identity-guard-daemon-forged-caller"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
SUB_FIXTURE="$(mktemp -d "/tmp/ig2-${RUNID}.XXXXXX")"

_run_scenario_111() {

  if ! ephemeral_daemon_start "$SUB_FIXTURE"; then
    emit_fail "$SID" "ephemeral-daemon-start" "ephemeral daemon starts" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  fi
  trap 'ephemeral_daemon_stop; rm -rf "$SUB_FIXTURE"' RETURN

  # Register an agent in the fixture so there is a real identity to forge.
  "$TE" exec --cwd "$FIXTURE_REPO" --clean -- \
    env THRUM_NAME=test_fixture thrum quickstart \
    --name test_fixture --role implementer --module all --non-interactive \
    >/dev/null 2>&1 || true

  local SOCK="$FIXTURE_REPO/.thrum/var/thrum.sock"
  local rpc_out
  rpc_out="$(mktemp -t kafm-IG2-rpc.XXXXXX)"

  # Raw JSON-RPC over the unix socket with a forged caller_agent_id.
  python3 - "$SOCK" <<'PYEOF' > "$rpc_out" 2>&1
import socket, json, sys, io
sock_path = sys.argv[1]
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(5)
try:
    s.connect(sock_path)
except OSError as e:
    print("CONNECT_ERR", e); sys.exit(0)
req = {"jsonrpc": "2.0", "id": 1, "method": "message.list",
       "params": {"caller_agent_id": "test_fixture", "for_agent": "test_fixture"}}
s.sendall((json.dumps(req) + "\n").encode())
buf = io.BytesIO()
while True:
    try:
        chunk = s.recv(4096)
        if not chunk:
            break
        buf.write(chunk)
        try:
            json.loads(buf.getvalue()); break
        except json.JSONDecodeError:
            pass
    except socket.timeout:
        break
s.close()
sys.stdout.write(buf.getvalue().decode(errors="replace"))
PYEOF

  # Assertion 1: response carries an error (or at least no result).
  if python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if ('error' in d or 'result' not in d) else 1)" \
       < "$rpc_out" 2>/dev/null; then
    emit_pass "$SID" "forged-caller-gets-rpc-error"
  else
    emit_fail "$SID" "forged-caller-gets-rpc-error" \
      "JSON-RPC response with forged caller_agent_id has 'error' (or no 'result')" \
      "$(tr '\n' ' ' < "$rpc_out" | head -c 320)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi

  # Assertion 2 (security-critical): no message rows leaked.
  if ! grep -qE '"messages"|"body_content"|"body"' "$rpc_out"; then
    emit_pass "$SID" "forged-caller-no-message-rows"
  else
    emit_fail "$SID" "forged-caller-no-message-rows" \
      "forged-caller response leaks no message rows" \
      "$(tr '\n' ' ' < "$rpc_out" | head -c 320)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$rpc_out"

  # Assertion 3: positive sanity — legitimate CLI caller still works.
  local legit_out legit_rc
  legit_out="$(mktemp -t kafm-IG2-legit.XXXXXX)"
  "$TE" exec --cwd "$FIXTURE_REPO" --clean -- \
    env THRUM_NAME=test_fixture thrum inbox \
    > "$legit_out" 2>&1
  legit_rc=$?
  if [ "$legit_rc" -eq 0 ]; then
    emit_pass "$SID" "legit-caller-still-works"
  else
    emit_fail "$SID" "legit-caller-still-works" \
      "legitimate CLI caller (FIXTURE_REPO, test_fixture) exits 0" \
      "rc=${legit_rc}; output: $(tr '\n' ' ' < "$legit_out" | head -c 240)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$legit_out"

}

_run_scenario_111
