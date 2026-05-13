#!/usr/bin/env node

/**
 * Thrum Blog Build Script
 *
 * Processes markdown files from /blog directory into:
 * - HTML files (blog/*.html — full standalone pages, not SPA shells)
 * - Blog index page (blog.html — lists all posts in reverse chronological order)
 *
 * Designed to mirror the structure of build-docs.js but simpler — blog posts
 * are full pages that match the landing/about page style. No SPA, no search
 * index, no per-doc navigation: each post is a standalone page that links
 * back to the index.
 *
 * Frontmatter schema (per post):
 *   title:       Post title (required)
 *   slug:        URL slug (optional — defaults to filename without .md)
 *   date:        ISO date (YYYY-MM-DD, required for ordering)
 *   author:      Author name (optional — defaults to "Leon Letto")
 *   description: One-line summary used on the index card and meta tags
 *   tags:        Array of tag strings
 *   video:       Optional YouTube video ID for hero embed at top of post
 *   draft:       If true, the post is skipped at build time (no HTML emitted,
 *                not listed on the index). Flip to false (or remove the field)
 *                to publish.
 *
 * Inline video shortcode in markdown body:
 *   {{< youtube VIDEO_ID >}}
 *
 * Replaced with a responsive iframe wrapper. Posts without videos render
 * normally — the shortcode is purely opt-in.
 */

const fs = require('fs-extra');
const path = require('path');
const matter = require('gray-matter');
const { marked } = require('marked');
const hljs = require('highlight.js');

const CONFIG = {
  blogDir: path.join(__dirname, '../blog'),
  websiteDir: path.join(__dirname, '..'),
  siteUrl: 'https://leonletto.github.io/thrum',
  defaultAuthor: 'Leon Letto',
};

/**
 * Marked renderer with syntax highlighting (same approach as build-docs.js).
 */
const renderer = new marked.Renderer();
renderer.code = function ({ text, lang, escaped }) {
  const safeLang = lang ? lang.replace(/[^a-zA-Z0-9\-_]/g, '') : '';
  if (safeLang && hljs.getLanguage(safeLang)) {
    try {
      const highlighted = hljs.highlight(text, { language: safeLang }).value;
      return `<pre><code class="language-${safeLang} hljs">${highlighted}</code></pre>\n`;
    } catch (err) {
      console.error(`Highlight error for ${safeLang}:`, err.message);
    }
  }
  const escapedText = escaped ? text : escapeHtml(text);
  const langClass = safeLang ? ` class="language-${safeLang}"` : '';
  return `<pre><code${langClass}>${escapedText}</code></pre>\n`;
};

marked.setOptions({ renderer, breaks: false, gfm: true });

function escapeHtml(html) {
  return html
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function escapeAttr(str) {
  return (str || '')
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

/**
 * Replace {{< youtube ID >}} shortcodes with responsive iframe wrappers.
 * Whitelisted ID format prevents arbitrary HTML injection.
 */
function expandShortcodes(html) {
  return html.replace(
    /\{\{&lt;\s*youtube\s+([a-zA-Z0-9_-]{6,20})\s*&gt;\}\}/g,
    (_match, id) => {
      return `<div class="video-embed"><iframe src="https://www.youtube.com/embed/${id}" title="YouTube video" frameborder="0" allow="accelerometer; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" allowfullscreen loading="lazy"></iframe></div>`;
    }
  );
}

/**
 * Format an ISO date string as "April 24, 2026".
 */
function formatDate(iso) {
  if (!iso) return '';
  const d = new Date(iso + 'T00:00:00Z');
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    timeZone: 'UTC',
  });
}

/**
 * Estimate reading time at 220 words/minute.
 */
function readingTime(markdown) {
  const words = markdown.split(/\s+/).filter(Boolean).length;
  const mins = Math.max(1, Math.round(words / 220));
  return `${mins} min read`;
}

/**
 * Find all blog post markdown files (top-level only).
 */
async function findPostFiles(dir) {
  if (!(await fs.pathExists(dir))) return [];
  const entries = await fs.readdir(dir, { withFileTypes: true });
  return entries
    .filter((e) => e.isFile() && e.name.endsWith('.md'))
    .map((e) => path.join(dir, e.name));
}

/**
 * Parse a single post: returns { meta, html, markdown, slug }.
 */
async function parsePost(filePath) {
  const raw = await fs.readFile(filePath, 'utf-8');
  const { data: frontmatter, content: markdown } = matter(raw);

  const slug =
    frontmatter.slug || path.basename(filePath, '.md').toLowerCase();

  let body = marked(markdown);
  body = expandShortcodes(body);

  return {
    slug,
    markdown,
    body,
    meta: {
      title: frontmatter.title || slug,
      date: frontmatter.date || '',
      author: frontmatter.author || CONFIG.defaultAuthor,
      description: frontmatter.description || '',
      tags: Array.isArray(frontmatter.tags) ? frontmatter.tags : [],
      video: frontmatter.video || '',
      draft: frontmatter.draft === true,
    },
  };
}

/**
 * Render a single post as a standalone HTML page.
 */
function renderPostPage(post) {
  const { meta, body, slug } = post;
  const heroVideo = meta.video
    ? `<div class="video-embed video-embed-hero"><iframe src="https://www.youtube.com/embed/${meta.video}" title="${escapeAttr(meta.title)}" frameborder="0" allow="accelerometer; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share" allowfullscreen loading="lazy"></iframe></div>`
    : '';

  const tagHtml = meta.tags.length
    ? `<div class="post-tags">${meta.tags.map((t) => `<span class="post-tag">${escapeHtml(t)}</span>`).join('')}</div>`
    : '';

  const canonicalUrl = `${CONFIG.siteUrl}/blog/${slug}.html`;

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta name="color-scheme" content="dark light">
  <title>${escapeHtml(meta.title)} · Thrum Blog</title>
  <meta name="description" content="${escapeAttr(meta.description)}">
  <link rel="canonical" href="${canonicalUrl}">
  <!-- Open Graph -->
  <meta property="og:type" content="article">
  <meta property="og:title" content="${escapeAttr(meta.title)}">
  <meta property="og:description" content="${escapeAttr(meta.description)}">
  <meta property="og:url" content="${canonicalUrl}">
  <meta property="og:site_name" content="Thrum">
  <meta property="og:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <meta property="article:published_time" content="${escapeAttr(meta.date)}">
  <meta property="article:author" content="${escapeAttr(meta.author)}">
  <!-- Twitter Card -->
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="${escapeAttr(meta.title)}">
  <meta name="twitter:description" content="${escapeAttr(meta.description)}">
  <meta name="twitter:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>⚡</text></svg>">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600;700&family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
  <link rel="stylesheet" href="../css/theme.css">
  <link rel="stylesheet" href="../css/landing.css">
  <link rel="stylesheet" href="../css/blog.css">
  <script>
    (function(){var t=localStorage.getItem('thrum-theme');if(t){document.documentElement.setAttribute('data-theme',t)}else if(window.matchMedia&&window.matchMedia('(prefers-color-scheme:light)').matches){document.documentElement.setAttribute('data-theme','light')}else{document.documentElement.setAttribute('data-theme','dark')}})();
  </script>
  <!-- Privacy-friendly analytics by Plausible -->
  <script async src="https://plausible.io/js/pa-H8-fSgAXYxNjVrtePBTu3.js"></script>
  <script>
    window.plausible=window.plausible||function(){(plausible.q=plausible.q||[]).push(arguments)},plausible.init=plausible.init||function(i){plausible.o=i||{}};
    plausible.init()
  </script>
</head>
<body>
  <header class="site-header">
    <div class="container">
      <nav class="header-nav">
        <a href="../index.html" class="logo">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
        </a>
        <div class="nav-links">
          <a href="../index.html" class="nav-link">Home</a>
          <a href="../docs.html" class="nav-link">Docs</a>
          <a href="../blog.html" class="nav-link nav-link-active">Blog</a>
          <a href="../about.html" class="nav-link">About</a>
          <a href="https://github.com/leonletto/thrum" class="nav-link nav-link-external" target="_blank" rel="noopener">GitHub</a>
          <button class="theme-toggle" id="theme-toggle" aria-label="Toggle theme">
            <span class="icon-sun">&#x2600;</span>
            <span class="icon-moon">&#x263E;</span>
          </button>
        </div>
      </nav>
    </div>
  </header>

  <article class="section blog-post-section">
    <div class="container-narrow">
      <p class="post-eyebrow"><a href="../blog.html">&larr; All posts</a></p>
      <h1 class="post-title">${escapeHtml(meta.title)}</h1>
      <p class="post-meta">
        <span class="post-date">${escapeHtml(formatDate(meta.date))}</span>
        <span class="post-meta-sep">&middot;</span>
        <span class="post-author">${escapeHtml(meta.author)}</span>
        <span class="post-meta-sep">&middot;</span>
        <span class="post-reading-time">${escapeHtml(readingTime(post.markdown))}</span>
      </p>
      ${tagHtml}
      ${heroVideo}
      <div class="post-body">
${body}
      </div>
      <hr class="post-divider">
      <p class="post-footer-nav"><a href="../blog.html">&larr; Back to all posts</a></p>
    </div>
  </article>

  <footer class="site-footer">
    <div class="container">
      <div class="footer-content">
        <div class="footer-brand">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
          <span class="text-muted">&middot; Persistent messaging for AI agents</span>
        </div>
        <div class="footer-links">
          <a href="../docs.html">Documentation</a>
          <a href="../blog.html">Blog</a>
          <a href="https://github.com/leonletto/thrum" target="_blank" rel="noopener">GitHub</a>
          <a href="../llms.txt">llms.txt</a>
          <a href="../llms-full.txt">llms-full.txt</a>
        </div>
      </div>
    </div>
  </footer>

  <script>
    document.getElementById('theme-toggle').addEventListener('click', function() {
      var current = document.documentElement.getAttribute('data-theme');
      var next = current === 'light' ? 'dark' : 'light';
      document.documentElement.setAttribute('data-theme', next);
      localStorage.setItem('thrum-theme', next);
    });
  </script>
</body>
</html>
`;
}

/**
 * Render the blog index page listing all posts.
 */
function renderIndexPage(posts) {
  const cards = posts
    .map((post) => {
      const { meta, slug } = post;
      const tagHtml = meta.tags.length
        ? `<div class="post-tags post-tags-small">${meta.tags.map((t) => `<span class="post-tag">${escapeHtml(t)}</span>`).join('')}</div>`
        : '';
      const videoBadge = meta.video
        ? `<span class="post-badge post-badge-video" title="Includes video">&#9655; Video</span>`
        : '';
      return `<article class="card post-card">
            <a href="blog/${slug}.html" class="post-card-link">
              <div class="post-card-meta">
                <span class="post-date">${escapeHtml(formatDate(meta.date))}</span>
                ${videoBadge}
              </div>
              <h3 class="post-card-title">${escapeHtml(meta.title)}</h3>
              <p class="post-card-description">${escapeHtml(meta.description)}</p>
              ${tagHtml}
              <span class="post-card-readmore">Read more &rarr;</span>
            </a>
          </article>`;
    })
    .join('\n');

  const empty = `<p class="text-muted" style="text-align:center;">No posts yet. Check back soon.</p>`;

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta name="color-scheme" content="dark light">
  <title>Thrum Blog</title>
  <meta name="description" content="How Thrum was built, what I went through along the way, and why it turned out the way it did.">
  <!-- Open Graph -->
  <meta property="og:type" content="website">
  <meta property="og:title" content="Thrum Blog">
  <meta property="og:description" content="How Thrum was built, what I went through along the way, and why it turned out the way it did.">
  <meta property="og:url" content="${CONFIG.siteUrl}/blog.html">
  <meta property="og:site_name" content="Thrum">
  <meta property="og:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <meta property="og:image:width" content="1200">
  <meta property="og:image:height" content="630">
  <!-- Twitter Card -->
  <meta name="twitter:card" content="summary_large_image">
  <meta name="twitter:title" content="Thrum Blog">
  <meta name="twitter:description" content="How Thrum was built, what I went through along the way, and why it turned out the way it did.">
  <meta name="twitter:image" content="${CONFIG.siteUrl}/img/social-card.png">
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>⚡</text></svg>">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600;700&family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
  <link rel="stylesheet" href="css/theme.css">
  <link rel="stylesheet" href="css/landing.css">
  <link rel="stylesheet" href="css/blog.css">
  <script>
    (function(){var t=localStorage.getItem('thrum-theme');if(t){document.documentElement.setAttribute('data-theme',t)}else if(window.matchMedia&&window.matchMedia('(prefers-color-scheme:light)').matches){document.documentElement.setAttribute('data-theme','light')}else{document.documentElement.setAttribute('data-theme','dark')}})();
  </script>
  <!-- Privacy-friendly analytics by Plausible -->
  <script async src="https://plausible.io/js/pa-H8-fSgAXYxNjVrtePBTu3.js"></script>
  <script>
    window.plausible=window.plausible||function(){(plausible.q=plausible.q||[]).push(arguments)},plausible.init=plausible.init||function(i){plausible.o=i||{}};
    plausible.init()
  </script>
</head>
<body>
  <header class="site-header">
    <div class="container">
      <nav class="header-nav">
        <a href="index.html" class="logo">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
        </a>
        <div class="nav-links">
          <a href="index.html" class="nav-link">Home</a>
          <a href="docs.html" class="nav-link">Docs</a>
          <a href="blog.html" class="nav-link nav-link-active">Blog</a>
          <a href="about.html" class="nav-link">About</a>
          <a href="https://github.com/leonletto/thrum" class="nav-link nav-link-external" target="_blank" rel="noopener">GitHub</a>
          <button class="theme-toggle" id="theme-toggle" aria-label="Toggle theme">
            <span class="icon-sun">&#x2600;</span>
            <span class="icon-moon">&#x263E;</span>
          </button>
        </div>
      </nav>
    </div>
  </header>

  <section class="section blog-index-hero">
    <div class="container-narrow">
      <h1 class="section-title">Blog</h1>
      <p class="section-lead" style="text-align: left;">
        How Thrum was built, what I went through along the way, and why it
        turned out the way it did.
      </p>
    </div>
  </section>

  <section class="section section-alt blog-index-list">
    <div class="container-narrow">
      <div class="post-grid">
${posts.length ? cards : empty}
      </div>
    </div>
  </section>

  <footer class="site-footer">
    <div class="container">
      <div class="footer-content">
        <div class="footer-brand">
          <span class="logo-glyph">&gt;_</span>
          <span class="logo-text">thrum</span>
          <span class="text-muted">&middot; Persistent messaging for AI agents</span>
        </div>
        <div class="footer-links">
          <a href="docs.html">Documentation</a>
          <a href="blog.html">Blog</a>
          <a href="https://github.com/leonletto/thrum" target="_blank" rel="noopener">GitHub</a>
          <a href="llms.txt">llms.txt</a>
          <a href="llms-full.txt">llms-full.txt</a>
        </div>
      </div>
    </div>
  </footer>

  <script>
    document.getElementById('theme-toggle').addEventListener('click', function() {
      var current = document.documentElement.getAttribute('data-theme');
      var next = current === 'light' ? 'dark' : 'light';
      document.documentElement.setAttribute('data-theme', next);
      localStorage.setItem('thrum-theme', next);
    });
  </script>
</body>
</html>
`;
}

async function buildBlog() {
  console.log('📝 Thrum Blog Build Script');
  console.log('📂 Source:', CONFIG.blogDir);
  console.log('');

  await fs.ensureDir(CONFIG.blogDir);

  const files = await findPostFiles(CONFIG.blogDir);
  console.log(`📄 Found ${files.length} blog post(s)`);

  const posts = [];
  for (const file of files) {
    const post = await parsePost(file);
    if (post.meta.draft) {
      console.log(`   · ${post.slug} (draft — skipped)`);
      // Remove any stale rendered HTML from a previous publish so the post
      // can't remain reachable at its slug URL after being marked draft.
      const stalePath = path.join(CONFIG.blogDir, `${post.slug}.html`);
      if (await fs.pathExists(stalePath)) {
        await fs.remove(stalePath);
        console.log(`     removed stale ${post.slug}.html`);
      }
      continue;
    }
    posts.push(post);
    console.log(`   ✓ ${post.slug} (${post.meta.date})`);
  }

  // Sort newest first
  posts.sort((a, b) => (a.meta.date < b.meta.date ? 1 : -1));

  // Write individual post pages
  for (const post of posts) {
    const outPath = path.join(CONFIG.blogDir, `${post.slug}.html`);
    await fs.writeFile(outPath, renderPostPage(post));
  }
  console.log(`📄 Wrote ${posts.length} post page(s) to blog/`);

  // Write blog index
  const indexPath = path.join(CONFIG.websiteDir, 'blog.html');
  await fs.writeFile(indexPath, renderIndexPage(posts));
  console.log(`📋 Wrote blog index to blog.html`);

  console.log('');
  console.log('✅ Blog build complete!');
}

if (require.main === module) {
  buildBlog().catch((err) => {
    console.error('❌ Blog build failed:', err);
    process.exit(1);
  });
}

module.exports = { buildBlog };
