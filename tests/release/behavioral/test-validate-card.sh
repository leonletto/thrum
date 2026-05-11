#!/usr/bin/env bash
# tests/release/behavioral/test-validate-card.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VALIDATE="${SCRIPT_DIR}/validate-card.sh"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# Valid card
cat > "$WORK_DIR/good.yaml" <<'EOF'
id: 99-test
description: example
agents:
  coord: { role: coordinator }
steps:
  - id: dispatch
    timeout: 5s
  - id: check
    timeout: 30s
    assert:
      - { kind: fs, predicate: dir_exists, path: /tmp }
EOF

bash "$VALIDATE" "$WORK_DIR/good.yaml" || { echo "FAIL: valid card rejected"; exit 1; }

# Missing top-level id
cat > "$WORK_DIR/bad-noid.yaml" <<'EOF'
description: missing id
agents: { coord: { role: coordinator } }
steps: [{ id: x, timeout: 5s }]
EOF
! bash "$VALIDATE" "$WORK_DIR/bad-noid.yaml" 2>/dev/null || { echo "FAIL: bad-noid card accepted"; exit 1; }

# Step missing id
cat > "$WORK_DIR/bad-step.yaml" <<'EOF'
id: 99-test
description: bad step
agents: { coord: { role: coordinator } }
steps:
  - timeout: 5s
EOF
! bash "$VALIDATE" "$WORK_DIR/bad-step.yaml" 2>/dev/null || { echo "FAIL: bad-step card accepted"; exit 1; }

# Assert missing kind
cat > "$WORK_DIR/bad-assert.yaml" <<'EOF'
id: 99-test
description: bad assert
agents: { coord: { role: coordinator } }
steps:
  - id: x
    timeout: 5s
    assert:
      - { predicate: dir_exists }
EOF
! bash "$VALIDATE" "$WORK_DIR/bad-assert.yaml" 2>/dev/null || { echo "FAIL: bad-assert card accepted"; exit 1; }

echo "PASS"
