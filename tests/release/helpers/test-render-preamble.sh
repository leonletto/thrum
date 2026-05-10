#!/usr/bin/env bash
# tests/release/helpers/test-render-preamble.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/render-preamble.sh"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# Source preamble with all five tokens
SRC="$WORK_DIR/src.md"
cat > "$SRC" <<'EOF'
You are {{.AgentName}} working in module {{.Module}}.
Worktree: {{.WorktreePath}}
Coordinator: {{.CoordinatorName}}
Repo root: {{.RepoRoot}}
EOF

# Fixture
export FIXTURE_REPO="$WORK_DIR/fixture"
mkdir -p "$FIXTURE_REPO/.thrum/role_templates"

# Render — coordinator role, agent_name=test_coordinator
render_preamble \
  --role coordinator \
  --src "$SRC" \
  --agent-name test_coordinator \
  --module main \
  --worktree "$FIXTURE_REPO" \
  --coordinator-name test_coordinator \
  --repo-root "$FIXTURE_REPO"

DST="$FIXTURE_REPO/.thrum/role_templates/coordinator.md"
[[ -f "$DST" ]] || { echo "FAIL: render did not produce $DST"; exit 1; }
grep -q "You are test_coordinator working in module main." "$DST" || { echo "FAIL: AgentName/Module not substituted"; cat "$DST"; exit 1; }
grep -q "Worktree: $FIXTURE_REPO" "$DST" || { echo "FAIL: WorktreePath not substituted"; exit 1; }
grep -q "Coordinator: test_coordinator" "$DST" || { echo "FAIL: CoordinatorName not substituted"; exit 1; }
grep -q "Repo root: $FIXTURE_REPO" "$DST" || { echo "FAIL: RepoRoot not substituted"; exit 1; }
! grep -q '{{' "$DST" || { echo "FAIL: untouched template tokens remain"; exit 1; }

echo "PASS"
