#!/usr/bin/env bash
# tests/release/run-subset.sh — focused subset runner for failure-group triage.
#
# Sources the SAME scenario files as run.sh (zero drift; fixes proven here
# apply directly to the gate). Lets you iterate on small batches of failures
# clustered by symptom — fix one root cause, re-run the group, move on.
#
# Usage:
#   bash tests/release/run-subset.sh -g <group-name>
#   bash tests/release/run-subset.sh <id> [<id>...]    # one or more scenario IDs (e.g. 14 or 14-worktree-setup-beads-redirect)
#   bash tests/release/run-subset.sh -l               # list groups + member scenarios
#
# Behaviour mirrors run.sh exactly: self-isolating launcher, shared
# coord+impl fixture setup, per-scenario emit_pass/emit_fail, end-of-run
# summary, exit 0 if all pass else 1. The launcher tee'd log + per-fail
# pane snapshots (commits 0b080b6afc + d8022db914) work identically here.
#
# Default groups are seeded from the v0106 RC1 first-full-gate result (run
# 2026-05-22, log /tmp/reltest-87642.log) and represent the dominant failure
# clusters. Tune as triage progresses.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPERS_DIR="$SCRIPT_DIR/helpers"
SCENARIOS_DIR="$SCRIPT_DIR/scenarios"

# Short-circuit args that don't need self-isolation (no scenarios run, no
# fixture setup) — handle them here, before the launcher would re-exec us
# into a detached tmux session for nothing.
if [ "$#" -eq 0 ]; then
  echo "ERROR: nothing to run. Pass -g <group> or one or more scenario IDs." >&2
  echo "       Use -l to list groups, -h for help." >&2
  exit 2
fi
for _arg in "$@"; do
  case "$_arg" in
    -h|--help)
      sed -n '3,16p' "$0"; exit 0 ;;
    -l|--list)
      _SHORT_CIRCUIT_LIST=1 ;;
  esac
done

# shellcheck disable=SC1091
source "$HELPERS_DIR/self-isolate.sh"
if [ -z "${_SHORT_CIRCUIT_LIST:-}" ]; then
  thrum_release_self_isolate "$SCRIPT_DIR/run-subset.sh" "$@"
fi

# Same preflight as run.sh (post-isolation, so we're in clean ancestry).
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
export THRUM_RELEASE_REPO_ROOT="$REPO_ROOT"

# shellcheck disable=SC1091
source "$HELPERS_DIR/all.sh"

# --- failure-group catalog -----------------------------------------------
# Map: group-name -> space-separated list of scenario IDs (the numeric prefix
# + the rest of the basename, no .test.sh suffix). Resolved to scenario files
# via "<id>*.test.sh" glob — keeps the catalog readable + tolerant of
# basename drift.
declare -A FAIL_GROUPS
FAIL_GROUPS[subfixture-init]="14 15 41 42 89"
FAIL_GROUPS[restart-fixture]="70 73 75 77 79"
FAIL_GROUPS[wizard-exit]="$(grep -lE 'wizard-exit' "$SCENARIOS_DIR"/*.test.sh 2>/dev/null | xargs -n1 basename 2>/dev/null | sed 's/\.test\.sh$//' | tr '\n' ' ')"
FAIL_GROUPS[multi-runtime]="63 64 65 66 67"
FAIL_GROUPS[worktree-redirect]="13 85 86 88"
FAIL_GROUPS[queue]="45 46 47 48 49"
FAIL_GROUPS[restart-preamble]="2 3 75"

list_groups() {
  printf 'Available groups (group: members):\n\n'
  local g
  for g in "${!FAIL_GROUPS[@]}"; do
    printf '  %-22s %s\n' "$g" "${FAIL_GROUPS[$g]}"
  done | sort
}

# --- arg parsing ----------------------------------------------------------
GROUP=""
IDS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -g|--group)
      [ "$#" -ge 2 ] || { echo "ERROR: -g requires a group name" >&2; exit 2; }
      GROUP="$2"; shift 2
      ;;
    -l|--list)
      list_groups; exit 0
      ;;
    -h|--help)
      sed -n '3,16p' "$0"; exit 0
      ;;
    *)
      IDS+=("$1"); shift
      ;;
  esac
done

# --- resolve scenarios ----------------------------------------------------
if [ -n "$GROUP" ]; then
  if [ -z "${FAIL_GROUPS[$GROUP]+x}" ]; then
    echo "ERROR: unknown group '$GROUP'. Available:" >&2
    list_groups >&2
    exit 2
  fi
  # shellcheck disable=SC2206 # intentional word-split of space-separated IDs
  IDS=(${FAIL_GROUPS[$GROUP]})
fi
if [ "${#IDS[@]}" -eq 0 ]; then
  echo "ERROR: nothing to run. Pass -g <group> or one or more scenario IDs." >&2
  echo "       Use -l to list groups." >&2
  exit 2
fi

SCENARIOS=()
for id in "${IDS[@]}"; do
  while IFS= read -r f; do SCENARIOS+=("$f"); done < <(
    cd "$SCENARIOS_DIR" && ls "${id}"*.test.sh 2>/dev/null | sort
  )
done
if [ "${#SCENARIOS[@]}" -eq 0 ]; then
  echo "ERROR: no scenarios matched ids: ${IDS[*]}" >&2
  exit 2
fi

# Dedupe while preserving order (a scenario could be in multiple groups).
declare -A _seen
UNIQ=()
for s in "${SCENARIOS[@]}"; do
  if [ -z "${_seen[$s]+x}" ]; then
    _seen[$s]=1
    UNIQ+=("$s")
  fi
done
SCENARIOS=("${UNIQ[@]}")

printf 'run-subset: %d scenario(s)%s\n' "${#SCENARIOS[@]}" \
  "${GROUP:+ from group $GROUP}"
printf '  %s\n' "${SCENARIOS[@]}"

# --- run (mirrors run.sh's per-scenario loop exactly) --------------------
RUN_START=$(date +%s)
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
