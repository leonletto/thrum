## Security & CI/CD

Thrum uses GitHub Actions for continuous integration, security scanning, and
automated deployment. This guide covers the available workflows and how to
configure them.

## GitHub Actions Workflows

### Documentation Deployment

**File:** `.github/workflows/deploy-pages.yml`

Automatically builds and deploys the documentation website to GitHub Pages.

**Triggers:**

- Push to `website-dev` branch (changes in `website/` directory)
- Manual dispatch via GitHub Actions UI

**Steps:**

1. Checkout repository
2. Install Node.js dependencies (`website/` directory)
3. Build docs (`npm run build-docs`) — generates HTML, search index, navigation
   index
4. Deploy `website/` directory to GitHub Pages

```yaml
# Manual trigger
gh workflow run deploy-pages.yml
```

### Security Scanning

Additional security scanning workflows are planned for a future release.

## Branch Protection

The repository uses branch-based workflows:

| Branch        | Purpose                       | Push / Deployment                          |
| ------------- | ----------------------------- | ------------------------------------------ |
| `main`        | Stable release branch         | Updated via release merges only            |
| `thrum-dev`   | Active development branch     | Pushed to origin; no deploy                |
| `website-dev` | Documentation website         | Push triggers GitHub Pages auto-deploy     |
| `feature/*`   | Feature/fix work in worktrees | Local-only; reaches origin via `thrum-dev` |

## Local Development

### Building Docs Locally

```bash
cd website
npm install
npm run build-docs
```

### Running the Dev Server

```bash
cd website
npm run serve
# Visit http://localhost:8080
```

### Syncing Docs

To sync `website/docs/` (with frontmatter) to `docs/` (clean markdown):

```bash
cd website
./scripts/sync-docs.sh          # sync all changed files
./scripts/sync-docs.sh --dry-run  # preview changes
```

## Tailscale Security

For remote access and cross-machine synchronization, Thrum uses Tailscale as the
primary security model. Tailscale gives you end-to-end WireGuard encryption,
zero-trust networking, and automatic key rotation. See
[Tailscale Security](tailscale-security.md) for detailed security model and
threat analysis.

## Next Steps

- [Development Guide](development.md) — full contributing guide including
  testing, building, and adding new features
- [Tailscale Security](tailscale-security.md) — the security model for remote
  access and cross-machine sync
- [Architecture](architecture.md) — system design overview before contributing
  to the codebase
- [Quickstart Guide](quickstart.md) — get Thrum running locally in 5 minutes
