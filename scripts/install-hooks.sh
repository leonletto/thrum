#!/usr/bin/env bash
set -euo pipefail

echo "=== Git Hook Setup ==="

REPO_ROOT="$(git rev-parse --show-toplevel)"

# Verify beads hooks are installed
if [ ! -f "$REPO_ROOT/.git/hooks/pre-commit" ]; then
    echo "Installing beads hooks..."
    bd hooks install
fi

# Verify chain scripts exist and are executable
for hook in pre-commit pre-push; do
    CHAIN="$REPO_ROOT/.beads/hooks/${hook}.chain"
    if [ -f "$CHAIN" ]; then
        chmod +x "$CHAIN"
        echo "Chain script ready: ${hook}.chain"
    else
        echo "WARNING: Missing chain script: $CHAIN"
    fi
done

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
echo "Hook chaining setup complete."
echo ""
echo "What runs on commit:"
echo "  1. beads pre-commit (export JSONL)"
echo "  2. pre-commit.chain (format check + go vet + git-secrets)"
echo ""
echo "What runs on push:"
echo "  1. beads pre-push (staleness check)"
echo "  2. pre-push.chain (full CI: lint + test + security + build)"
echo ""
echo "Optional: Run scripts/setup-git-secrets.sh to add secret scanning"
