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
CLAUDE_PROJECT_RESOURCES="${CLAUDE_SKILLS}/project-setup/resources"

sync_tree() {
  local src="$1"
  local dst="$2"

  rm -rf "${dst}"
  mkdir -p "$(dirname "${dst}")"
  cp -R "${src}" "${dst}"
  rm -f "${dst}/CLAUDE.md"
}

extract_frontmatter_value() {
  local file="$1"
  local key="$2"

  awk -v key="${key}" '
    NR == 1 && /^---$/ { in_frontmatter = 1; next }
    in_frontmatter && /^---$/ { exit }
    in_frontmatter && $0 ~ ("^" key ":") {
      line = $0
      sub("^" key ":[[:space:]]*", "", line)
      gsub(/^"/, "", line)
      gsub(/"$/, "", line)
      if (length(line) > 0) {
        print line
        exit
      }
      while (getline > 0) {
        if ($0 ~ /^---$/) {
          break
        }
        if ($0 !~ /^[[:space:]]+/) {
          break
        }
        gsub(/^[[:space:]]+/, "", $0)
        value = value (value ? " " : "") $0
      }
      print value
      exit
    }
  ' "${file}"
}

strip_frontmatter() {
  awk '
    NR == 1 && /^---$/ { in_frontmatter = 1; next }
    in_frontmatter && /^---$/ { in_frontmatter = 0; next }
    !in_frontmatter { print }
  ' "$1"
}

normalize_headings() {
  awk '
    function hashes(count, out, i) {
      out = ""
      for (i = 0; i < count; i++) {
        out = out "#"
      }
      return out
    }

    {
      lines[NR] = $0
      if ($0 ~ /^```/) {
        in_fence = !in_fence
      }
      if (!in_fence && $0 ~ /^#{1,6}[[:space:]]+/) {
        match($0, /^#+/)
        level = RLENGTH
        if (min_level == 0 || level < min_level) {
          min_level = level
        }
      }
    }

    END {
      if (min_level == 0) {
        min_level = 2
      }
      delta = 2 - min_level
      in_fence = 0

      for (i = 1; i <= NR; i++) {
        line = lines[i]
        if (line ~ /^```/) {
          in_fence = !in_fence
          print line
          continue
        }

        if (!in_fence && line ~ /^#{1,6}[[:space:]]+/) {
          match(line, /^#+/)
          level = RLENGTH
          new_level = level + delta
          if (new_level < 1) {
            new_level = 1
          }
          if (new_level > 6) {
            new_level = 6
          }
          print hashes(new_level) substr(line, level + 1)
          continue
        }

        print line
      }
    }
  '
}

title_case() {
  printf '%s' "$1" | awk -F- '
    {
      for (i = 1; i <= NF; i++) {
        $i = toupper(substr($i, 1, 1)) substr($i, 2)
      }
      OFS = " "
      print $0
    }
  '
}

write_openai_metadata() {
  local skill_dir="$1"
  local display_name="$2"
  local short_description="$3"
  local default_prompt="$4"

  mkdir -p "${skill_dir}/agents"
  cat > "${skill_dir}/agents/openai.yaml" <<EOF
interface:
  display_name: "${display_name}"
  short_description: "${short_description}"
  default_prompt: "${default_prompt}"
EOF
}

generate_codex_command_skill() {
  local command_file="$1"
  local codex_skills_dir="$2"
  local command_name skill_name skill_dir command_title description body_file

  command_name="$(basename "${command_file}" .md)"
  skill_name="thrum-${command_name}"
  skill_dir="${codex_skills_dir}/${skill_name}"
  command_title="$(title_case "${command_name}")"
  description="$(extract_frontmatter_value "${command_file}" "description")"
  [[ -n "${description}" ]] || description="Run the thrum ${command_name} workflow"

  rm -rf "${skill_dir}"
  mkdir -p "${skill_dir}"

  body_file="$(mktemp)"
  strip_frontmatter "${command_file}" | normalize_headings > "${body_file}"

  cat > "${skill_dir}/SKILL.md" <<EOF
---
name: ${skill_name}
description: ${description}
# source: claude-plugin/commands/${command_name}.md
# generated-by: scripts/sync-skills.sh
---

# Thrum ${command_title}

Use this skill when the user explicitly wants the \`${command_name}\` Thrum
workflow. Prefer the umbrella \`thrum\` skill when the request spans multiple
commands or needs broader coordination judgment.

$(cat "${body_file}")
EOF

  write_openai_metadata \
    "${skill_dir}" \
    "Thrum ${command_title}" \
    "${description}" \
    "Run the thrum ${command_name} workflow and summarize the result."

  rm -f "${body_file}"
  echo "  generated skill ${skill_name}"
}

# ─── Codex target ───────────────────────────────────────────────────────────

sync_codex() {
  local CODEX_PLUGIN="${REPO_ROOT}/codex-plugin"
  local CODEX_SKILLS="${CODEX_PLUGIN}/skills"
  local THRUM_RESOURCES="${CODEX_SKILLS}/thrum/resources"
  local PROJECT_RESOURCES="${CODEX_SKILLS}/thrum-project-setup/resources"

  if [[ ! -d "${CODEX_PLUGIN}" ]]; then
    echo "skip: codex-plugin/ not found"
    return
  fi

  echo "── codex-plugin ──"

  rm -rf \
    "${CODEX_SKILLS}/thrum-core" \
    "${CODEX_SKILLS}/thrum-ops" \
    "${CODEX_SKILLS}/thrum-role-config" \
    "${CODEX_SKILLS}/project-setup"
  rm -f "${CODEX_SKILLS}/update-project.md"

  if [[ -d "${CLAUDE_RESOURCES}" ]]; then
    mkdir -p "${CODEX_SKILLS}/thrum"
    sync_tree "${CLAUDE_RESOURCES}" "${THRUM_RESOURCES}"
    echo "  synced thrum resources -> thrum/resources/"
  fi

  if [[ -d "${CLAUDE_PROJECT_RESOURCES}" ]]; then
    mkdir -p "${CODEX_SKILLS}/thrum-project-setup"
    sync_tree "${CLAUDE_PROJECT_RESOURCES}" "${PROJECT_RESOURCES}"
    echo "  synced project resources -> thrum-project-setup/resources/"
  fi

  if [[ -d "${CLAUDE_COMMANDS}" ]]; then
    for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
      generate_codex_command_skill "${cmd_file}" "${CODEX_SKILLS}"
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
