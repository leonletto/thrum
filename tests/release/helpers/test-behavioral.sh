#!/usr/bin/env bash
# tests/release/helpers/test-behavioral.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/behavioral.sh"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# Smoke fixture
export FIXTURE_REPO="$TMPDIR/repo"
export FIXTURE_WORKSPACES="$TMPDIR/workspaces"
export FIXTURE_THRUM="$FIXTURE_REPO/.thrum"
export RUNTIME=bash
mkdir -p "$FIXTURE_REPO" "$FIXTURE_WORKSPACES"

# Minimal card with one step that polls fs.dir_exists and passes.
CARD="$TMPDIR/smoke.yaml"
cat > "$CARD" <<'EOF'
id: smoke-driver
description: driver smoke test
agents: {}
steps:
  - id: check-tmp
    timeout: 5s
    assert:
      - { kind: fs, predicate: dir_exists, path: "${FIXTURE_REPO}" }
EOF

OUT="$TMPDIR/out.jsonl"
behavioral_run_card "$CARD" "$OUT" || { echo "FAIL: behavioral_run_card returned non-zero"; cat "$OUT"; exit 1; }

# Verify JSONL has a PASS record for check-tmp
grep -q '"step":"check-tmp"' "$OUT" || { echo "FAIL: no check-tmp record"; cat "$OUT"; exit 1; }
grep -q '"outcome":"PASS"' "$OUT" || { echo "FAIL: check-tmp not PASS"; cat "$OUT"; exit 1; }
grep -q '"step":"__summary__"' "$OUT" || { echo "FAIL: no summary record"; exit 1; }

echo "PASS"
