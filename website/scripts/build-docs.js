#!/usr/bin/env node

/**
 * Thrum Documentation Build Script
 *
 * Processes markdown files from /docs directory into:
 * - HTML files (assets/docs/*.html)
 * - Navigation index (assets/docs/index.json)
 * - Search index (assets/docs/search-index.json)
 *
 * Dependencies:
 * - gray-matter: Parse YAML frontmatter
 * - marked: Markdown to HTML conversion
 * - highlight.js: Syntax highlighting
 * - minisearch: Full-text search indexing
 * - fs-extra: File system operations
 *
 * Implementation: Task thrum-235d.3
 */

const fs = require('fs-extra');
const path = require('path');
const matter = require('gray-matter');
const { marked } = require('marked');
const hljs = require('highlight.js');
const MiniSearch = require('minisearch');

// Configuration
const CONFIG = {
  docsDir: path.join(__dirname, '../docs'),
  outputDir: path.join(__dirname, '../assets/docs'),
  websiteDir: path.join(__dirname, '..'),
  siteUrl: 'https://thrum.team'
};

/**
 * Custom tokenizer for MiniSearch
 * CRITICAL: This MUST be identical to the runtime tokenizer in search.js
 * Preserves compound terms (docker-compose, api_key) as single tokens
 */
const customTokenize = (text) => {
  const tokens = [];
  const words = text.toLowerCase().match(/[\w][\w\-_]*/g) || [];
  for (const word of words) {
    tokens.push(word);
  }
  return tokens;
};

/**
 * Configure marked for syntax highlighting (SPA version)
 */
const renderer = new marked.Renderer();
const originalCodeRenderer = renderer.code.bind(renderer);

renderer.code = function({ text, lang, escaped }) {
  const safeLang = lang ? lang.replace(/[^a-zA-Z0-9\-_]/g, '') : '';
  // Apply syntax highlighting if language is specified
  if (safeLang && hljs.getLanguage(safeLang)) {
    try {
      const highlighted = hljs.highlight(text, { language: safeLang }).value;
      return `<pre><code class="language-${safeLang} hljs">${highlighted}</code></pre>\n`;
    } catch (err) {
      console.error(`Highlight error for ${safeLang}:`, err.message);
    }
  }
  // Fall back to default rendering without highlighting
  const escapedText = escaped ? text : escape(text);
  const langClass = safeLang ? ` class="language-${safeLang}"` : '';
  return `<pre><code${langClass}>${escapedText}</code></pre>\n`;
};

// Helper function for HTML escaping
function escape(html) {
  return html
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

marked.setOptions({
  renderer: renderer,
  breaks: false,
  gfm: true
});

/**
 * Recursively find all markdown files
 */
async function findMarkdownFiles(dir, baseDir = dir) {
  const files = [];
  const entries = await fs.readdir(dir, { withFileTypes: true });

  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...await findMarkdownFiles(fullPath, baseDir));
    } else if (entry.isFile() && entry.name.endsWith('.md')) {
      files.push(fullPath);
    }
  }

  return files;
}

/**
 * Rewrite internal markdown links to HTML links
 */
function rewriteLinks(html) {
  return html.replace(/href="([^"]*\.md)(#[^"]*)?"/g, (match, file, hash) => {
    return `href="${file.replace('.md', '.html')}${hash || ''}"`;
  });
}

/**
 * Process a single markdown file
 */
async function processMarkdownFile(filePath, docsDir, outputDir) {
  const content = await fs.readFile(filePath, 'utf-8');
  const { data: frontmatter, content: markdown } = matter(content);

  // Convert to HTML
  let html = marked(markdown);

  // Rewrite internal links
  html = rewriteLinks(html);

  // Calculate output path
  const relativePath = path.relative(docsDir, filePath);
  const outputPath = path.join(
    outputDir,
    relativePath.replace('.md', '.html')
  );

  // Ensure output directory exists
  await fs.ensureDir(path.dirname(outputPath));

  // Write HTML file
  await fs.writeFile(outputPath, html);

  // Calculate URL path for navigation
  const urlPath = relativePath.replace('.md', '.html');

  return {
    path: urlPath,
    title: frontmatter.title || path.basename(filePath, '.md'),
    description: frontmatter.description || '',
    category: frontmatter.category || 'uncategorized',
    order: frontmatter.order != null ? frontmatter.order : 999,
    tags: frontmatter.tags || [],
    lastUpdated: frontmatter.last_updated || '',
    content: markdown,
    filePath: relativePath
  };
}

// Category ordering, labels, and parent→children relationships.
// MUST match js/docs-nav.js so per-doc SEO pages render the same sidebar
// as the (legacy) SPA. Update both together when categories change.
const SPA_CATEGORY_ORDER = [
  'overview',
  'onboarding',
  'quickstart',
  'orchestration',
  'identity',
  'messaging',
  'substrate',
  'coordination',
  'integrations',
  'infrastructure',
  'reference',
  'api'
];

const SPA_CATEGORY_LABELS = {
  overview: 'Overview',
  onboarding: 'Onboarding',
  quickstart: 'Getting Started',
  tools: 'Recommended Tools',
  orchestration: 'Orchestration',
  identity: 'Identity & Agents',
  messaging: 'Messaging',
  substrate: 'Personal Agent Substrate',
  coordination: 'Coordination',
  integrations: 'Integrations',
  infrastructure: 'Infrastructure',
  reference: 'Reference',
  'agent-protocols': 'Agent Protocols',
  api: 'API Reference',
  guides: 'Guides'
};

const SPA_CATEGORY_CHILDREN = {
  quickstart: ['tools'],
  reference: ['agent-protocols']
};

/**
 * Generate per-doc static pages at /docs/<path>.html — each is the canonical
 * URL for its content. Pages are pre-rendered with the full SPA chrome
 * (header, sidebar, content, footer), use self-canonical URLs (no SPA
 * fragment), and serve their content statically with no JS redirect. The
 * docs.html SPA shell exists only as a redirect to /docs/overview.html now.
 *
 * See `spaTemplate` and `buildSpaSidebar` below for the rendering logic.
 */
async function generateSEOPages(docs, docsDir) {
  let count = 0;
  for (const doc of docs) {
    // Render content with hljs syntax-highlighting markup (same as the
    // assets/docs/<path>.html artifacts the legacy SPA fetched) so per-doc
    // pages display code identically.
    const contentHtml = rewriteLinks(marked.parse(doc.content));
    // doc.path already includes the .html extension (e.g. "beta-channel.html",
    // "api/events.html") so we don't append another one.
    const canonicalUrl = `${CONFIG.siteUrl}/docs/${doc.path}`;
    const sidebarHtml = buildSpaSidebar(docs, doc);
    const seoPath = path.join(docsDir, doc.path);
    await fs.ensureDir(path.dirname(seoPath));
    await fs.writeFile(seoPath, spaTemplate(doc, contentHtml, sidebarHtml, canonicalUrl));
    count++;
  }
  return count;
}

/**
 * Build the pre-rendered sidebar nav HTML for a per-doc page.
 *
 * Mirrors the structure that js/docs-nav.js produces at runtime so the
 * pre-rendered page renders an identical sidebar to the (legacy) SPA. The
 * current doc's link gets `.active`. Sub-categories use the same
 * `.sidebar-subcategory` + `.collapsible` markup; the chevron is rendered
 * (▸) — without docs-nav.js loaded, sub-categories stay expanded.
 *
 * Links use absolute root-relative URLs (`/docs/<path>`) so the same href
 * works from any per-doc page regardless of depth.
 */
function buildSpaSidebar(docs, currentDoc) {
  // Group by category (skip uncategorized)
  const groups = {};
  for (const doc of docs) {
    if (doc.category === 'uncategorized') continue;
    if (!groups[doc.category]) groups[doc.category] = [];
    groups[doc.category].push(doc);
  }
  // Sort within each group by order, then title
  for (const cat of Object.keys(groups)) {
    groups[cat].sort((a, b) => {
      if (a.order !== b.order) return a.order - b.order;
      return a.title.localeCompare(b.title);
    });
  }
  // child→parent map for skip logic
  const childToParent = {};
  for (const [parent, children] of Object.entries(SPA_CATEGORY_CHILDREN)) {
    for (const child of children) {
      childToParent[child] = parent;
    }
  }

  function renderCategoryBlock(cat, isSub) {
    if (!groups[cat]) return '';
    const label = SPA_CATEGORY_LABELS[cat] || cat;
    const catClass = isSub
      ? 'sidebar-category sidebar-subcategory'
      : 'sidebar-category';
    const labelClass = isSub
      ? 'sidebar-category-label sidebar-subcategory-label collapsible'
      : 'sidebar-category-label';

    const labelHtml = isSub
      ? `<span class="${labelClass}"><span class="sidebar-chevron">▸</span><span>${escapeAttr(label)}</span></span>`
      : `<span class="${labelClass}">${escapeAttr(label)}</span>`;

    const baseLinkClass = isSub ? 'sidebar-link sidebar-sublink' : 'sidebar-link';
    const linksHtml = groups[cat].map(doc => {
      const isActive = doc.path === currentDoc.path;
      const cls = isActive ? `${baseLinkClass} active` : baseLinkClass;
      // doc.path already includes the .html extension. Use absolute root-
      // relative URL so the same href works from any page depth.
      const href = `/docs/${doc.path}`;
      const titleAttr = doc.description ? ` title="${escapeAttr(doc.description)}"` : '';
      return `          <a class="${cls}" href="${href}" data-path="${doc.path}" data-category="${cat}"${titleAttr}>${escapeAttr(doc.title)}</a>`;
    }).join('\n');

    // Render child sub-categories inline after the links (matches docs-nav.js)
    let childrenHtml = '';
    for (const childCat of (SPA_CATEGORY_CHILDREN[cat] || [])) {
      childrenHtml += renderCategoryBlock(childCat, true);
    }

    return `        <div class="${catClass}" data-category="${cat}">
          ${labelHtml}
          <div class="sidebar-category-content">
${linksHtml}${childrenHtml ? '\n' + childrenHtml : ''}
          </div>
        </div>
`;
  }

  let html = '';
  // Top-level categories in declared order; skip those rendered as children
  for (const cat of SPA_CATEGORY_ORDER) {
    if (childToParent[cat]) continue;
    html += renderCategoryBlock(cat, false);
  }
  // Catch-all: any category seen in docs but not declared
  for (const cat of Object.keys(groups)) {
    if (SPA_CATEGORY_ORDER.includes(cat)) continue;
    if (childToParent[cat]) continue;
    html += renderCategoryBlock(cat, false);
  }
  return html;
}

/**
 * Per-doc static page template — full SPA chrome (header, sidebar, content,
 * footer), self-canonical, indexable. Every doc gets one of these at
 * /docs/<path>.html.
 */
function spaTemplate(doc, contentHtml, sidebarHtml, canonicalUrl) {
  const jsonLd = JSON.stringify({
    '@context': 'https://schema.org',
    '@type': 'TechArticle',
    headline: doc.title,
    description: doc.description,
    url: canonicalUrl,
    author: { '@type': 'Person', name: 'Leon Letto' },
    publisher: {
      '@type': 'Organization',
      name: 'Thrum',
      url: `${CONFIG.siteUrl}/`,
    },
    isPartOf: {
      '@type': 'TechArticle',
      name: 'Thrum Documentation',
      url: `${CONFIG.siteUrl}/docs/overview.html`,
    },
  });
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta name="color-scheme" content="dark light">
  <title>${escapeAttr(doc.title)} — Thrum</title>
  <meta name="description" content="${escapeAttr(doc.description)}">
  <!-- Open Graph -->
  <meta property="og:type" content="article">
  <meta property="og:title" content="${escapeAttr(doc.title)} — Thrum">
  <meta property="og:description" content="${escapeAttr(doc.description)}">
  <meta property="og:url" content="${canonicalUrl}">
  <meta property="og:site_name" content="Thrum">
  <meta property="og:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <meta property="og:image:width" content="1200">
  <meta property="og:image:height" content="630">
  <!-- Twitter Card -->
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="${escapeAttr(doc.title)} — Thrum">
  <meta name="twitter:description" content="${escapeAttr(doc.description)}">
  <meta name="twitter:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <link rel="canonical" href="${canonicalUrl}">
  <script type="application/ld+json">${jsonLd}</script>
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>⚡</text></svg>">
  <link rel="preload" as="font" type="font/woff2" href="/assets/fonts/inter.woff2" crossorigin>
  <link rel="preload" as="font" type="font/woff2" href="/assets/fonts/ibm-plex-mono-regular.woff2" crossorigin>
  <link rel="stylesheet" href="/css/theme.css">
  <link rel="stylesheet" href="/css/docs.css">
  <script>
    (function(){var t=localStorage.getItem('thrum-theme');if(t){document.documentElement.setAttribute('data-theme',t)}else if(window.matchMedia&&window.matchMedia('(prefers-color-scheme:light)').matches){document.documentElement.setAttribute('data-theme','light')}else{document.documentElement.setAttribute('data-theme','dark')}})();
  </script>
  <!-- Privacy-friendly analytics by Plausible -->
  <script async src="https://plausible.io/js/pa-0EO0yjcXim9oq9r4-I2w1.js"></script>
  <script>
    window.plausible=window.plausible||function(){(plausible.q=plausible.q||[]).push(arguments)},plausible.init=plausible.init||function(i){plausible.o=i||{}};
    plausible.init()
  </script>
</head>
<body>

  <!-- Header -->
  <header class="site-header">
    <div class="header-inner">
      <nav class="header-nav">
        <a href="/index.html" class="logo">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
        </a>
        <div class="nav-links">
          <a href="/index.html" class="nav-link">Home</a>
          <a href="/docs/overview.html" class="nav-link nav-link-active">Docs</a>
          <a href="/blog.html" class="nav-link">Blog</a>
          <a href="/about.html" class="nav-link">About</a>
          <a href="https://github.com/leonletto/thrum" class="nav-link nav-link-external" target="_blank" rel="noopener">GitHub</a>
          <button class="theme-toggle" id="theme-toggle" aria-label="Toggle theme">
            <span class="icon-sun">&#x2600;</span>
            <span class="icon-moon">&#x263E;</span>
          </button>
        </div>
        <button class="sidebar-toggle" id="sidebar-toggle" aria-label="Toggle navigation">
          <span></span><span></span><span></span>
        </button>
      </nav>
    </div>
  </header>

  <!-- Layout -->
  <div class="docs-layout">

    <!-- Sidebar -->
    <aside class="docs-sidebar" id="docs-sidebar">
      <div class="sidebar-search">
        <input type="text" id="search-input" placeholder="Search docs... (Ctrl+K)" autocomplete="off">
        <div class="search-results" id="search-results"></div>
      </div>
      <nav class="sidebar-nav" id="sidebar-nav">
${sidebarHtml}      </nav>
    </aside>

    <!-- Content -->
    <main class="docs-content" id="docs-content">
      <div class="docs-content-inner" id="docs-content-inner">
${contentHtml}
      </div>
    </main>

  </div>

  <script src="/js/vendor/minisearch.js"></script>
  <script src="/js/search.js" data-docs-base="/docs/" data-search-index="/assets/docs/search-index.json"></script>
  <script>
    document.getElementById('theme-toggle').addEventListener('click', function() {
      var current = document.documentElement.getAttribute('data-theme');
      var next = current === 'light' ? 'dark' : 'light';
      document.documentElement.setAttribute('data-theme', next);
      localStorage.setItem('thrum-theme', next);
    });
    document.getElementById('sidebar-toggle').addEventListener('click', function() {
      document.getElementById('docs-sidebar').classList.toggle('open');
    });
  </script>
</body>
</html>`;
}

function escapeAttr(str) {
  return (str || '')
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

/**
 * Main build function
 */
async function buildDocs() {
  console.log('🚀 Thrum Documentation Build Script');
  console.log('📂 Source:', CONFIG.docsDir);
  console.log('📦 Output:', CONFIG.outputDir);
  console.log('');

  try {
    // Ensure output directory exists
    await fs.ensureDir(CONFIG.outputDir);

    // Step 1: Find all markdown files
    console.log('📄 Finding markdown files...');
    const markdownFiles = await findMarkdownFiles(CONFIG.docsDir);
    console.log(`   Found ${markdownFiles.length} files`);

    // Step 2-3: Process each file (parse frontmatter + convert to HTML)
    console.log('🔄 Processing markdown files...');
    const docs = [];
    for (const file of markdownFiles) {
      const doc = await processMarkdownFile(file, CONFIG.docsDir, CONFIG.outputDir);
      docs.push(doc);
      console.log(`   ✓ ${doc.path}`);
    }

    // Step 4: Generate navigation index
    console.log('📋 Generating navigation index...');
    const navIndex = docs
      .sort((a, b) => {
        // Sort by category, then order, then title
        if (a.category !== b.category) {
          return a.category.localeCompare(b.category);
        }
        if (a.order !== b.order) {
          return a.order - b.order;
        }
        return a.title.localeCompare(b.title);
      })
      .map(doc => ({
        path: doc.path,
        title: doc.title,
        description: doc.description,
        category: doc.category,
        order: doc.order,
        tags: doc.tags,
        lastUpdated: doc.lastUpdated
      }));

    await fs.writeJSON(
      path.join(CONFIG.outputDir, 'index.json'),
      navIndex,
      { spaces: 2 }
    );
    console.log(`   ✓ Generated index.json (${navIndex.length} entries)`);

    // Step 5: Generate search index
    console.log('🔍 Generating search index...');
    const miniSearch = new MiniSearch({
      fields: ['title', 'description', 'content', 'tags'],
      storeFields: ['title', 'description', 'path', 'category'],
      tokenize: customTokenize,
      searchOptions: {
        boost: { title: 2, description: 1.5 },
        fuzzy: 0.2,
        prefix: true
      }
    });

    // Add documents to search index
    const searchDocs = docs.map((doc, idx) => ({
      id: idx.toString(),
      title: doc.title,
      description: doc.description,
      content: doc.content,
      tags: doc.tags.join(' '),
      path: doc.path,
      category: doc.category
    }));

    miniSearch.addAll(searchDocs);

    // Export search index
    const searchIndex = JSON.stringify(miniSearch);
    await fs.writeFile(
      path.join(CONFIG.outputDir, 'search-index.json'),
      searchIndex
    );
    console.log(`   ✓ Generated search-index.json (${searchDocs.length} documents)`);

    // Step 6: Generate per-doc SEO pages with Open Graph meta tags
    console.log('🔗 Generating SEO pages with Open Graph meta tags...');
    const seoCount = await generateSEOPages(docs, CONFIG.docsDir);
    console.log(`   ✓ Generated ${seoCount} SEO pages in docs/`);

    console.log('');
    console.log('✅ Build complete!');
    console.log(`   Processed: ${docs.length} documents`);
    console.log(`   Output: ${CONFIG.outputDir}`);

  } catch (error) {
    console.error('❌ Build failed:', error);
    throw error;
  }
}

// Run if called directly
if (require.main === module) {
  buildDocs().catch(err => {
    console.error('❌ Build failed:', err);
    process.exit(1);
  });
}

module.exports = { buildDocs, customTokenize };
