#!/usr/bin/env bash
# Scenario: r01-init-quickstart-smoke
#
# First remote-host scenario. Validates that the v0.10.0 binary, when dropped
# onto a clean macOS host (no contending claude session, no prior thrum state),
# can take a fresh git repo through:
#
#   1. thrum init --non-interactive --runtime claude
#      (legacy silent path — wizard is unit-tested locally via 101-107)
#   2. thrum daemon start
#   3. thrum quickstart --no-agent-pid (--no-agent-pid: see remote_agent_test_plan
#      harness-bringup section — without it, self-heal kills the session
#      when the CLI process exits)
#   4. thrum send (self-message — minimal RPC round-trip)
#   5. thrum inbox --json — verify the message arrived
#   6. teardown: daemon stop + cleanup
#
# Why this scenario exists for the v0.10.0 release: the local release-test
# fixture (run.sh) requires a quiet claude CLI inside a tmux pane to
# bootstrap, which the active coordinator session contends with on the dev
# box. This remote scenario gives us a clean-environment validation gate
# that does NOT need claude installed on the remote — only the binary,
# daemon RPC, and CLI are exercised.
#
# Inputs from run-remote.sh: HOST and SSH_EXEC env vars.
SID="r01-init-quickstart-smoke"

RUN_TS="$(date +%Y%m%dT%H%M%S)-$$"
REMOTE_BASE="/tmp/thrum-remote-r01-${RUN_TS}"
REMOTE_REPO="${REMOTE_BASE}/repo"

# Helper: run a one-shot command on the remote with structured exit codes.
# Sets RC and OUT (combined stdout+stderr).
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

# Teardown: stop daemon and remove temp tree even on assertion failure. A
# leaked daemon process under /tmp on a shared host is rude.
teardown_r01() {
  if [ "${THRUM_RELEASE_NO_TEARDOWN:-}" = "1" ]; then
    echo "DEBUG: r01 teardown skipped (THRUM_RELEASE_NO_TEARDOWN=1); ${REMOTE_BASE} on ${HOST}" >&2
    return 0
  fi
  "$SSH_EXEC" exec --host "$HOST" --timeout 30 -- bash -lc \
    "thrum --repo '${REMOTE_REPO}' daemon stop 2>/dev/null || true; rm -rf '${REMOTE_BASE}'" \
    >/dev/null 2>&1 || true
}
trap 'teardown_r01' RETURN

# ---------------------------------------------------------------------------
# Step 0: Fresh repo on the remote.
# ---------------------------------------------------------------------------
r_exec 30 -- bash -lc "
  set -e
  rm -rf '${REMOTE_BASE}'
  mkdir -p '${REMOTE_REPO}'
  cd '${REMOTE_REPO}'
  git init --initial-branch=main >/dev/null
  git config user.email 'remote-tests@thrum.local'
  git config user.name 'Remote Tests'
  echo '# r01' > README.md
  git add . && git commit -m 'init' >/dev/null
  echo OK
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-fresh-repo"
else
  emit_fail "${SID}" "remote-fresh-repo" "git init succeeded on ${HOST}" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi

# ---------------------------------------------------------------------------
# Step 1: thrum init --non-interactive --runtime claude.
# ---------------------------------------------------------------------------
r_exec 30 -- bash -lc "cd '${REMOTE_REPO}' && thrum init --non-interactive --runtime claude"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-init-non-interactive"
else
  emit_fail "${SID}" "remote-init-non-interactive" "thrum init exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi

# Step 1b: scaffolded shape.
r_exec 15 -- bash -lc "
  test -d '${REMOTE_REPO}/.thrum'        || { echo 'MISSING: .thrum/'; exit 1; }
  test -f '${REMOTE_REPO}/.thrum/config.json' || { echo 'MISSING: .thrum/config.json'; exit 1; }
  test -d '${REMOTE_REPO}/.git/thrum-sync/a-sync' || { echo 'MISSING: a-sync sync worktree'; exit 1; }
  echo OK
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-init-scaffold-shape"
else
  emit_fail "${SID}" "remote-init-scaffold-shape" "core .thrum artifacts present" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# Step 1c: v0.10.0 worktrees.base_path default. Headline migration. Note:
# init writes the BASE (~/.thrum/worktrees); `thrum worktree create` appends
# the repo basename at create time, not at init time. The assertion is
# "default points at .thrum/worktrees and NOT the legacy ~/.workspaces path".
r_exec 15 -- bash -lc "jq -r '.worktrees.base_path // empty' '${REMOTE_REPO}/.thrum/config.json'"
EXPECTED_FRAGMENT=".thrum/worktrees"
EXCLUDE_LEGACY=".workspaces"
if [ "${RC}" -eq 0 ] \
   && printf '%s' "${OUT}" | grep -q "${EXPECTED_FRAGMENT}" \
   && ! printf '%s' "${OUT}" | grep -q "${EXCLUDE_LEGACY}"; then
  emit_pass "${SID}" "remote-init-worktrees-base-path-default"
else
  emit_fail "${SID}" "remote-init-worktrees-base-path-default" \
    "base_path contains '${EXPECTED_FRAGMENT}' and not legacy '${EXCLUDE_LEGACY}'" \
    "rc=${RC}; got: $(printf '%s' "${OUT}" | tr '\n' ' ' | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# ---------------------------------------------------------------------------
# Step 2: daemon should already be running — `thrum init` auto-starts it on
# both the wizard path and the legacy --non-interactive path. Just verify.
# ---------------------------------------------------------------------------
r_exec 10 -- bash -lc "cd '${REMOTE_REPO}' && thrum daemon status --json | jq -r '.running'"
if [ "${RC}" -eq 0 ] && [ "$(printf '%s' "${OUT}" | tr -d '[:space:]')" = "true" ]; then
  emit_pass "${SID}" "remote-daemon-status-running"
else
  emit_fail "${SID}" "remote-daemon-status-running" "daemon.running == true" \
    "rc=${RC}; got: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# ---------------------------------------------------------------------------
# Step 3: Quickstart with --no-agent-pid.
# ---------------------------------------------------------------------------
AGENT_NAME="r01_smoke"
r_exec 30 -- bash -lc "
  cd '${REMOTE_REPO}' && thrum quickstart \
    --name ${AGENT_NAME} \
    --role implementer \
    --module test \
    --intent 'r01 smoke test' \
    --no-agent-pid
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-quickstart"
else
  emit_fail "${SID}" "remote-quickstart" "thrum quickstart exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 320)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
  return 0
fi

r_exec 10 -- bash -lc "cd '${REMOTE_REPO}' && thrum whoami --json | jq -r '.agent_id // empty'"
if [ "${RC}" -eq 0 ] && [ "$(printf '%s' "${OUT}" | tr -d '[:space:]')" = "${AGENT_NAME}" ]; then
  emit_pass "${SID}" "remote-whoami-matches"
else
  emit_fail "${SID}" "remote-whoami-matches" "whoami agent_id == ${AGENT_NAME}" \
    "rc=${RC}; got: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# ---------------------------------------------------------------------------
# Step 4: registry → projection → query round-trip.
#
# We don't use a self-send for round-trip verification: by design, sending
# to your own agent does not deliver to your own inbox (the daemon filters
# it). Instead we exercise: send (to self — the RPC still validates routing
# + write path) THEN query the projection via `thrum team --json` and
# assert the agent we just registered shows up. This is the same set of
# code paths that drive the cross-agent case but doesn't require a second
# agent inside the scenario fixture.
# ---------------------------------------------------------------------------
PROBE_TEXT="r01-smoke-probe-${RUN_TS}"
r_exec 15 -- bash -lc "cd '${REMOTE_REPO}' && thrum send '${PROBE_TEXT}' --to '@${AGENT_NAME}'"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-send-rpc-accepted"
else
  emit_fail "${SID}" "remote-send-rpc-accepted" "thrum send exits 0" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

r_exec 15 -- bash -lc "
  cd '${REMOTE_REPO}' && thrum team --json \
    | jq -e --arg n '${AGENT_NAME}' '.members[]? | select(.agent_id == \$n)' \
    >/dev/null
"
if [ "${RC}" -eq 0 ]; then
  emit_pass "${SID}" "remote-team-projection"
else
  emit_fail "${SID}" "remote-team-projection" \
    "thrum team shows agent_id=${AGENT_NAME}" \
    "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
    "remote-scenarios/${SID}.test.sh:${LINENO}"
fi

# ---------------------------------------------------------------------------
# Step 5: Downgrade verification (best-effort).
#
# Per the pre-release process spec
# (dev-docs/specs/2026-05-07-pre-release-process-design.md § "Rollback
# policy"), every release's test cycle exercises rolling back from the
# candidate version to the previous stable. The fixture above already
# created a registered agent + sent a message + populated the team
# projection — the natural seam for verifying that the previous stable
# can still read state produced by the current binary.
#
# Failure of this step does NOT auto-block promotion at the run.sh level;
# the coordinator decides whether to (a) make the rc.N migration
# downgrade-safe, or (b) document the rollback failure mode in the rc.N
# release notes per the spec.
#
# Skipped cleanly when no prior stable exists (first release on a host).
# ---------------------------------------------------------------------------
PREV_STABLE="$(gh release list --repo leonletto/thrum --exclude-pre-releases --limit 1 \
                 2>/dev/null | awk 'NR==1 {print $1}')"

if [ -z "${PREV_STABLE}" ]; then
  emit_skip "${SID}" "remote-downgrade-verify" \
    "no prior stable release found via gh — skipping downgrade step"
else
  # Reinstall previous stable on the remote via the curl install path with
  # VERSION=. Match the documented beta-channel rollback flow exactly so
  # this test exercises the user-visible path.
  r_exec 120 -- bash -lc "
    set -e
    VERSION='${PREV_STABLE}' curl -fsSL \
      https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh \
      | sh
  "
  if [ "${RC}" -eq 0 ]; then
    emit_pass "${SID}" "remote-downgrade-install"
  else
    emit_fail "${SID}" "remote-downgrade-install" \
      "VERSION=${PREV_STABLE} install succeeded" \
      "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 320)" \
      "remote-scenarios/${SID}.test.sh:${LINENO}"
  fi

  # Restart the daemon under the previous-stable binary.
  r_exec 30 -- bash -lc "cd '${REMOTE_REPO}' && thrum daemon restart"
  if [ "${RC}" -eq 0 ]; then
    emit_pass "${SID}" "remote-downgrade-daemon-restart"
  else
    emit_fail "${SID}" "remote-downgrade-daemon-restart" \
      "thrum daemon restart succeeded after rollback to ${PREV_STABLE}" \
      "rc=${RC}; out: $(printf '%s' "${OUT}" | head -c 240)" \
      "remote-scenarios/${SID}.test.sh:${LINENO}"
  fi

  # Verify daemon reports running and the registered agent's identity is
  # still readable (projection survived the downgrade).
  r_exec 15 -- bash -lc "cd '${REMOTE_REPO}' && thrum daemon status --json | jq -r '.running'"
  if [ "${RC}" -eq 0 ] && [ "$(printf '%s' "${OUT}" | tr -d '[:space:]')" = "true" ]; then
    emit_pass "${SID}" "remote-downgrade-daemon-running"
  else
    emit_fail "${SID}" "remote-downgrade-daemon-running" \
      "daemon.running == true under ${PREV_STABLE}" \
      "rc=${RC}; got: $(printf '%s' "${OUT}" | head -c 240)" \
      "remote-scenarios/${SID}.test.sh:${LINENO}"
  fi

  r_exec 15 -- bash -lc "cd '${REMOTE_REPO}' && thrum whoami --json | jq -r '.agent_id // empty'"
  if [ "${RC}" -eq 0 ] && [ "$(printf '%s' "${OUT}" | tr -d '[:space:]')" = "${AGENT_NAME}" ]; then
    emit_pass "${SID}" "remote-downgrade-state-readable"
  else
    emit_fail "${SID}" "remote-downgrade-state-readable" \
      "whoami still returns ${AGENT_NAME} under ${PREV_STABLE}" \
      "rc=${RC}; got: $(printf '%s' "${OUT}" | head -c 240)" \
      "remote-scenarios/${SID}.test.sh:${LINENO}"
  fi
fi

# Teardown via RETURN trap.
