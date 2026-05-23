#!/usr/bin/env bash
# Scenario: reconcile-on-boot restores write-RPC auth (thrum-soj8)
#
# Verifies that after `thrum daemon restart`, a write RPC issued from the
# same shell in a worktree where an identity file exists succeeds WITHOUT
# requiring `thrum quickstart --force` or any other re-registration. The
# v0.10.0/v0.10.1 daemon dropped session_refs on restart; v0.10.1's boot
# reconcile pass repopulates them from .thrum/identities/*.json so write
# RPCs continue to authenticate via peercred.

SID="109-reconcile-on-boot-restores-write-rpc"
SUB_REPO="$BASE/soj8-109-repo"
SUB_AGENT="soj8_109_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_109() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-109@thrum.local" \
    && git config user.name  "Release Tests 109" \
    && echo "# 109 sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# Initialize thrum and register the agent.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module 109 \
    --intent "Release test 109" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart" "thrum quickstart" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Confirm send works initially (pre-restart baseline).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env "THRUM_NAME=$SUB_AGENT" thrum send "pre-restart" --to "@$SUB_AGENT" >/dev/null 2>&1 || {
    emit_fail "$SID" "send-before-restart" "thrum send pre-restart" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Restart the sub-daemon — this exercises reconcile-on-boot.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon restart >/dev/null 2>&1 || {
    emit_fail "$SID" "daemon-restart" "thrum daemon restart" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }
sleep 1

# Critical assertion: send still works without re-registering.
local send_out send_rc
send_out=$(
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    env "THRUM_NAME=$SUB_AGENT" thrum send "post-restart" --to "@$SUB_AGENT" 2>&1
)
send_rc=$?
if [ "$send_rc" -eq 0 ]; then
  emit_pass "$SID" "send-after-restart" \
    "thrum send post-restart succeeded (reconcile restored session_refs)"
else
  emit_fail "$SID" "send-after-restart" \
    "thrum send succeeds without re-quickstart" \
    "exit=$send_rc out=$(printf '%s' "$send_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_109

_run_scenario_109

# Sub-fixture daemon cleanup (au7k discipline).
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop >/dev/null 2>&1 || true
