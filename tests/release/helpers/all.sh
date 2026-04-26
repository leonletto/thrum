#!/usr/bin/env bash
# tests/release/helpers/all.sh — single source point for scenarios + run.sh.
# Sources every helper in the order they depend on each other.

HELPERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck disable=SC1091
source "$HELPERS_DIR/paths.sh"
# shellcheck disable=SC1091
source "$HELPERS_DIR/output.sh"
# shellcheck disable=SC1091
source "$HELPERS_DIR/drive.sh"
# shellcheck disable=SC1091
source "$HELPERS_DIR/assert.sh"
# shellcheck disable=SC1091
source "$HELPERS_DIR/setup-repo.sh"
# shellcheck disable=SC1091
source "$HELPERS_DIR/teardown.sh"
