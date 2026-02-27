#!/usr/bin/env bash
set -euo pipefail

THRUM="/Users/leon/dev/opensource/thrum"
SRC="$THRUM/dev-docs/"
DEST="/Users/leon/dev/opensource/thrumdev/dev-docs/"

# Refresh beads backup before syncing
echo "Refreshing beads backup..."
mkdir -p "$THRUM/dev-docs/backup/beads"
(cd "$THRUM" && bd backup --force 2>/dev/null || true)
cp "$THRUM/.beads/backup/"*.jsonl "$THRUM/.beads/backup/backup_state.json" \
   "$THRUM/dev-docs/backup/beads/" 2>/dev/null || true
echo "Beads backup updated."

# Sync files (delete removed files from dest)
rsync -av --delete "$SRC" "$DEST"

# Commit any changes in the target repo
cd /Users/leon/dev/opensource/thrumdev
if git diff --quiet -- dev-docs/ && git diff --cached --quiet -- dev-docs/ && [ -z "$(git ls-files --others --exclude-standard dev-docs/)" ]; then
  echo "No changes to commit."
  exit 0
fi

git add dev-docs/
git commit -m "backup: sync dev-docs from thrum $(date +%Y-%m-%d)"
echo "Committed dev-docs backup."
