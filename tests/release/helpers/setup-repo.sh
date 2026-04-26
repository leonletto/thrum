#!/usr/bin/env bash
# tests/release/helpers/setup-repo.sh — run-level setup (spec § 4 steps A-C).
# Brings up a complete coord+impl multi-agent fixture and exports
# BASE / REPO / RUNID / COORD_PANE / IMPL_PANE / COORD_REPO / IMPL_REPO.

# run_setup → exports the env vars above. Returns non-zero on any setup failure.
run_setup() {
  # A. Prep
  RUNID="$(date +%Y%m%dT%H%M%S)-$$"
  BASE="$HOME/.thrum_release_tests/$RUNID"
  REPO="$BASE/repo"
  mkdir -p "$REPO"
  # shellcheck disable=SC2046
  unset $(env | grep -E '^THRUM_' | cut -d= -f1) 2>/dev/null || true

  # B. Main repo + coordinator
  (
    cd "$REPO" || exit 1
    git init --initial-branch=main >/dev/null
    git config user.email release-tests@thrum.local
    git config user.name "Release Tests"
    echo "# Release test repo $RUNID" > README.md
    git add . && git commit -m "Initial commit" >/dev/null
    thrum init >/dev/null
  ) || { echo "ERROR: B/repo init failed" >&2; return 1; }

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
