#!/usr/bin/env bash
set -euo pipefail

# Unified skill and command sync script.
# Source of truth: claude-plugin/
# Targets: codex-plugin/, opencode-plugin/
#
# Run before release to keep all runtime plugins in sync.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

CLAUDE_PLUGIN="${REPO_ROOT}/claude-plugin"
CLAUDE_SKILLS="${CLAUDE_PLUGIN}/skills"
CLAUDE_COMMANDS="${CLAUDE_PLUGIN}/commands"
CLAUDE_RESOURCES="${CLAUDE_SKILLS}/thrum/resources"

# ─── Codex target ───────────────────────────────────────────────────────────

sync_codex() {
  local CODEX_PLUGIN="${REPO_ROOT}/codex-plugin"
  local CODEX_SKILLS="${CODEX_PLUGIN}/skills"
  local OPS_REFS="${CODEX_SKILLS}/thrum-ops/references"
  local CORE_REFS="${CODEX_SKILLS}/thrum-core/references"

  if [[ ! -d "${CODEX_PLUGIN}" ]]; then
    echo "skip: codex-plugin/ not found"
    return
  fi

  echo "── codex-plugin ──"

  # Ops references: CLI_REFERENCE.md
  if [[ -d "${CLAUDE_RESOURCES}" ]]; then
    mkdir -p "${OPS_REFS}"
    cp "${CLAUDE_RESOURCES}/CLI_REFERENCE.md" "${OPS_REFS}/CLI_REFERENCE.md"
    echo "  synced CLI_REFERENCE.md -> thrum-ops/references/"
  fi

  # Core references: all resource files except CLI_REFERENCE.md
  if [[ -d "${CLAUDE_RESOURCES}" ]]; then
    mkdir -p "${CORE_REFS}"
    for res_file in "${CLAUDE_RESOURCES}"/*.md; do
      fname="$(basename "${res_file}")"
      [[ "${fname}" == "CLI_REFERENCE.md" ]] && continue
      cp "${res_file}" "${CORE_REFS}/${fname}"
      echo "  synced ${fname} -> thrum-core/references/"
    done
  fi

  # Commands: claude-plugin/commands/*.md -> thrum-ops/references/
  if [[ -d "${CLAUDE_COMMANDS}" ]]; then
    mkdir -p "${OPS_REFS}"
    for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
      fname="$(basename "${cmd_file}")"
      cp "${cmd_file}" "${OPS_REFS}/${fname}"
      echo "  synced commands/${fname} -> thrum-ops/references/"
    done
  fi

  echo "  codex sync complete"
}

# ─── Open Code target ───────────────────────────────────────────────────────

sync_opencode() {
  local OC_PLUGIN="${REPO_ROOT}/opencode-plugin"
  local OC_SKILLS="${OC_PLUGIN}/assets/skills"
  local OC_COMMANDS="${OC_PLUGIN}/assets/commands"

  if [[ ! -d "${OC_PLUGIN}" ]]; then
    echo "skip: opencode-plugin/ not found"
    return
  fi

  echo "── opencode-plugin ──"

  # Skills: copy each skill directory from claude-plugin/skills/
  if [[ -d "${CLAUDE_SKILLS}" ]]; then
    mkdir -p "${OC_SKILLS}"
    for skill_path in "${CLAUDE_SKILLS}"/*; do
      [[ -d "${skill_path}" ]] || continue
      skill_name="$(basename "${skill_path}")"
      target="${OC_SKILLS}/${skill_name}"
      rm -rf "${target}"
      cp -R "${skill_path}" "${target}"
      # Remove claude-specific maintenance files
      rm -f "${target}/CLAUDE.md"
      echo "  synced skill ${skill_name}"
    done
  fi

  # Commands: copy and rename with thrum- prefix
  if [[ -d "${CLAUDE_COMMANDS}" ]]; then
    mkdir -p "${OC_COMMANDS}"
    # Clean existing thrum-* commands
    rm -f "${OC_COMMANDS}"/thrum-*.md
    for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
      fname="$(basename "${cmd_file}")"
      target_name="thrum-${fname}"
      # Copy and strip argument-hint (Open Code uses $ARGUMENTS)
      sed '/^argument-hint:/d' "${cmd_file}" > "${OC_COMMANDS}/${target_name}"
      echo "  synced command ${fname} -> ${target_name}"
    done
  fi

  echo "  opencode sync complete"
}

# ─── Main ───────────────────────────────────────────────────────────────────

if [[ ! -d "${CLAUDE_PLUGIN}" ]]; then
  echo "error: claude-plugin/ not found at ${CLAUDE_PLUGIN}" >&2
  exit 1
fi

echo "Syncing skills and commands from claude-plugin/ to all runtime plugins..."
echo ""

sync_codex
echo ""
sync_opencode

echo ""
echo "done"
