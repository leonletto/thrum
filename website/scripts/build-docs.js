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
  siteUrl: 'https://leonletto.github.io/thrum'
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

/**
 * Plain renderer for SEO/agent pages — no syntax highlighting spans
 */
const plainRenderer = new marked.Renderer();
plainRenderer.code = function({ text, lang, escaped }) {
  const escapedText = escaped ? text : escape(text);
  return `<pre><code>${escapedText}</code></pre>\n`;
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
  breaks: true,
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

// Category ordering and labels for SEO page sitemap nav
// (mirrors docs-nav.js CATEGORY_ORDER / CATEGORY_LABELS)
const SEO_CATEGORY_ORDER = [
  'overview', 'quickstart', 'webui', 'cli', 'messaging', 'identity',
  'guides', 'api', 'daemon', 'mcp', 'sync', 'development'
];

const SEO_CATEGORY_LABELS = {
  overview: 'Overview',
  quickstart: 'Getting Started',
  tools: 'Recommended Tools',
  webui: 'Web UI',
  cli: 'CLI',
  messaging: 'Messaging',
  identity: 'Identity',
  guides: 'Guides',
  api: 'API Reference',
  reference: 'Reference',
  daemon: 'Daemon',
  mcp: 'MCP Server',
  sync: 'Sync',
  context: 'Context',
  architecture: 'Architecture',
  development: 'Development'
};

const SEO_CATEGORY_CHILDREN = {
  quickstart: ['tools']
};

/**
 * Build a static sitemap nav HTML string from the docs index.
 * Groups pages by category, marks the current page as bold text (no link).
 */
function buildSitemapNav(docs, currentDoc) {
  // Group docs by category
  const groups = {};
  for (const doc of docs) {
    const cat = doc.category || 'uncategorized';
    if (!groups[cat]) groups[cat] = [];
    groups[cat].push(doc);
  }

  // Sort within each group by order then title
  for (const cat of Object.keys(groups)) {
    groups[cat].sort((a, b) => {
      if (a.order !== b.order) return a.order - b.order;
      return a.title.localeCompare(b.title);
    });
  }

  // Collect all categories that appear, ordered by SEO_CATEGORY_ORDER
  const childCats = new Set();
  for (const children of Object.values(SEO_CATEGORY_CHILDREN)) {
    for (const c of children) childCats.add(c);
  }

  const orderedCats = [];
  for (const cat of SEO_CATEGORY_ORDER) {
    if (groups[cat]) {
      orderedCats.push(cat);
      // Add children inline after parent
      const children = SEO_CATEGORY_CHILDREN[cat] || [];
      for (const child of children) {
        if (groups[child]) orderedCats.push(child);
      }
    }
  }
  // Add any remaining categories not in the order list
  for (const cat of Object.keys(groups)) {
    if (!orderedCats.includes(cat)) orderedCats.push(cat);
  }

  // Compute relative prefix for links (pages in subdirs need different paths)
  const currentDepth = (currentDoc.path.match(/\//g) || []).length;
  const linkPrefix = currentDepth > 0 ? '../'.repeat(currentDepth) : '';

  let html = '';
  for (const cat of orderedCats) {
    const label = SEO_CATEGORY_LABELS[cat] || cat;
    const indent = childCats.has(cat) ? '  ' : '';
    html += `${indent}<strong>${label}</strong>: `;
    const links = groups[cat].map(doc => {
      if (doc.path === currentDoc.path) {
        return `<strong>${doc.title}</strong>`;
      }
      return `<a href="${linkPrefix}${doc.path}">${doc.title}</a>`;
    });
    html += links.join(' | ') + '<br>\n';
  }

  return html;
}

/**
 * Render markdown to plain HTML without syntax highlighting.
 * Used for SEO/agent pages to minimize token overhead.
 */
function renderPlainHTML(markdown) {
  let html = marked.parse(markdown, { renderer: plainRenderer, breaks: false, gfm: true });
  return rewriteLinks(html);
}

/**
 * Generate per-doc SEO pages optimized for AI agents and crawlers.
 *
 * Each doc gets a lightweight HTML page at docs/{name}.html that:
 * - Has minimal <head> (title, description, canonical — no OG/Twitter/fonts/CSS)
 * - Includes a sitemap nav showing all doc categories and pages
 * - Renders content without syntax highlighting spans
 * - Redirects browsers to the SPA (docs.html#{path})
 */
async function generateSEOPages(docs, docsDir) {
  const seoTemplate = (doc, html, sitemapNav) => `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>${doc.title} — Thrum</title>
<meta name="description" content="${escapeAttr(doc.description)}">
<link rel="canonical" href="${CONFIG.siteUrl}/docs.html#${doc.path}">
<style>
body{font-family:system-ui,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1.5rem;line-height:1.6;color:#222}
pre{background:#f5f5f5;padding:1rem;overflow-x:auto;border-radius:4px}
code{font-family:ui-monospace,monospace;font-size:0.9em}
table{border-collapse:collapse;width:100%}
th,td{border:1px solid #ddd;padding:0.4rem 0.6rem;text-align:left}
th{background:#f5f5f5}
nav{margin-bottom:1.5rem;padding-bottom:1rem;border-bottom:1px solid #ddd;line-height:1.8}
h2{margin-top:2rem}
a{color:#0366d6}
</style>
</head>
<body>
<nav>
<strong><a href="${doc.path.includes('/') ? '../' : ''}../docs.html">Thrum Docs</a></strong> &rsaquo; ${SEO_CATEGORY_LABELS[doc.category] || doc.category} &rsaquo; ${doc.title}
<hr>
${sitemapNav}</nav>
<main>
${html}
</main>
<script>if(location.search.indexOf('nospa')===-1){location.replace('${doc.path.includes('/') ? '../' : ''}../docs.html#${doc.path}')}</script>
</body>
</html>`;

  let count = 0;
  for (const doc of docs) {
    // Render plain HTML from original markdown (no hljs)
    const plainHtml = renderPlainHTML(doc.content);
    const sitemapNav = buildSitemapNav(docs, doc);
    const seoPath = path.join(docsDir, doc.path);
    await fs.ensureDir(path.dirname(seoPath));
    await fs.writeFile(seoPath, seoTemplate(doc, plainHtml, sitemapNav));
    count++;
  }
  return count;
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
