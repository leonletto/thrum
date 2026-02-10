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
  websiteDir: path.join(__dirname, '..')
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
