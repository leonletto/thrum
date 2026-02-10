#!/usr/bin/env bash
set -euo pipefail

echo "=== git-secrets setup ==="

# Check if git-secrets is installed
if ! command -v git-secrets >/dev/null 2>&1; then
    echo "git-secrets is not installed."
    if command -v brew >/dev/null 2>&1; then
        echo "Install with: brew install git-secrets"
    else
        echo "Install from: https://github.com/awslabs/git-secrets"
    fi
    exit 1
fi

# Install git-secrets hooks into this repo
# This adds the git-secrets hooks alongside existing hooks
git secrets --install --force

# Register AWS patterns (common secret patterns)
git secrets --register-aws

# Add custom patterns for common secret leaks
git secrets --add 'PRIVATE KEY'
git secrets --add 'password\s*=\s*["\x27][^"\x27]{8,}'
git secrets --add 'secret\s*=\s*["\x27][^"\x27]{8,}'
git secrets --add 'token\s*=\s*["\x27][^"\x27]{8,}'
git secrets --add 'api[_-]?key\s*=\s*["\x27][^"\x27]{8,}'

echo ""
echo "git-secrets configured successfully."
echo "Patterns registered: AWS keys, private keys, passwords, secrets, tokens, API keys"
echo ""
echo "Note: git-secrets hooks are installed in .git/hooks/"
echo "They will run alongside the existing beads hook shims."
echo ""
echo "To scan existing history: git secrets --scan-history"
echo "To scan staged files:     git secrets --scan"
