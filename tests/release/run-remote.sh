#!/usr/bin/env bash
# tests/release/run-remote.sh — release test framework for remote-host scenarios.
#
# Sibling to run.sh. Where run.sh exercises the local coord+impl tmux fixture
# (which needs a quiet `claude` CLI on the dev box), run-remote.sh ssh's into a
# target host and exercises the binary's end-to-end behavior there. Useful
# when:
#
#   - The local box has a contended `claude` (e.g. an active coordinator
#     session) so the local fixture can't bootstrap.
#   - You want a clean-environment validation gate for a release.
#   - You want to start covering the cross-machine scenarios that
#     dev-docs/release-testing/remote_agent_test_plan.md describes manually.
#
# Each scenario file under remote-scenarios/ owns its own remote tempdir,
# does its work, and tears itself down. The runner just sources each scenario
# in turn and aggregates pass/fail counters using the same emit_pass/emit_fail
# helpers as run.sh.
#
# Usage:
#   ./tests/release/run-remote.sh --host leondev
#   ./tests/release/run-remote.sh --host leondev 'r01*'
#
# Env:
#   THRUM_RELEASE_NO_TEARDOWN=1   skip per-scenario remote cleanup (debug)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPERS_DIR="$SCRIPT_DIR/helpers"
SCENARIOS_DIR="$SCRIPT_DIR/remote-scenarios"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SSH_EXEC="$REPO_ROOT/scripts/ssh-exec"

HOST=""
SCENARIO_FILTER="*.test.sh"

usage() {
  cat <<USAGE >&2
Usage: $0 --host <ssh-target> [scenario-glob]

  --host <user@host>   SSH target (required)
  scenario-glob        Optional glob filter (default: *.test.sh)

Examples:
  $0 --host leondev
  $0 --host leondev 'r01*'
USAGE
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host)
      [ -n "${2:-}" ] || usage
      HOST="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      SCENARIO_FILTER="$1"
      shift
      ;;
  esac
done

[ -n "$HOST" ] || usage
[ -x "$SSH_EXEC" ] || { echo "ERROR: $SSH_EXEC missing or not executable" >&2; exit 2; }

# Preflight: thrum on remote PATH and at the version we expect locally.
LOCAL_VERSION="$("$REPO_ROOT/bin/thrum" version 2>&1 | head -1)"
echo "Local thrum: $LOCAL_VERSION"
REMOTE_VERSION="$("$SSH_EXEC" exec --host "$HOST" -- bash -lc 'thrum version 2>&1 | head -1' 2>/dev/null)" || {
  echo "ERROR: thrum not callable on $HOST. Run 'make deploy-remote REMOTE=$HOST' first." >&2
  exit 2
}
echo "Remote thrum ($HOST): $REMOTE_VERSION"

if [ "$LOCAL_VERSION" != "$REMOTE_VERSION" ]; then
  echo "WARNING: local and remote versions differ. Continuing — scenarios will pin THRUM_BIN if they care." >&2
fi

# Source output helpers (emit_pass/fail/scenario_start/etc).
# shellcheck disable=SC1091
source "$HELPERS_DIR/output.sh"

# Export common vars scenarios can read.
export HOST SSH_EXEC THRUM_RELEASE_REPO_ROOT="$REPO_ROOT"

# Discover scenarios.
SCENARIOS=()
while IFS= read -r f; do SCENARIOS+=("$f"); done < <(
  # shellcheck disable=SC2086
  cd "$SCENARIOS_DIR" && ls $SCENARIO_FILTER 2>/dev/null | sort
)
if [ "${#SCENARIOS[@]}" -eq 0 ]; then
  echo "ERROR: no remote scenarios matched '$SCENARIO_FILTER' under $SCENARIOS_DIR" >&2
  exit 2
fi

RUN_START=$(date +%s)
for scenario_file in "${SCENARIOS[@]}"; do
  sid="${scenario_file%.test.sh}"
  rel="remote-scenarios/$scenario_file"
  scenario_start "$sid" "$rel"
  # shellcheck disable=SC1090
  if ! source "$SCENARIOS_DIR/$scenario_file"; then
    emit_fail "$sid" "scenario-source" "scenario sourced cleanly" "non-zero exit while sourcing" "$rel"
  fi
  scenario_end "$sid"
done

DUR=$(( $(date +%s) - RUN_START ))
summary_block "$DUR"

[ "$RT_COUNT_FAIL" -eq 0 ]
