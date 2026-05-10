#!/usr/bin/env bash
# tests/release/helpers/render-preamble.sh — substitutes role-template
# tokens in a candidate preamble file and writes it into the fixture's
# main repo .thrum/role_templates/ directory.
#
# IMPORTANT: writes to ${FIXTURE_REPO}/.thrum/role_templates/, NOT a
# worktree's .thrum/. The redirect in worktree .thrum/ resolves agents
# back to the main repo, so this is the path that has effect.

render_preamble() {
  local role="" src="" agent_name="" module="" worktree="" coordinator_name="" repo_root=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --role)              role="$2"; shift 2 ;;
      --src)               src="$2"; shift 2 ;;
      --agent-name)        agent_name="$2"; shift 2 ;;
      --module)            module="$2"; shift 2 ;;
      --worktree)          worktree="$2"; shift 2 ;;
      --coordinator-name)  coordinator_name="$2"; shift 2 ;;
      --repo-root)         repo_root="$2"; shift 2 ;;
      *) echo "render-preamble: unknown arg $1" >&2; return 2 ;;
    esac
  done

  for required in role src; do
    if [[ -z "${!required}" ]]; then
      echo "render-preamble: missing --$required" >&2
      return 2
    fi
  done

  if [[ -z "${FIXTURE_REPO:-}" ]]; then
    echo "render-preamble: FIXTURE_REPO env var not set" >&2
    return 2
  fi
  if [[ ! -f "$src" ]]; then
    echo "render-preamble: src file missing: $src" >&2
    return 2
  fi

  local dst_dir="${FIXTURE_REPO}/.thrum/role_templates"
  mkdir -p "$dst_dir"
  local dst="${dst_dir}/${role}.md"

  # Use sed with safe delimiter (|) since paths contain /. Escape | in values.
  local an="${agent_name//|/\\|}"
  local mo="${module//|/\\|}"
  local wt="${worktree//|/\\|}"
  local cn="${coordinator_name//|/\\|}"
  local rr="${repo_root//|/\\|}"

  sed \
    -e "s|{{\.AgentName}}|${an}|g" \
    -e "s|{{\.Module}}|${mo}|g" \
    -e "s|{{\.WorktreePath}}|${wt}|g" \
    -e "s|{{\.CoordinatorName}}|${cn}|g" \
    -e "s|{{\.RepoRoot}}|${rr}|g" \
    "$src" > "$dst"

  echo "render-preamble: wrote $dst (role=$role, src=$src)" >&2
}
