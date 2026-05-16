#!/usr/bin/env bash
# Refresh the parent leonletto.github.io sitemap with current Thrum URLs.
#
# Google rejects sub-sitemaps under /thrum/ and the corresponding /thrum/
# URL-prefix property. The workaround is a single merged sitemap at the
# hostname root (https://leonletto.github.io/sitemap.xml) that lists both
# personal-site pages AND Thrum pages. Same hostname, different paths —
# explicitly allowed by the sitemap protocol.
#
# This script:
#   1. Reads the Thrum sitemap we just built locally (must exist first).
#   2. Re-runs the Jekyll site's generate_site_map.sh with our local Thrum
#      sitemap as the source.
#   3. If the Jekyll tree is otherwise clean: auto-commits and pushes.
#      If dirty: leaves the sitemap.xml change in place for Leon to review
#      and commit manually.
#
# Safe to call repeatedly. Never fails the caller — exits 0 on every
# branch including "no Jekyll repo on this machine."

set -uo pipefail

# ── Configuration ────────────────────────────────────────────────────
THRUM_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
THRUM_SITEMAP="${THRUM_REPO_ROOT}/website/sitemap.xml"
JEKYLL_REPO="${JEKYLL_REPO_PATH:-/Users/Shared/Personal/leonletto.github.io}"
JEKYLL_GENERATOR="${JEKYLL_REPO}/generate_site_map.sh"

# ── Preflight ────────────────────────────────────────────────────────
if [[ ! -d "${JEKYLL_REPO}" ]]; then
  # No Jekyll repo on this machine — nothing to do. Silent exit.
  exit 0
fi

if [[ ! -f "${THRUM_SITEMAP}" ]]; then
  echo "sync-parent-sitemap: Thrum sitemap not found at ${THRUM_SITEMAP}; skipping."
  exit 0
fi

if [[ ! -x "${JEKYLL_GENERATOR}" ]]; then
  echo "sync-parent-sitemap: Jekyll generator not executable at ${JEKYLL_GENERATOR}; skipping."
  exit 0
fi

# ── Snapshot Jekyll tree state BEFORE we touch sitemap.xml ───────────
# If there are uncommitted changes other than sitemap.xml itself, we'll
# regenerate but skip the commit/push — Leon's mid-edit, don't interfere.
cd "${JEKYLL_REPO}"
PRE_DIRTY=$(git status --porcelain | grep -v 'sitemap.xml$' || true)

# ── Regenerate ───────────────────────────────────────────────────────
echo "sync-parent-sitemap: regenerating ${JEKYLL_REPO}/sitemap.xml..."
THRUM_SITEMAP_LOCAL_PATH="${THRUM_SITEMAP}" bash "${JEKYLL_GENERATOR}"

# ── Decide what to do with the result ────────────────────────────────
if git diff --quiet sitemap.xml; then
  echo "sync-parent-sitemap: parent sitemap unchanged; nothing to commit."
  exit 0
fi

if [[ -n "${PRE_DIRTY}" ]]; then
  cat <<EOF
sync-parent-sitemap: parent sitemap regenerated, but the Jekyll repo has
other uncommitted changes — leaving sitemap.xml unstaged for manual review.

  Repo: ${JEKYLL_REPO}
  Other dirty files:
$(echo "${PRE_DIRTY}" | sed 's/^/    /')

Commit and push when ready.
EOF
  exit 0
fi

# Clean tree: auto-commit + push.
git add sitemap.xml
git commit -m "chore(sitemap): refresh with current Thrum URLs"

BRANCH=$(git branch --show-current)
if git push origin "${BRANCH}"; then
  echo "sync-parent-sitemap: pushed sitemap refresh to origin/${BRANCH}."
else
  echo "sync-parent-sitemap: WARNING — commit landed locally but push failed." >&2
  exit 0
fi
