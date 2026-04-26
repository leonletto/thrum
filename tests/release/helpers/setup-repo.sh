#!/usr/bin/env bash
# tests/release/helpers/setup-repo.sh — run-level setup (spec § 4 steps A-C).
# Brings up a complete coord+impl multi-agent fixture and exports
# BASE / REPO / RUNID / COORD_PANE / IMPL_PANE / COORD_REPO / IMPL_REPO.

# run_setup → exports the env vars above. Returns non-zero on any setup failure.
run_setup() {
  # Capture tmux-exec path BEFORE step A unsets all THRUM_* env vars (which
  # would also strip THRUM_RELEASE_REPO_ROOT set by run.sh).
  #
  # Driver-side thrum calls run through scripts/tmux-exec to break the PID
  # ancestry chain (spec § 4 lines 206-212). Otherwise thrum walks up from
  # the driver's bash → claude (this script's parent) and adopts the wrong
  # identity / fires cross-worktree guards. tmux-exec runs each command in
  # an ephemeral tmux pane whose ancestry chain ends at the tmux server, no
  # claude in sight.
  local TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
  if [ ! -x "$TE" ]; then
    echo "ERROR: $TE missing or not executable" >&2
    return 1
  fi

  # A. Prep
  RUNID="$(date +%Y%m%dT%H%M%S)-$$"
  BASE="$HOME/.thrum_release_tests/$RUNID"
  REPO="$BASE/repo"
  mkdir -p "$REPO"
  # shellcheck disable=SC2046
  unset $(env | grep -E '^THRUM_' | cut -d= -f1) 2>/dev/null || true

  (
    cd "$REPO" || exit 1
    git init --initial-branch=main >/dev/null
    git config user.email release-tests@thrum.local
    git config user.name "Release Tests"
    echo "# Release test repo $RUNID" > README.md
    git add . && git commit -m "Initial commit" >/dev/null
  ) || { echo "ERROR: B/git init failed" >&2; return 1; }

  # --runtime claude bypasses the interactive prompt (when stdin is a tty,
  # which it is inside a tmux-exec pane).
  "$TE" exec --cwd "$REPO" --clean -- thrum init --runtime claude \
    || { echo "ERROR: B/thrum init failed" >&2; return 1; }

  "$TE" exec --cwd "$REPO" --clean -- thrum quickstart \
      --name test_coordinator_main \
      --role coordinator \
      --module all \
      --intent "Release test coordinator" \
    || { echo "ERROR: B/thrum quickstart failed" >&2; return 1; }

  # thrum tmux start creates the session, launches claude, then auto-attaches.
  # The attach blocks until the tty closes; tmux-exec's --timeout bounds it.
  # Session + runtime launch happen synchronously before the attach attempt.
  : > /tmp/zh4p-tmux-start.log
  "$TE" exec --cwd "$REPO" --clean --timeout 30 -- thrum tmux start --name coord \
    > /tmp/zh4p-tmux-start.log 2>&1 || true

  # TROUBLESHOOTING STOP: halt after init + quickstart + tmux start so we
  # can examine what landed before continuing.
  echo "=== STOP after init + quickstart + tmux start ==="
  echo "REPO=$REPO"
  echo "--- /tmp/zh4p-tmux-start.log ---"
  cat /tmp/zh4p-tmux-start.log 2>&1 || true
  echo "--- tmux list-sessions ---"
  tmux list-sessions 2>&1 || true
  echo "--- ls -la \$REPO/.thrum/identities/ ---"
  ls -la "$REPO/.thrum/identities/" 2>&1 || true
  echo "--- cat \$REPO/.thrum/config.json ---"
  cat "$REPO/.thrum/config.json" 2>&1 || true
  return 0

  # PIN driver-side thrum CLI calls to the ephemeral repo's daemon.
  # Without this, every subsequent `thrum tmux create/send/kill` in this
  # script walks up from the script's cwd and finds the DEV repo's .thrum/,
  # talking to the wrong daemon and silently failing. EffectiveRepoPath
  # checks THRUM_HOME before flagRepo / cwd, so this single export is
  # sufficient — no need to wrap each CLI call with tmux-exec on the
  # driver side. Pane-side thrum calls (inside coord/impl) resolve via
  # their --cwd correctly without THRUM_HOME.
  export THRUM_HOME="$REPO"

  thrum tmux create coord \
    --cwd "$REPO" \
    --name test_coordinator_main \
    --role coordinator \
    --module all \
    --intent "Release test coordinator" >/dev/null \
    || { echo "ERROR: B/tmux create coord failed" >&2; return 1; }

  thrum tmux send coord "claude --model haiku --dangerously-skip-permissions"
  wait_for_session_start "$REPO" 60 \
    || { echo "ERROR: coord SessionStart did not appear within 60s" >&2; return 1; }

  thrum tmux send coord '! thrum whoami --json'
  wait_for_jsonl_match "$REPO" \
    '.type == "user" and (.message.content | type == "string") and (.message.content | contains("test_coordinator_main"))' \
    30 >/dev/null \
    || { echo "ERROR: coord whoami did not return expected agent_id" >&2; return 1; }

  # C. Implementer worktree
  # C.1 patch worktrees config
  jq --arg bp "$BASE/" \
    '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
    "$REPO/.thrum/config.json" > "$REPO/.thrum/config.json.tmp" \
    && mv "$REPO/.thrum/config.json.tmp" "$REPO/.thrum/config.json" \
    || { echo "ERROR: C.1 worktrees config patch failed" >&2; return 1; }

  # C.2 create the worktree FROM the coordinator pane
  thrum tmux send coord '! thrum worktree create impl -b feature/release-test-impl'
  # Wait for the worktree dir to appear on disk (driver-side filesystem check —
  # faster + more deterministic than waiting for the JSONL bash-stdout entry).
  local elapsed=0
  while [ ! -d "$BASE/impl" ] && [ "$elapsed" -lt 30 ]; do
    sleep 1
    elapsed=$((elapsed + 1))
  done
  if [ ! -d "$BASE/impl" ]; then
    echo "ERROR: C.2 worktree create did not produce $BASE/impl/" >&2
    return 1
  fi

  # C.3 implementer's tmux session + claude
  thrum tmux create impl \
    --cwd "$BASE/impl" \
    --name test_implementer \
    --role implementer \
    --module all \
    --intent "Release test implementer" >/dev/null \
    || { echo "ERROR: C.3 tmux create impl failed" >&2; return 1; }

  thrum tmux send impl "claude --model haiku --dangerously-skip-permissions"
  wait_for_session_start "$BASE/impl" 60 \
    || { echo "ERROR: impl SessionStart did not appear within 60s" >&2; return 1; }

  thrum tmux send impl '! thrum whoami --json'
  wait_for_jsonl_match "$BASE/impl" \
    '.type == "user" and (.message.content | type == "string") and (.message.content | contains("test_implementer"))' \
    30 >/dev/null \
    || { echo "ERROR: impl whoami did not return expected agent_id" >&2; return 1; }

  # Export per-scenario context
  export RUNID BASE REPO
  export COORD_PANE=coord
  export IMPL_PANE=impl
  export COORD_REPO="$REPO"
  export IMPL_REPO="$BASE/impl"
  return 0
}
