#!/usr/bin/env bash
# tests/release/helpers/test-assert-fs.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/assert-fs.sh"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$WORK_DIR/sub"
echo "hello world" > "$WORK_DIR/sub/file.txt"

# dir_exists
assert_fs_dir_exists "$WORK_DIR/sub"      || { echo "FAIL: dir_exists positive"; exit 1; }
! assert_fs_dir_exists "$WORK_DIR/missing" || { echo "FAIL: dir_exists negative"; exit 1; }

# file_exists
assert_fs_file_exists "$WORK_DIR/sub/file.txt" || { echo "FAIL: file_exists positive"; exit 1; }
! assert_fs_file_exists "$WORK_DIR/sub"        || { echo "FAIL: file_exists negative (dir not file)"; exit 1; }

# file_contains
assert_fs_file_contains "$WORK_DIR/sub/file.txt" "hello" || { echo "FAIL: file_contains positive"; exit 1; }
! assert_fs_file_contains "$WORK_DIR/sub/file.txt" "missing" || { echo "FAIL: file_contains negative"; exit 1; }

# file_matches
assert_fs_file_matches "$WORK_DIR/sub/file.txt" '^hello.*world$' || { echo "FAIL: file_matches positive"; exit 1; }
! assert_fs_file_matches "$WORK_DIR/sub/file.txt" '^nope' || { echo "FAIL: file_matches negative"; exit 1; }

echo "PASS"
