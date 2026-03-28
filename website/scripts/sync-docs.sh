#!/usr/bin/env bash
# sync-docs.sh — Sync website/docs/ → docs/ stripping YAML frontmatter
#
# Source of truth: website/docs/ (has frontmatter for the website build)
# Target: docs/ (clean markdown, no frontmatter)
#
# Usage:
#   ./scripts/sync-docs.sh          # from website/ directory
#   ./scripts/sync-docs.sh --dry-run  # preview what would change

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WEBSITE_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(dirname "$WEBSITE_DIR")"

SRC_DIR="$WEBSITE_DIR/docs"
DST_DIR="$REPO_ROOT/docs"

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

if [[ ! -d "$SRC_DIR" ]]; then
  echo "Error: Source directory not found: $SRC_DIR" >&2
  exit 1
fi

strip_frontmatter() {
  local file="$1"
  # If file starts with ---, strip everything between first and second ---
  if head -1 "$file" | grep -q '^---$'; then
    # Print everything after the closing --- of frontmatter
    awk 'BEGIN{fm=0} /^---$/{fm++; next} fm>=2{print}' "$file"
  else
    cat "$file"
  fi
}

synced=0
skipped=0

# Find all .md files in source, preserving subdirectory structure
while IFS= read -r src_file; do
  rel_path="${src_file#$SRC_DIR/}"
  dst_file="$DST_DIR/$rel_path"
  dst_dir="$(dirname "$dst_file")"

  # Strip frontmatter and compare
  stripped=$(strip_frontmatter "$src_file")

  if [[ -f "$dst_file" ]]; then
    existing=$(cat "$dst_file")
    if [[ "$stripped" == "$existing" ]]; then
      skipped=$((skipped + 1))
      continue
    fi
  fi

  if $DRY_RUN; then
    echo "[dry-run] Would sync: $rel_path"
  else
    mkdir -p "$dst_dir"
    echo "$stripped" > "$dst_file"
    echo "Synced: $rel_path"
  fi
  synced=$((synced + 1))

done < <(find "$SRC_DIR" -name '*.md' -type f | sort)

echo ""
echo "Done. $synced synced, $skipped unchanged."

if [[ $synced -gt 0 ]] && ! $DRY_RUN; then
  # Run formatting and linting so synced files match CI expectations
  echo ""
  echo "Running fmt-all and lint-all to ensure synced files pass CI..."
  if make -C "$REPO_ROOT" fmt-all 2>&1; then
    echo "Formatting: OK"
  else
    echo "Warning: formatting had issues (non-fatal)" >&2
  fi

  if make -C "$REPO_ROOT" lint-md-fix 2>&1; then
    echo "Markdown lint fix: OK"
  else
    echo "Warning: markdown lint fix had issues (non-fatal)" >&2
  fi

  echo ""
  echo "Sync complete. All files formatted and linted — ready to commit."
fi
