#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SRC_DIR="${PLUGIN_ROOT}/skills"
DEST_DIR="${HOME}/.agents/skills"
FORCE=0
LEGACY_SKILLS=(
  thrum-core
  thrum-ops
  thrum-role-config
  project-setup
  configure-roles
)

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--dest DIR] [--force]

Install Codex skills from codex-plugin/skills into ~/.agents/skills.

Options:
  --dest DIR   Destination skills directory (default: \$HOME/.agents/skills)
  --force      Replace an existing installed skill
  -h, --help   Show this help text
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dest)
      DEST_DIR="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -n "${CODEX_HOME:-}" && "${CODEX_HOME}" != "${HOME}/.codex" ]]; then
  echo "warning: CODEX_HOME is set to '${CODEX_HOME}' but is no longer honored." >&2
  echo "         Skills install to \$HOME/.agents/skills as of codex v0.130.0." >&2
  echo "         Pass --dest \"\$CODEX_HOME/skills\" to restore the old behavior." >&2
fi

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "Source skills directory not found: ${SRC_DIR}" >&2
  exit 1
fi

mkdir -p "${DEST_DIR}"

if [[ ${FORCE} -eq 1 ]]; then
  for legacy_skill in "${LEGACY_SKILLS[@]}"; do
    legacy_target="${DEST_DIR}/${legacy_skill}"
    if [[ -e "${legacy_target}" ]]; then
      rm -rf "${legacy_target}"
      echo "removed legacy skill ${legacy_skill}"
    fi
  done
fi

installed=0
skipped=0
for skill_path in "${SRC_DIR}"/*; do
  [[ -d "${skill_path}" ]] || continue
  skill_name="$(basename "${skill_path}")"
  target="${DEST_DIR}/${skill_name}"

  if [[ -e "${target}" && ${FORCE} -ne 1 ]]; then
    echo "skip ${skill_name}: already exists (${target})"
    skipped=$((skipped + 1))
    continue
  fi

  rm -rf "${target}"
  cp -R "${skill_path}" "${target}"
  echo "installed ${skill_name} -> ${target}"
  installed=$((installed + 1))
done

echo "done: installed=${installed} skipped=${skipped} dest=${DEST_DIR}"
