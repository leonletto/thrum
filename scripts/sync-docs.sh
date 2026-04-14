#!/usr/bin/env bash
# sync-docs.sh — Copy website/docs/ to docs/ (stripping YAML frontmatter) and
# sync llms.txt + llms-full.txt from website/ to repo root, plus llms.txt to
# internal/context/reference/ (the embedded binary copy).
#
# Usage: ./scripts/sync-docs.sh
#
# The website/ directory is the source of truth. This script copies each .md
# file from website/docs/ to docs/, removing any YAML frontmatter (--- delimited
# block at the top of the file) so docs/ contains clean markdown for GitHub/LLM
# consumption. It also copies llms.txt and llms-full.txt to the repo root and
# llms.txt to internal/context/reference/ (the binary embed source), erroring
# out if any destination copy is newer than the website source (to prevent
# silent overwrites of direct edits).

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

# Sync llms.txt and llms-full.txt from website/ to repo root.
# These are hand-crafted, not generated — website/ is the source of truth.
# Safety check: error out if the repo-root copy is newer (someone may have
# edited the root copy directly and a blind overwrite would lose that work).
echo ""
echo "Syncing llms.txt and llms-full.txt to repo root..."
for llm_file in llms.txt llms-full.txt; do
  src_llm="$REPO_ROOT/website/$llm_file"
  dst_llm="$REPO_ROOT/$llm_file"

  if [ ! -f "$src_llm" ]; then
    echo "Warning: $src_llm not found; skipping" >&2
    continue
  fi

  if [ -f "$dst_llm" ] && [ "$dst_llm" -nt "$src_llm" ]; then
    echo "Error: $dst_llm is newer than $src_llm" >&2
    echo "  Someone may have edited the repo-root copy directly." >&2
    echo "  If website/ is correct, touch it first: touch $src_llm" >&2
    exit 1
  fi

  if [ -f "$dst_llm" ] && diff -q "$src_llm" "$dst_llm" >/dev/null 2>&1; then
    echo "  $llm_file: already in sync"
  else
    cp "$src_llm" "$dst_llm"
    echo "  $llm_file: synced"
  fi
done

# Sync website/llms.txt to internal/context/reference/llms.txt (the embedded
# binary copy). Only llms.txt is embedded — llms-full.txt is not. Same safety
# check: error if the embed copy is newer than the website source.
echo ""
echo "Syncing llms.txt to internal/context/reference/ (embedded binary copy)..."
embed_src="$REPO_ROOT/website/llms.txt"
embed_dst="$REPO_ROOT/internal/context/reference/llms.txt"
if [ ! -f "$embed_src" ]; then
  echo "Warning: $embed_src not found; skipping embed sync" >&2
elif [ -f "$embed_dst" ] && [ "$embed_dst" -nt "$embed_src" ]; then
  echo "Error: $embed_dst is newer than $embed_src" >&2
  echo "  Someone may have edited the embed copy directly." >&2
  echo "  If website/ is correct, touch it first: touch $embed_src" >&2
  exit 1
elif [ -f "$embed_dst" ] && diff -q "$embed_src" "$embed_dst" >/dev/null 2>&1; then
  echo "  llms.txt (embed): already in sync"
else
  cp "$embed_src" "$embed_dst"
  echo "  llms.txt (embed): synced"
fi

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
