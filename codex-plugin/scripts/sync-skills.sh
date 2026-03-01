#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${PLUGIN_ROOT}/.." && pwd)"
SRC_DIR="${PLUGIN_ROOT}/skills"
DEST_ROOT="${CODEX_HOME:-${HOME}/.codex}"
DEST_DIR="${DEST_ROOT}/skills"

# claude-plugin source paths
CLAUDE_PLUGIN="${REPO_ROOT}/claude-plugin"
CLAUDE_RESOURCES="${CLAUDE_PLUGIN}/skills/thrum/resources"
CLAUDE_COMMANDS="${CLAUDE_PLUGIN}/commands"

usage() {
  cat <<USAGE
Usage: $(basename "$0") [--dest DIR] [--no-upstream]

Sync Codex skills from codex-plugin/skills into ~/.codex/skills.
Also pulls reference files from claude-plugin into codex-plugin/skills.
Existing skills in destination are replaced with local versions.

Options:
  --dest DIR      Destination skills directory (default: \$CODEX_HOME/skills or ~/.codex/skills)
  --no-upstream   Skip syncing reference files from claude-plugin
  -h, --help      Show this help text
USAGE
}

SYNC_UPSTREAM=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dest)
      DEST_DIR="$2"
      shift 2
      ;;
    --no-upstream)
      SYNC_UPSTREAM=false
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

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "Source skills directory not found: ${SRC_DIR}" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 1: Pull reference files from claude-plugin into codex-plugin/skills
# ---------------------------------------------------------------------------

# Ops-related resources (CLI reference) go to thrum-ops/references.
# Core-related resources (messaging, groups, identity, etc.) go to thrum-core/references.
# Commands go to thrum-ops/references.

OPS_REFS="${SRC_DIR}/thrum-ops/references"
CORE_REFS="${SRC_DIR}/thrum-core/references"

if [[ "${SYNC_UPSTREAM}" == true ]]; then
  if [[ ! -d "${CLAUDE_PLUGIN}" ]]; then
    echo "warning: claude-plugin not found at ${CLAUDE_PLUGIN}, skipping upstream sync" >&2
  else
    # -- Sync ops references: CLI_REFERENCE.md from claude-plugin resources --
    if [[ -d "${CLAUDE_RESOURCES}" ]]; then
      mkdir -p "${OPS_REFS}"
      cp "${CLAUDE_RESOURCES}/CLI_REFERENCE.md" "${OPS_REFS}/CLI_REFERENCE.md"
      echo "upstream: synced CLI_REFERENCE.md -> thrum-ops/references/"
    fi

    # -- Sync core references: all resource files except CLI_REFERENCE.md --
    if [[ -d "${CLAUDE_RESOURCES}" ]]; then
      mkdir -p "${CORE_REFS}"
      for res_file in "${CLAUDE_RESOURCES}"/*.md; do
        fname="$(basename "${res_file}")"
        if [[ "${fname}" == "CLI_REFERENCE.md" ]]; then
          continue  # ops-only
        fi
        cp "${res_file}" "${CORE_REFS}/${fname}"
        echo "upstream: synced ${fname} -> thrum-core/references/"
      done
    fi

    # -- Sync commands: claude-plugin/commands/*.md -> thrum-ops/references/ --
    if [[ -d "${CLAUDE_COMMANDS}" ]]; then
      mkdir -p "${OPS_REFS}"
      for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
        fname="$(basename "${cmd_file}")"
        cp "${cmd_file}" "${OPS_REFS}/${fname}"
        echo "upstream: synced commands/${fname} -> thrum-ops/references/"
      done
    fi

    echo "upstream: reference sync complete"
  fi
fi

# ---------------------------------------------------------------------------
# Step 2: Copy codex-plugin/skills into ~/.codex/skills
# ---------------------------------------------------------------------------

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
