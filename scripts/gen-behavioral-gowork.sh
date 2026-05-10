#!/usr/bin/env bash
# scripts/gen-behavioral-gowork.sh — generate the gitignored go.work that
# resolves the `git.local/llmclient` placeholder import to LLM_CLIENT_PATH
# from .env. Idempotent: re-runs overwrite go.work cleanly.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Locate .env at the *main* repo root so this works from feature worktrees
# too. `git rev-parse --git-common-dir` returns the shared .git directory
# (the main repo's .git, even when called from a worktree), so its parent
# is the main repo root.
MAIN_REPO_ROOT="$(cd "${REPO_ROOT}" && cd "$(git rev-parse --git-common-dir)/.." && pwd)"
ENV_FILE="${MAIN_REPO_ROOT}/.env"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: .env not found at main repo root: $ENV_FILE" >&2
  exit 2
fi
# shellcheck disable=SC1091
set -a; source "$ENV_FILE"; set +a

if [[ -z "${LLM_CLIENT_PATH:-}" ]]; then
  echo "ERROR: LLM_CLIENT_PATH not set in .env" >&2
  exit 2
fi
if [[ ! -d "${LLM_CLIENT_PATH}" ]]; then
  echo "ERROR: LLM_CLIENT_PATH does not exist: ${LLM_CLIENT_PATH}" >&2
  exit 2
fi
if [[ ! -f "${LLM_CLIENT_PATH}/go.mod" ]]; then
  echo "ERROR: no go.mod in ${LLM_CLIENT_PATH}" >&2
  exit 2
fi

# Determine the upstream module name from its go.mod (line 1 'module <name>')
UPSTREAM_MODULE="$(awk '/^module /{print $2; exit}' "${LLM_CLIENT_PATH}/go.mod")"
if [[ -z "$UPSTREAM_MODULE" ]]; then
  echo "ERROR: could not parse module name from ${LLM_CLIENT_PATH}/go.mod" >&2
  exit 2
fi

cat > "${REPO_ROOT}/go.work" <<EOF
go 1.26

use ./tests/release/cmd/llm-judge

replace git.local/llmclient => ${LLM_CLIENT_PATH}
replace ${UPSTREAM_MODULE} => ${LLM_CLIENT_PATH}
EOF

echo "Wrote ${REPO_ROOT}/go.work pointing at ${LLM_CLIENT_PATH}"
