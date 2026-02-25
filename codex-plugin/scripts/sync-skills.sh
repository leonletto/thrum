#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SRC_DIR="${PLUGIN_ROOT}/skills"
DEST_ROOT="${CODEX_HOME:-${HOME}/.codex}"
DEST_DIR="${DEST_ROOT}/skills"

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--dest DIR]

Sync Codex skills from codex-plugin/skills into ~/.codex/skills.
Existing skills in destination are replaced with local versions.

Options:
  --dest DIR   Destination skills directory (default: \$CODEX_HOME/skills or ~/.codex/skills)
  -h, --help   Show this help text
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dest)
      DEST_DIR="$2"
      shift 2
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

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "Source skills directory not found: ${SRC_DIR}" >&2
  exit 1
fi

mkdir -p "${DEST_DIR}"

synced=0
for skill_path in "${SRC_DIR}"/*; do
  [[ -d "${skill_path}" ]] || continue
  skill_name="$(basename "${skill_path}")"
  target="${DEST_DIR}/${skill_name}"

  rm -rf "${target}"
  cp -R "${skill_path}" "${target}"
  echo "synced ${skill_name} -> ${target}"
  synced=$((synced + 1))
done

echo "done: synced=${synced} dest=${DEST_DIR}"
