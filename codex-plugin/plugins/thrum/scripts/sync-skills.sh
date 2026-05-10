#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../../.." && pwd)"

"${REPO_ROOT}/scripts/sync-skills.sh"
"${SCRIPT_DIR}/install-skills.sh" --force
