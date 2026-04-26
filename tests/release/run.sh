#!/usr/bin/env bash
# tests/release/run.sh — release test framework entry point.
# See dev-docs/specs/2026-04-26-release-test-framework-design.md
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPERS_DIR="$SCRIPT_DIR/helpers"
SCENARIOS_DIR="$SCRIPT_DIR/scenarios"

# Preflight: required tools
for tool in thrum tmux jq git claude; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool '$tool' not found in PATH" >&2
    exit 2
  fi
done

# Preflight: scripts/check-context-value.sh exists + executable
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
CHECK_CONTEXT="$REPO_ROOT/scripts/check-context-value.sh"
if [ ! -x "$CHECK_CONTEXT" ]; then
  echo "ERROR: $CHECK_CONTEXT missing or not executable" >&2
  exit 2
fi

# TODO (Task 6+): source helpers/all.sh, run_setup, dispatch scenarios, run_teardown, summary
echo "skeleton OK; helpers and scenarios not yet wired"
