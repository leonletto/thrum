#!/usr/bin/env bash
# tests/release/behavioral/run-behavioral.sh — entry-point runner.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
HELPERS_DIR="${REPO_ROOT}/tests/release/helpers"
CARDS_DIR="${SCRIPT_DIR}/cards"
RESULTS_DIR_DEFAULT="${REPO_ROOT}/dev-docs/behavioral"

# CLI defaults
RUNTIME="claude"
declare -A PREAMBLES=()
FILTER="*.yaml"
NO_AUTO_DIAGNOSE=0
CAPTURE=""
COMPARE=""
RESULTS_DIR="${THRUM_BEHAVIORAL_RESULTS_DIR:-$RESULTS_DIR_DEFAULT}"

usage() {
  cat <<'USAGE'
Usage: run-behavioral.sh [options]
  --runtime=<name>            runtime to test (default: claude)
  --preamble=<role>:<path>    candidate preamble for a role (repeatable)
  --filter=<glob>             card-file glob (default: *.yaml)
  --no-auto-diagnose          disable LLM auto-diagnose on failed steps
  --capture <name>            capture-mode: save baseline to baselines/<name>/
  --compare <name>            compare-mode: score against baselines/<name>/
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --runtime=*) RUNTIME="${1#--runtime=}"; shift ;;
    --preamble=*)
      val="${1#--preamble=}"
      role="${val%%:*}"
      path="${val#*:}"
      PREAMBLES[$role]="$path"
      shift
      ;;
    --filter=*) FILTER="${1#--filter=}"; shift ;;
    --no-auto-diagnose) NO_AUTO_DIAGNOSE=1; shift ;;
    --capture) CAPTURE="$2"; shift 2 ;;
    --compare) COMPARE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Preflight
for tool in thrum tmux jq yq git "$RUNTIME"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool '$tool' not found in PATH" >&2
    exit 2
  fi
done
yq_v="$(yq --version 2>&1 | head -1)"
if ! { grep -q 'mikefarah' <<<"$yq_v" && grep -q -E 'v?4\.' <<<"$yq_v"; }; then
  echo "ERROR: incompatible yq ('$yq_v'); need mikefarah/yq v4+" >&2
  exit 2
fi

source "${HELPERS_DIR}/all.sh"          # paths.sh, drive.sh, assert.sh, output.sh
source "${HELPERS_DIR}/ephemeral-daemon.sh"
source "${HELPERS_DIR}/render-preamble.sh"
source "${HELPERS_DIR}/behavioral.sh"
# Intentionally NOT sourcing setup-repo.sh / teardown.sh: those wire up
# the scenarios fixture (fixed coord/impl panes via tmux-exec). The
# behavioral harness uses the ephemeral-daemon helper instead.

# Fixture lifecycle (one fixture for the whole run)
RUN_TIMESTAMP="$(date -u +%Y-%m-%dT%H-%M-%S)"
SHORT_SHA() { echo "$1" | cut -c1-8; }
COORD_SHA="baseline"
IMPL_SHA="baseline"
if [[ -n "${PREAMBLES[coordinator]:-}" ]]; then
  COORD_SHA="$(SHORT_SHA "$(shasum -a 256 "${PREAMBLES[coordinator]}" | awk '{print $1}')")"
fi
if [[ -n "${PREAMBLES[implementer]:-}" ]]; then
  IMPL_SHA="$(SHORT_SHA "$(shasum -a 256 "${PREAMBLES[implementer]}" | awk '{print $1}')")"
fi
RUN_DIR="${RESULTS_DIR}/runs/${RUN_TIMESTAMP}-${RUNTIME}-coord:${COORD_SHA}_impl:${IMPL_SHA}"
mkdir -p "$RUN_DIR"

FIXTURE_BASE="$(mktemp -d -t behavioral-fixture-XXXXXX)"
trap 'ephemeral_daemon_stop; rm -rf "$FIXTURE_BASE"' EXIT
if ! ephemeral_daemon_start "$FIXTURE_BASE"; then
  echo "ERROR: ephemeral-daemon setup failed" >&2
  exit 2
fi
# ephemeral_daemon_start exports FIXTURE_REPO, FIXTURE_THRUM, FIXTURE_WORKSPACES
# and patches FIXTURE_THRUM/config.json's worktrees.base_path so coord-spawned
# worktrees land at FIXTURE_WORKSPACES/<name> (not nested under FIXTURE_REPO).
export RUNTIME

# Seed all roles from project baseline first
thrum --repo "$FIXTURE_REPO" roles deploy >/dev/null 2>&1 || true

# Then overwrite swapped roles with candidates
for role in "${!PREAMBLES[@]}"; do
  src="${PREAMBLES[$role]}"
  if [[ ! -f "$src" ]]; then
    echo "ERROR: preamble file missing: $src" >&2
    exit 2
  fi
  render_preamble \
    --role "$role" \
    --src "$src" \
    --agent-name "test_${role}" \
    --module main \
    --worktree "$FIXTURE_REPO" \
    --coordinator-name test_coordinator \
    --repo-root "$FIXTURE_REPO"
  export "PREAMBLE_$(echo "$role" | tr 'a-z' 'A-Z')=$src"
done

# Validate and run each card matching --filter
shopt -s nullglob
cards=("${CARDS_DIR}"/${FILTER})
if [[ ${#cards[@]} -eq 0 ]]; then
  echo "ERROR: no cards matched filter '$FILTER'" >&2
  exit 2
fi

total_pass=0 total_fail=0
for card in "${cards[@]}"; do
  bash "${SCRIPT_DIR}/validate-card.sh" "$card" || exit 2
  test_id="$(yq -r '.id' "$card")"
  out="${RUN_DIR}/${test_id}.jsonl"
  echo "==> ${test_id}"
  if behavioral_run_card "$card" "$out"; then
    pass=1
    total_pass=$((total_pass+1))
  else
    pass=0
    total_fail=$((total_fail+1))
  fi
  # Print brief per-test summary
  yq_summary="$(grep '"step":"__summary__"' "$out" | tail -1 || true)"
  echo "    ${yq_summary}"
done

echo ""
echo "Run complete. Results: ${RUN_DIR}"
echo "Tests passed: ${total_pass}, failed: ${total_fail}"
[[ $total_fail -eq 0 ]] || exit 1
