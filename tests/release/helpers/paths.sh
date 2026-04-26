#!/usr/bin/env bash
# tests/release/helpers/paths.sh — JSONL transcript discovery for a given repo cwd.
# Port of internal/restart/restart.go:encodeCwd + FindLatestJSONLForCwd.

# encode_cwd <abs-path> → encoded project dir name
# Mirrors Claude Code's project-dir naming: leading "/" stripped, then "/" and
# "." both replaced with "-", then a leading "-" prepended.
#   /Users/leon/dev/project       → -Users-leon-dev-project
#   $HOME/.thrum_release_tests/x  → -Users-leon--thrum_release_tests-x
encode_cwd() {
  local cwd="$1"
  cwd="${cwd#/}"
  cwd="${cwd//\//-}"
  cwd="${cwd//./-}"
  printf '%s' "-${cwd}"
}

# jsonl_for_repo <repo-abs-path> → newest .jsonl file path (empty + exit 1 if none)
# Polls up to ~3s for a flush — claude buffers transcript writes.
jsonl_for_repo() {
  local repo="$1"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local jsonl=""
  for _ in 1 2 3; do
    if [ -d "$project_dir" ]; then
      jsonl=$(ls -t "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      if [ -n "$jsonl" ] && [ -s "$jsonl" ]; then
        printf '%s' "$jsonl"
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}
