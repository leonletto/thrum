#!/usr/bin/env bash
set -euo pipefail

# Deploy cursor-plugin into a target .cursor/ directory.
# Usage: local-install.sh [--target <path>]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) TARGET="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# Default to git repo root
if [ -z "$TARGET" ]; then
  TARGET="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
fi

CURSOR_DIR="$TARGET/.cursor"

echo "Installing cursor-plugin into $CURSOR_DIR"

# Create directories
mkdir -p "$CURSOR_DIR/rules" "$CURSOR_DIR/skills" "$CURSOR_DIR/commands" "$CURSOR_DIR/agents"

# Copy rules
cp "$SCRIPT_DIR/rules/"*.mdc "$CURSOR_DIR/rules/"

# Copy skills (if synced)
if [ -d "$SCRIPT_DIR/skills" ] && [ "$(ls -A "$SCRIPT_DIR/skills" 2>/dev/null)" ]; then
  cp -R "$SCRIPT_DIR/skills/"* "$CURSOR_DIR/skills/"
fi

# Copy commands (if synced)
if [ -d "$SCRIPT_DIR/commands" ] && [ "$(ls -A "$SCRIPT_DIR/commands" 2>/dev/null)" ]; then
  cp "$SCRIPT_DIR/commands/"*.md "$CURSOR_DIR/commands/"
fi

# Copy agents
if [ -d "$SCRIPT_DIR/agents" ] && [ "$(ls -A "$SCRIPT_DIR/agents" 2>/dev/null)" ]; then
  cp "$SCRIPT_DIR/agents/"*.md "$CURSOR_DIR/agents/"
fi

# Write hooks.json with resolved absolute paths
sed "s|__PLUGIN_ROOT__|${SCRIPT_DIR}|g" \
  "$SCRIPT_DIR/hooks/hooks.json" > "$CURSOR_DIR/hooks.json"

# Write mcp.json for thrum MCP server
cat > "$CURSOR_DIR/mcp.json" <<'MCPEOF'
{
  "mcpServers": {
    "thrum": {
      "type": "command",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
MCPEOF

# Add .cursor/ to .gitignore if not present
GITIGNORE="$TARGET/.gitignore"
if [ -f "$GITIGNORE" ]; then
  if ! grep -qx '.cursor/' "$GITIGNORE"; then
    echo '.cursor/' >> "$GITIGNORE"
    echo "Added .cursor/ to .gitignore"
  fi
else
  echo '.cursor/' > "$GITIGNORE"
  echo "Created .gitignore with .cursor/"
fi

echo "Done. Plugin installed at $CURSOR_DIR"
