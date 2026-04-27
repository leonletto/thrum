#!/usr/bin/env bash
# tests/release/helpers/paths.sh — encode_cwd helper.
# Port of internal/restart/restart.go:encodeCwd.

# encode_cwd <abs-path> → encoded project dir name
# Mirrors Claude Code's project-dir naming: leading "/" stripped, then "/", ".",
# and "_" all replaced with "-", then a leading "-" prepended.
#   /Users/leon/dev/project       → -Users-leon-dev-project
#   $HOME/.thrum_release_tests/x  → -Users-leon--thrum-release-tests-x
#
# Matches the Go reference at internal/restart/restart.go:188 which collapses
# the same three characters. Both must stay in sync — they're independent
# implementations of one Claude-Code-side convention.
encode_cwd() {
  local cwd="$1"
  cwd="${cwd#/}"
  cwd="${cwd//\//-}"
  cwd="${cwd//./-}"
  cwd="${cwd//_/-}"
  printf '%s' "-${cwd}"
}
