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
 * Configure marked for syntax highlighting
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
    order: frontmatter.order || 999,
    tags: frontmatter.tags || [],
    lastUpdated: frontmatter.last_updated || '',
    content: markdown,
    filePath: relativePath
  };
}

/**
 * Generate per-doc SEO pages with Open Graph meta tags.
 *
 * Each doc gets a lightweight HTML page at docs/{name}.html that:
 * - Has proper <title>, og:title, og:description for social sharing
 * - Includes the rendered content for crawler indexing
 * - Redirects browsers to the SPA (docs.html#{path})
 */
async function generateSEOPages(docs, docsDir) {
  const seoTemplate = (doc, html) => `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${doc.title} \u2014 Thrum</title>
  <meta name="description" content="${escapeAttr(doc.description)}">
  <!-- Open Graph -->
  <meta property="og:type" content="article">
  <meta property="og:title" content="${escapeAttr(doc.title)} \u2014 Thrum">
  <meta property="og:description" content="${escapeAttr(doc.description)}">
  <meta property="og:url" content="${CONFIG.siteUrl}/docs/${doc.path}">
  <meta property="og:site_name" content="Thrum">
  <meta property="og:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <meta property="og:image:width" content="1200">
  <meta property="og:image:height" content="630">
  <!-- Twitter Card -->
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="${escapeAttr(doc.title)} \u2014 Thrum">
  <meta name="twitter:description" content="${escapeAttr(doc.description)}">
  <meta name="twitter:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <!-- Canonical: SPA is the primary URL -->
  <link rel="canonical" href="${CONFIG.siteUrl}/docs.html#${doc.path}">
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>&#x26A1;</text></svg>">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600;700&family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
  <link rel="stylesheet" href="../css/theme.css">
  <link rel="stylesheet" href="../css/docs.css">
  <script>
    (function(){var t=localStorage.getItem('thrum-theme');if(t){document.documentElement.setAttribute('data-theme',t)}else if(window.matchMedia&&window.matchMedia('(prefers-color-scheme:light)').matches){document.documentElement.setAttribute('data-theme','light')}else{document.documentElement.setAttribute('data-theme','dark')}})();
  </script>
</head>
<body>
  <header class="site-header">
    <div class="header-inner">
      <nav class="header-nav">
        <a href="../index.html" class="logo">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
        </a>
        <div class="nav-links">
          <a href="../index.html" class="nav-link">Home</a>
          <a href="../docs.html" class="nav-link nav-link-active">Docs</a>
          <a href="../about.html" class="nav-link">About</a>
          <a href="https://github.com/leonletto/thrum" class="nav-link nav-link-external" target="_blank" rel="noopener">GitHub</a>
        </div>
      </nav>
    </div>
  </header>
  <main class="docs-content" style="max-width:48rem;margin:2rem auto;padding:0 1.5rem">
    <div class="docs-content-inner">
${html}
    </div>
    <p style="margin-top:2rem"><a href="../docs.html#${doc.path}">&larr; View in documentation</a></p>
  </main>
  <script>
    // Redirect browsers to the SPA for full navigation experience.
    // Crawlers (which don't execute JS) will index the static content above.
    if (window.location.search.indexOf('nospa') === -1) {
      window.location.replace('../docs.html#${doc.path}');
    }
  </script>
</body>
</html>`;

  let count = 0;
  for (const doc of docs) {
    const htmlPath = path.join(CONFIG.outputDir, doc.path);
    const html = await fs.readFile(htmlPath, 'utf-8');
    const seoPath = path.join(docsDir, doc.path);
    await fs.ensureDir(path.dirname(seoPath));
    await fs.writeFile(seoPath, seoTemplate(doc, html));
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
