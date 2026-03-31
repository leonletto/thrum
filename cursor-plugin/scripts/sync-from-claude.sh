#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${PLUGIN_ROOT}/.." && pwd)"

CLAUDE_RESOURCES="${REPO_ROOT}/claude-plugin/skills/thrum/resources"
CLAUDE_COMMANDS="${REPO_ROOT}/claude-plugin/commands"

mkdir -p "${PLUGIN_ROOT}/skills/thrum-core/references"
mkdir -p "${PLUGIN_ROOT}/skills/thrum-ops/references"

cp "${CLAUDE_RESOURCES}/CLI_REFERENCE.md" "${PLUGIN_ROOT}/skills/thrum-ops/references/CLI_REFERENCE.md"

for res_file in "${CLAUDE_RESOURCES}"/*.md; do
  fname="$(basename "${res_file}")"
  if [[ "${fname}" == "CLI_REFERENCE.md" ]]; then
    continue
  fi
  cp "${res_file}" "${PLUGIN_ROOT}/skills/thrum-core/references/${fname}"
done

for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
  cp "${cmd_file}" "${PLUGIN_ROOT}/skills/thrum-ops/references/$(basename "${cmd_file}")"
done

cp "${REPO_ROOT}/codex-plugin/skills/thrum-core/references/MESSAGE_LISTENER_AGENT.md" \
  "${PLUGIN_ROOT}/skills/thrum-core/references/MESSAGE_LISTENER_AGENT.md"

echo "cursor-plugin reference sync complete"
