#!/usr/bin/env bash
set -euo pipefail

# Unified skill and command sync script.
# Source of truth: claude-plugin/
# Targets: codex-plugin/, opencode-plugin/, cursor-plugin/
#
# Run before release to keep all runtime plugins in sync.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

CLAUDE_PLUGIN="${REPO_ROOT}/claude-plugin"
CLAUDE_SKILLS="${CLAUDE_PLUGIN}/skills"
CLAUDE_COMMANDS="${CLAUDE_PLUGIN}/commands"
CLAUDE_RESOURCES="${CLAUDE_SKILLS}/thrum/resources"

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

replace_claude_skill_syntax() {
  # Rewrite claude-specific skill-invocation syntax (/thrum:foo) to codex's
  # flat-skill form ($thrum-foo). Operates on a single file in place.
  # Spec ref: dev-docs/specs/codex-plugin-first-class.md anti-pattern #1.
  local file="$1"
  [[ -f "${file}" ]] || return 0
  local tmp
  tmp="$(mktemp)"
  sed 's|/thrum:\([a-z][a-z0-9-]*\)|$thrum-\1|g' "${file}" > "${tmp}"
  mv "${tmp}" "${file}"
}

adapt_codex_skill() {
  local src_skill_dir="$1"
  local dst_skill_dir="$2"
  local display_name="$3"
  local default_prompt="$4"
  local skill_name src_skill_md description body_file

  src_skill_md="${src_skill_dir}/SKILL.md"
  if [[ ! -f "${src_skill_md}" ]]; then
    echo "  skip: ${src_skill_dir} has no SKILL.md" >&2
    return
  fi

  skill_name="$(basename "${src_skill_dir}")"

  rm -rf "${dst_skill_dir}"
  mkdir -p "${dst_skill_dir}/agents"

  description="$(extract_frontmatter_value "${src_skill_md}" "description")"
  [[ -n "${description}" ]] || description="${display_name}"

  body_file="$(mktemp)"
  cat > "${body_file}" <<EOF
---
name: ${skill_name}
description: "${description//\"/\\\"}"
# source: claude-plugin/skills/${skill_name}/SKILL.md
# generated-by: scripts/sync-skills.sh
---

EOF
  strip_frontmatter "${src_skill_md}" | normalize_headings >> "${body_file}"

  mv "${body_file}" "${dst_skill_dir}/SKILL.md"
  replace_claude_skill_syntax "${dst_skill_dir}/SKILL.md"

  if [[ -d "${src_skill_dir}/resources" ]]; then
    sync_tree "${src_skill_dir}/resources" "${dst_skill_dir}/resources"
    while IFS= read -r -d '' rfile; do
      replace_claude_skill_syntax "${rfile}"
    done < <(find "${dst_skill_dir}/resources" -type f -print0)
  fi

  write_openai_metadata \
    "${dst_skill_dir}" \
    "${display_name}" \
    "${description}" \
    "${default_prompt}"

  echo "  adapted skill ${skill_name}"
}

# ─── Codex target ───────────────────────────────────────────────────────────

sync_codex() {
  # codex-plugin layout (matches openai-bundled marketplace shape):
  # codex-plugin/.agents/plugins/marketplace.json points at ./plugins/thrum.
  # All skills/hooks/scripts/manifest live under codex-plugin/plugins/thrum/.
  local CODEX_MARKETPLACE="${REPO_ROOT}/codex-plugin"
  local CODEX_PLUGIN="${CODEX_MARKETPLACE}/plugins/thrum"
  local CODEX_SKILLS="${CODEX_PLUGIN}/skills"
  local THRUM_RESOURCES="${CODEX_SKILLS}/thrum/resources"

  if [[ ! -d "${CODEX_PLUGIN}" ]]; then
    echo "skip: codex-plugin/plugins/thrum/ not found"
    return
  fi

  echo "── codex-plugin ──"

  # Legacy directories from earlier naming schemes — adapted skills now use
  # the canonical (unprefixed) names sourced from claude-plugin/skills/.
  rm -rf \
    "${CODEX_SKILLS}/thrum-core" \
    "${CODEX_SKILLS}/thrum-ops" \
    "${CODEX_SKILLS}/thrum-role-config" \
    "${CODEX_SKILLS}/thrum-configure-roles" \
    "${CODEX_SKILLS}/thrum-project-setup" \
    "${CODEX_SKILLS}/project-setup"
  rm -f "${CODEX_SKILLS}/update-project.md"

  if [[ -d "${CLAUDE_RESOURCES}" ]]; then
    mkdir -p "${CODEX_SKILLS}/thrum"
    sync_tree "${CLAUDE_RESOURCES}" "${THRUM_RESOURCES}"
    while IFS= read -r -d '' rfile; do
      replace_claude_skill_syntax "${rfile}"
    done < <(find "${THRUM_RESOURCES}" -type f -print0)
    echo "  synced thrum resources -> thrum/resources/"
  fi

  if [[ -d "${CLAUDE_COMMANDS}" ]]; then
    for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
      generate_codex_command_skill "${cmd_file}" "${CODEX_SKILLS}"
    done
  fi

  # Parity list: claude-plugin skills that aren't command-derived. Each entry:
  #   <src-skill-name>|<display-name>|<default-prompt>
  # The adapt loop drops the legacy thrum- prefix and produces SKILL.md +
  # agents/openai.yaml + resources/ (when present in source).
  local PARITY_SKILLS=(
    "adversarial-critique|Adversarial Critique|Critique a plan or proposal adversarially before agreeing."
    "configure-roles|Configure Roles|Configure thrum role templates for this project."
    "coordinator-context-monitoring|Coordinator: Context Monitoring|Use when managing live agents during a long session to pre-empt context-limit blow-ups."
    "coordinator-dispatching-work|Coordinator: Dispatching Work|Use before dispatching work to an implementer."
    "coordinator-managing-state-and-lifecycle|Coordinator: Managing State|Use when managing agent lifecycle and project state."
    "coordinator-post-restart-sweep|Coordinator: Post-Restart Sweep|Use after returning from a restart, compaction, or extended absence to detect agents blocked waiting for a decision."
    "coordinator-running-brainstorm-cycles|Coordinator: Brainstorm Cycles|Use when starting a brainstorm for a bug fix, feature, or architectural decision the coordinator can't trivially decide alone."
    "coordinator-running-review-cycles|Coordinator: Review Cycles|Use when running parallel review cycles on agent branches."
    "efficient-multi-agent-research|Efficient Multi-Agent Research|Use when delegating research across multiple agents."
    "implementer-receiving-dispatch|Implementer: Receiving Dispatch|Use when picking up a new implementation task."
    "implementer-receiving-review-feedback|Implementer: Receiving Review Feedback|Use when consolidating and addressing review findings."
    "implementer-status-and-handoff|Implementer: Status & Handoff|Use when reporting status or handing off work."
    "implementer-tdd-and-quality|Implementer: TDD & Quality|Use when writing or modifying code with quality discipline."
    "project-philosophy|Project Philosophy|Use when philosophical/architectural decisions need framing."
    "project-setup|Project Setup|Use when setting up a new thrum project."
    "researcher-answering-queries|Researcher: Answering Queries|Use when fielding a research request from another agent."
    "researcher-investigating|Researcher: Investigating|Use when starting an investigation or exploration task."
    "researcher-maintaining-memory|Researcher: Maintaining Memory|Use after research to update memory and indexes."
    "verify-against-plan|Verify Against Plan|Use when verifying an implementation against a spec."
    "verify-against-source|Verify Against Source|Use when verifying a prose artifact (brainstorm, spec, plan) honors its source artifact."
  )

  local entry skill_name display_name default_prompt src dst
  for entry in "${PARITY_SKILLS[@]}"; do
    IFS='|' read -r skill_name display_name default_prompt <<< "${entry}"
    src="${CLAUDE_SKILLS}/${skill_name}"
    dst="${CODEX_SKILLS}/${skill_name}"
    if [[ -d "${src}" ]]; then
      adapt_codex_skill "${src}" "${dst}" "${display_name}" "${default_prompt}"
    else
      echo "  skip: ${skill_name} (no source at ${src})" >&2
    fi
  done

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

# ─── Cursor target ─────────────────────────────────────────────────────────

sync_cursor() {
  local CURSOR_PLUGIN="${REPO_ROOT}/cursor-plugin"
  local CURSOR_SKILLS="${CURSOR_PLUGIN}/skills"
  local CURSOR_COMMANDS="${CURSOR_PLUGIN}/commands"
  local CURSOR_AGENTS="${CURSOR_PLUGIN}/agents"

  if [[ ! -d "${CURSOR_PLUGIN}" ]]; then
    echo "skip: cursor-plugin/ not found"
    return
  fi

  echo "── cursor-plugin ──"

  # Skills: copy each skill directory from claude-plugin
  if [[ -d "${CLAUDE_SKILLS}" ]]; then
    mkdir -p "${CURSOR_SKILLS}"
    for skill_path in "${CLAUDE_SKILLS}"/*; do
      [[ -d "${skill_path}" ]] || continue
      skill_name="$(basename "${skill_path}")"
      target="${CURSOR_SKILLS}/${skill_name}"
      rm -rf "${target}"
      cp -R "${skill_path}" "${target}"
      rm -f "${target}/CLAUDE.md"
      # Strip allowed-tools from SKILL.md (Cursor doesn't enforce it).
      # Must handle multi-line values (e.g., key on one line, quoted value on next).
      # Use awk instead of sed for portability (no sed -i '' macOS vs Linux issue).
      if [[ -f "${target}/SKILL.md" ]]; then
        awk '
          /^allowed-tools:/ { skip = 1; next }
          skip && /^[[:space:]]/ { next }
          { skip = 0; print }
        ' "${target}/SKILL.md" > "${target}/SKILL.md.tmp"
        mv "${target}/SKILL.md.tmp" "${target}/SKILL.md"
      fi
      echo "  synced skill ${skill_name}"
    done
  fi

  # Commands: copy directly (same format as claude-plugin)
  if [[ -d "${CLAUDE_COMMANDS}" ]]; then
    mkdir -p "${CURSOR_COMMANDS}"
    rm -f "${CURSOR_COMMANDS}"/*.md
    for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
      cp "${cmd_file}" "${CURSOR_COMMANDS}/"
      echo "  synced command $(basename "${cmd_file}")"
    done
  fi

  # Agents: copy directly
  local CLAUDE_AGENTS="${CLAUDE_PLUGIN}/agents"
  if [[ -d "${CLAUDE_AGENTS}" ]]; then
    mkdir -p "${CURSOR_AGENTS}"
    rm -f "${CURSOR_AGENTS}"/*.md
    for agent_file in "${CLAUDE_AGENTS}"/*.md; do
      [[ -f "${agent_file}" ]] || continue
      cp "${agent_file}" "${CURSOR_AGENTS}/"
      echo "  synced agent $(basename "${agent_file}")"
    done
  fi

  echo "  cursor sync complete"
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
sync_cursor

echo ""
echo "done"
