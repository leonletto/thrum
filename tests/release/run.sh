#!/usr/bin/env bash
# tests/release/run.sh — release test framework entry point.
# See dev-docs/specs/2026-04-26-release-test-framework-design.md
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPERS_DIR="$SCRIPT_DIR/helpers"
SCENARIOS_DIR="$SCRIPT_DIR/scenarios"

# Preflight
for tool in thrum tmux jq git claude expect; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool '$tool' not found in PATH" >&2
    exit 2
  fi
done
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CHECK_CONTEXT="$REPO_ROOT/scripts/check-context-value.sh"
if [ ! -x "$CHECK_CONTEXT" ]; then
  echo "ERROR: $CHECK_CONTEXT missing or not executable" >&2
  exit 2
fi
export THRUM_RELEASE_REPO_ROOT="$REPO_ROOT"  # scenarios may need it

# shellcheck disable=SC1091
source "$HELPERS_DIR/all.sh"

# Optional: scenario filter. If args given, treat as glob filters within scenarios/.
SCENARIO_FILTER="${1:-*.test.sh}"
SCENARIOS=()
while IFS= read -r f; do SCENARIOS+=("$f"); done < <(
  # NOTE: `$SCENARIO_FILTER` is intentionally unquoted so a caller-supplied
  # glob pattern (e.g. `01-*.test.sh`) expands inside `ls`. Don't "fix" the
  # shellcheck-quoting hint here — quoting it would treat the glob as a
  # literal filename and never match anything.
  # shellcheck disable=SC2086
  cd "$SCENARIOS_DIR" && ls $SCENARIO_FILTER 2>/dev/null | sort
)
if [ "${#SCENARIOS[@]}" -eq 0 ]; then
  echo "ERROR: no scenarios matched '$SCENARIO_FILTER' under $SCENARIOS_DIR" >&2
  exit 2
fi

RUN_START=$(date +%s)
# Debug toggle: set THRUM_RELEASE_NO_TEARDOWN=1 to leave coord/impl tmux
# sessions and the ephemeral daemon alive after the script exits, so the
# fixture can be inspected manually (tmux attach -t coord, etc.).
if [ "${THRUM_RELEASE_NO_TEARDOWN:-}" = "1" ]; then
  echo "DEBUG: THRUM_RELEASE_NO_TEARDOWN=1 — teardown disabled; manual cleanup required" >&2
else
  trap 'run_teardown' EXIT
fi

if ! run_setup; then
  echo "ERROR: run-level setup failed; aborting (no scenarios run)" >&2
  exit 2
fi

for scenario_file in "${SCENARIOS[@]}"; do
  sid="${scenario_file%.test.sh}"
  rel="scenarios/$scenario_file"
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
