# Security & CI/CD

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

> **Note:** Security CI/CD workflows are being added. This section will be
> updated with specific workflow details, scan types, and configuration options
> once they are merged.

Planned security checks include:

- **Dependency scanning** — automated checks for known vulnerabilities in Go and
  Node.js dependencies
- **Static analysis** — code quality and security linting
- **Secret detection** — prevent accidental credential commits

## Branch Protection

The repository uses branch-based workflows:

| Branch        | Purpose               | Deployment               |
| ------------- | --------------------- | ------------------------ |
| `main`        | Stable release branch | Production merges        |
| `website-dev` | Documentation website | GitHub Pages auto-deploy |
| `feature/*`   | Feature development   | CI checks on PR          |

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
./dev-up.sh
# Visit http://localhost:8080
```

### Syncing Docs

To sync `website/docs/` (with frontmatter) to `docs/` (clean markdown):

```bash
cd website
./scripts/sync-docs.sh          # sync all changed files
./scripts/sync-docs.sh --dry-run  # preview changes
```

## See Also

- [Development Guide](development.md) — contributing and local setup
- [Quickstart Guide](quickstart.md) — getting started with Thrum
