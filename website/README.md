# Thrum Documentation Website

**URL:** https://docs.thrum.info

Static documentation website for Thrum - Git-backed messaging for AI agent
coordination.

## Structure

```
website/
├── index.html              # Landing page (marketing)
├── docs.html               # Documentation viewer
├── css/
│   ├── theme.css          # Design system (Thrum UI)
│   ├── landing.css        # Landing page styles
│   └── docs.css           # Documentation styles
├── js/
│   ├── search.js          # MiniSearch integration
│   ├── docs-nav.js        # Documentation navigation
│   ├── minisearch.min.js  # MiniSearch library
│   └── highlight.min.js   # Syntax highlighting
├── assets/
│   ├── docs/              # Generated HTML + indexes
│   ├── fonts/             # Self-hosted fonts
│   └── images/            # Logo, icons
└── scripts/
    └── build-docs.js      # Build script
```

## Development

```bash
# Install dependencies
npm install

# Build documentation
npm run build-docs

# Serve locally at http://localhost:8080
npm run serve
```

## Design System

**Theme:** Data Terminal / Mission Control

- **Colors:** Dark navy (#0a0e1a, #0f172a, #1e293b) with cyan accents (#38bdf8)
- **Fonts:** IBM Plex Mono (technical), Inter (body)
- **Style:** Sharp corners (2-4px), glowing borders, gradient panels

## Technology

- **Pure vanilla** HTML/CSS/JS (no framework)
- **MiniSearch** for client-side search
- **marked** + **highlight.js** for markdown processing
- **Static deployment** (GitHub Pages, Netlify, Vercel)

## Tasks

Epic: `thrum-235d` — Documentation Website

- Design spec: `/docs/plans/2026-02-09-docs-website-design.md`
- Tasks tracked in beads issue tracker

## License

MIT
