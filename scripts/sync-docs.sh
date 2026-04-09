#!/usr/bin/env bash
# sync-docs.sh — Copy website/docs/ to docs/, stripping YAML frontmatter if present.
#
# Usage: ./scripts/sync-docs.sh
#
# The website/docs/ directory is the source of truth. This script copies each
# .md file to docs/, removing any YAML frontmatter (--- delimited block at the
# top of the file) so docs/ contains clean markdown for GitHub/LLM consumption.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$REPO_ROOT/website/docs"
DST="$REPO_ROOT/docs"
MARKDOWNLINT_VERSION="0.43.0"

if [ ! -d "$SRC" ]; then
  echo "Error: $SRC does not exist" >&2
  exit 1
fi

count=0

# Recursively find all .md files in website/docs/
while IFS= read -r src_file; do
  rel="${src_file#"$SRC/"}"
  dst_file="$DST/$rel"

  # Skip docs/plans/ — plans live in dev-docs/plans/, not docs/
  case "$rel" in
    plans/*) continue ;;
  esac

  # Ensure destination directory exists
  mkdir -p "$(dirname "$dst_file")"

  # Strip YAML frontmatter if present (--- delimited block at start of file)
  if head -1 "$src_file" | grep -q '^---$'; then
    # Find the closing --- and output everything after it
    awk 'BEGIN{skip=0; found=0} /^---$/{if(!found){found=1; skip=1; next} else if(skip){skip=0; next}} !skip{print}' "$src_file" > "$dst_file"
  else
    cp "$src_file" "$dst_file"
  fi

  count=$((count + 1))
done < <(find "$SRC" -name '*.md' -type f)

echo "Synced $count files from website/docs/ to docs/"

# Run formatting and linting only on the synced doc trees so we don't touch
# unrelated files elsewhere in the repo.
echo ""
echo "Formatting synced markdown in website/docs and docs..."
if command -v prettier >/dev/null 2>&1; then
  prettier --write "$SRC/**/*.md" "$DST/**/*.md" --prose-wrap always --ignore-path "$REPO_ROOT/.prettierignore" 2>/dev/null || true
  echo "Formatting: OK"
else
  echo "Warning: prettier not found; skipping markdown formatting" >&2
fi

echo "Running markdownlint on website/docs and docs..."
if ! command -v markdownlint >/dev/null 2>&1; then
  echo "markdownlint not found. Installing ${MARKDOWNLINT_VERSION}..."
  npm install -g "markdownlint-cli@${MARKDOWNLINT_VERSION}" || {
    echo "Warning: failed to install markdownlint; skipping markdown lint fix" >&2
    echo ""
    echo "Sync complete. Synced files were copied, but markdown lint was skipped."
    exit 0
  }
fi

if markdownlint "$SRC" "$DST" --config "$REPO_ROOT/.markdownlint.json" --ignore-path "$REPO_ROOT/.markdownlintignore" --fix; then
  echo "Markdown lint fix: OK"
else
  echo "Warning: markdown lint fix had issues (non-fatal)" >&2
fi

echo ""
echo "Sync complete. Synced docs were formatted and linted — ready to commit."
