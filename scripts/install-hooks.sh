#!/usr/bin/env bash
set -euo pipefail

echo "=== Git Hook Setup ==="

REPO_ROOT="$(git rev-parse --show-toplevel)"

# Point git to repo-tracked hooks (survives re-clones)
git config core.hooksPath scripts/hooks
echo "Set core.hooksPath -> scripts/hooks"

# Ensure hooks are executable
chmod +x "$REPO_ROOT/scripts/hooks/"* 2>/dev/null || true

# Verify beads config has chaining enabled
if grep -q "chain_strategy" "$REPO_ROOT/.beads/config.yaml" 2>/dev/null; then
    echo "Hook chaining configured in .beads/config.yaml"
else
    echo "WARNING: Hook chaining not configured in .beads/config.yaml"
    echo "Add the following to .beads/config.yaml:"
    echo "  hooks:"
    echo "    chain_strategy: after"
    echo "    chain_timeout_ms: 10000"
fi

echo ""
echo "Hook setup complete."
echo ""
echo "What runs on commit (scripts/hooks/pre-commit):"
echo "  1. beads pre-commit (export JSONL)"
echo "  2. dev-docs/ guard (block accidental commits)"
echo ""
echo "Optional: Run scripts/setup-git-secrets.sh to add secret scanning"
