# Website

Static documentation site for Thrum. Source of truth for all docs — `docs/` in
the repo root is a sync target, not an edit target.

## Build & Run

```bash
cd website
npm install          # first time only
npm run build-docs   # process markdown → HTML + index.json + search index
npm run serve        # starts http-server on localhost:8080
```

## Verify with playwright-cli

Use `playwright-cli` (not Playwright MCP) to check the site after changes:

```bash
playwright-cli open http://localhost:8080/docs.html
playwright-cli snapshot                        # check sidebar nav, page content
playwright-cli click e5                        # interact with elements by ref
playwright-cli screenshot --filename=check.png # save visual proof to file
playwright-cli close
```

## Docs Workflow

1. Edit markdown files in `website/docs/` (include YAML frontmatter for title,
   category, order, tags)
2. Run `npm run build-docs` to regenerate `assets/docs/`
3. Run `bash scripts/sync-docs.sh` from the repo root to sync stripped copies to
   `docs/`
4. Verify locally with `npm run serve` + `playwright-cli`

## Deployment

The `website-dev` branch is what gets deployed. When changes are ready:

```bash
git checkout website-dev
git merge --ff-only main    # or thrum-dev, whichever has the changes
git push origin website-dev
git checkout thrum-dev      # return to dev branch
```

Edit on `main` or `thrum-dev`, fast-forward `website-dev` when ready to deploy.

## Key Files

| Path                    | Purpose                                                                 |
| ----------------------- | ----------------------------------------------------------------------- |
| `docs/*.md`             | Source markdown with YAML frontmatter                                   |
| `scripts/build-docs.js` | Build pipeline (markdown → HTML, index.json, search index, SEO pages)   |
| `js/docs-nav.js`        | Sidebar navigation (category order, labels, collapsible sub-categories) |
| `css/docs.css`          | Sidebar and content styles                                              |
| `assets/docs/`          | Build output (gitignored except index.json and search-index.json)       |
