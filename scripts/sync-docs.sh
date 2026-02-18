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

if [ ! -d "$SRC" ]; then
  echo "Error: $SRC does not exist" >&2
  exit 1
fi

count=0

# Recursively find all .md files in website/docs/
while IFS= read -r src_file; do
  rel="${src_file#"$SRC/"}"
  dst_file="$DST/$rel"

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
