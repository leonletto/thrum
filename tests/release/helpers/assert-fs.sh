#!/usr/bin/env bash
# tests/release/helpers/assert-fs.sh — filesystem predicates for the
# behavioral harness. Each function returns 0 on pass, non-zero on fail,
# and prints a single diagnostic line on fail.

assert_fs_dir_exists() {
  local path="$1"
  if [[ -d "$path" ]]; then return 0; fi
  echo "assert-fs.dir_exists: not a directory: $path" >&2
  return 1
}

assert_fs_file_exists() {
  local path="$1"
  if [[ -f "$path" ]]; then return 0; fi
  echo "assert-fs.file_exists: not a regular file: $path" >&2
  return 1
}

assert_fs_file_contains() {
  local path="$1" needle="$2"
  if [[ ! -f "$path" ]]; then
    echo "assert-fs.file_contains: file missing: $path" >&2
    return 1
  fi
  if grep -F -q -- "$needle" "$path"; then return 0; fi
  echo "assert-fs.file_contains: needle '$needle' not found in $path" >&2
  return 1
}

assert_fs_file_matches() {
  local path="$1" regex="$2"
  if [[ ! -f "$path" ]]; then
    echo "assert-fs.file_matches: file missing: $path" >&2
    return 1
  fi
  if grep -E -q -- "$regex" "$path"; then return 0; fi
  echo "assert-fs.file_matches: regex '$regex' did not match in $path" >&2
  return 1
}
