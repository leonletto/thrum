#!/usr/bin/env bash
# Scenario: r02-quickstart-redirect-regression (thrum-tc4w on remote host)
#
# Mirrors the local scenario 108 but on a clean remote host so the
# regression gate fires under conditions closer to a fresh release
# install. The bug under test: `thrum quickstart` from a redirect-using
# worktree, with THRUM_HOME pointing at the redirect target, silently
# wrote the new agent's identity into THRUM_HOME's .thrum/identities/
# instead of the calling worktree.
#
# Inputs from run-remote.sh: HOST and SSH_EXEC env vars.

SID="r02-quickstart-redirect-regression"

RUN_TS="$(date +%Y%m%dT%H%M%S)-$$"
REMOTE_BASE="/tmp/thrum-remote-r02-${RUN_TS}"
REMOTE_REPO="${REMOTE_BASE}/repo"
REMOTE_WT_BASE="${REMOTE_BASE}/wt"
PARENT_AGENT="r02_parent"
WT_NAME="r02-child"
WT_AGENT="r02_child"
# After thrum's auto-append of repo basename:
WT_PATH="${REMOTE_WT_BASE}/$(basename "${REMOTE_REPO}")/${WT_NAME}"

r_exec() {
  local timeout="$1"; shift
  [ "$1" = "--" ] && shift
  local out
  if out="$("$SSH_EXEC" exec --host "$HOST" --timeout "$timeout" -- "$@" 2>&1)"; then
    RC=0
  else
    RC=$?
  fi
  OUT="$out"
}

teardown_r02() {
  if [ "${THRUM_RELEASE_NO_TEARDOWN:-}" = "1" ]; then
    echo "DEBUG: r02 teardown skipped (THRUM_RELEASE_NO_TEARDOWN=1); ${REMOTE_BASE} on ${HOST}" >&2
    return 0
  fi
  "$SSH_EXEC" exec --host "$HOST" --timeout 30 -- bash -lc \
    "thrum --repo '${REMOTE_REPO}' daemon stop 2>/dev/null || true; rm -rf '${REMOTE_BASE}'" \
    >/dev/null 2>&1 || true
}
trap 'teardown_r02' RETURN

# ---------------------------------------------------------------------------
# Step 0: Fresh repo + thrum init + parent quickstart.
# ---------------------------------------------------------------------------
r_exec 30 -- bash -lc "
  set -e
  rm -rf '${REMOTE_BASE}'
  mkdir -p '${REMOTE_REPO}' '${REMOTE_WT_BASE}'
  cd '${REMOTE_REPO}'
  git init --initial-branch=main >/dev/null
  git config user.email 'remote-tests-r02@thrum.local'
  git config user.name 'Remote Tests r02'
  echo '# r02' > README.md
  git add . && git commit -m 'init' >/dev/null
  echo OK
"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-fresh-repo" "git init succeeded on ${HOST}" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-fresh-repo"

r_exec 30 -- bash -lc "cd '${REMOTE_REPO}' && thrum init --non-interactive --runtime claude"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-init" "thrum init exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-init"

r_exec 30 -- bash -lc "
  cd '${REMOTE_REPO}' && thrum quickstart \
    --name ${PARENT_AGENT} \
    --role coordinator \
    --module all \
    --intent 'r02 parent' \
    --no-agent-pid
"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-parent-quickstart" "parent quickstart exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-parent-quickstart"

# Patch worktrees.base_path so the child lands under REMOTE_WT_BASE.
r_exec 15 -- bash -lc "
  cfg='${REMOTE_REPO}/.thrum/config.json'
  jq --arg bp '${REMOTE_WT_BASE}/' \
    '.worktrees = {\"base_path\": \$bp, \"beads_enabled\": false, \"thrum_enabled\": true}' \
    \"\$cfg\" > \"\$cfg.tmp\" && mv \"\$cfg.tmp\" \"\$cfg\"
"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-config-patch" "patch worktrees.base_path" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-config-patch"

# Create the child worktree.
r_exec 30 -- bash -lc "
  cd '${REMOTE_REPO}' && THRUM_NAME=${PARENT_AGENT} thrum worktree create ${WT_NAME}
"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-worktree-create" "thrum worktree create exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-worktree-create"

r_exec 10 -- bash -lc "test -f '${WT_PATH}/.thrum/redirect' && echo OK"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-redirect-present" \
    ".thrum/redirect at ${WT_PATH}/.thrum/redirect" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-redirect-present"

# ---------------------------------------------------------------------------
# THE TEST: quickstart from the child cwd with THRUM_HOME=parent. On v0.10.0
# this writes the identity to ${REMOTE_REPO}/.thrum/identities/. Post-fix it
# lands in ${WT_PATH}/.thrum/identities/.
# ---------------------------------------------------------------------------
r_exec 30 -- bash -lc "
  cd '${WT_PATH}' && THRUM_HOME='${REMOTE_REPO}' thrum quickstart \
    --name ${WT_AGENT} \
    --role implementer \
    --module child \
    --intent 'r02 child' \
    --runtime claude \
    --no-agent-pid \
    --force
"
if [ "${RC}" -ne 0 ]; then
  emit_fail "${SID}" "remote-child-quickstart-success" "child quickstart exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 320)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi
emit_pass "${SID}" "remote-child-quickstart-success"

# Assertion 1: identity in CHILD's identities dir.
r_exec 10 -- bash -lc "test -f '${WT_PATH}/.thrum/identities/${WT_AGENT}.json' && echo OK"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-identity-in-child"
else
  emit_fail "${SID}" "remote-identity-in-child" \
    "identity file at ${WT_PATH}/.thrum/identities/${WT_AGENT}.json" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# Assertion 2: identity NOT in parent.
r_exec 10 -- bash -lc "
  if [ -f '${REMOTE_REPO}/.thrum/identities/${WT_AGENT}.json' ]; then
    echo LEAK; exit 1;
  fi
  echo OK
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-no-parent-leak"
else
  emit_fail "${SID}" "remote-no-parent-leak" \
    "no ${WT_AGENT}.json in ${REMOTE_REPO}/.thrum/identities/" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# Assertion 3: worktree field on the identity equals child path.
r_exec 10 -- bash -lc "
  stored=\$(jq -r '.worktree // \"\"' '${WT_PATH}/.thrum/identities/${WT_AGENT}.json' 2>/dev/null)
  expected=\$(cd '${WT_PATH}' && pwd)
  resolved=\$(cd \"\$stored\" 2>/dev/null && pwd)
  if [ -n \"\$resolved\" ] && [ \"\$resolved\" = \"\$expected\" ]; then
    echo OK
  else
    echo \"stored=\$stored resolved=\$resolved expected=\$expected\"
    exit 1
  fi
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-worktree-field-matches"
else
  emit_fail "${SID}" "remote-worktree-field-matches" \
    "worktree field equals child path" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# Teardown via RETURN trap.
