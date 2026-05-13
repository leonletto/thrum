#!/usr/bin/env node
/**
 * Generate sitemap.xml and robots.txt for the Thrum website.
 *
 * Discovers every published HTML page under website/ — landing pages at the
 * root, blog posts under blog/, and SEO doc pages under docs/ — and emits
 * an XML sitemap plus a robots.txt pointing at it.
 *
 * Run after build-docs and build-blog so the sitemap reflects whatever
 * HTML those produced. Wired into `npm run build`.
 */

const fs = require('fs-extra');
const path = require('path');

const CONFIG = {
  websiteDir: path.join(__dirname, '..'),
  siteUrl: 'https://leonletto.github.io/thrum',
  // Top-level HTML files to include. 404.html and the like are excluded.
  rootPages: ['index.html', 'docs.html', 'about.html', 'blog.html'],
  // Subdirs to walk for additional HTML pages.
  walkDirs: ['blog', 'docs'],
  // Files to skip even if they match the walk.
  excludeFiles: new Set(['404.html']),
};

/**
 * Recursively collect *.html files under `dir`, returning paths relative to
 * websiteDir so they map cleanly to URL paths.
 */
async function collectHtmlFiles(dir) {
  const results = [];
  const absDir = path.join(CONFIG.websiteDir, dir);
  if (!(await fs.pathExists(absDir))) return results;

  const entries = await fs.readdir(absDir, { withFileTypes: true });
  for (const entry of entries) {
    const rel = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      results.push(...(await collectHtmlFiles(rel)));
    } else if (entry.isFile() && entry.name.endsWith('.html')) {
      if (CONFIG.excludeFiles.has(entry.name)) continue;
      results.push(rel);
    }
  }
  return results;
}

/**
 * Build the URL path that a search engine should index for a given file.
 * GitHub Pages serves `foo/index.html` as both `foo/` and `foo/index.html`;
 * we prefer the directory form for `index.html` and the explicit `.html`
 * form for everything else so canonical URLs match what `<link rel=canonical>`
 * declares on each page.
 */
function urlForFile(relPath) {
  const normalized = relPath.split(path.sep).join('/');
  if (normalized === 'index.html') return `${CONFIG.siteUrl}/`;
  return `${CONFIG.siteUrl}/${normalized}`;
}

/**
 * Build the <lastmod> string from a file's mtime. For SEO doc + blog HTML
 * this rebuilds on every `npm run build`, so the mtime tracks the build
 * rather than the source. Good enough: search engines treat lastmod as a
 * hint, and we want them to re-crawl after a deploy anyway.
 */
async function lastModForFile(relPath) {
  const abs = path.join(CONFIG.websiteDir, relPath);
  const stat = await fs.stat(abs);
  return stat.mtime.toISOString().slice(0, 10);
}

/**
 * Per-section priority/changefreq hints. Engines treat these as advisory.
 */
function hintsForUrl(relPath) {
  if (relPath === 'index.html') {
    return { priority: '1.0', changefreq: 'weekly' };
  }
  if (relPath.startsWith('blog/') || relPath === 'blog.html') {
    return { priority: '0.7', changefreq: 'weekly' };
  }
  if (relPath.startsWith('docs/') || relPath === 'docs.html') {
    return { priority: '0.8', changefreq: 'weekly' };
  }
  return { priority: '0.5', changefreq: 'monthly' };
}

function xmlEscape(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

async function buildSitemap() {
  console.log('🗺  Building sitemap.xml + robots.txt...');

  const files = new Set();
  for (const page of CONFIG.rootPages) {
    if (await fs.pathExists(path.join(CONFIG.websiteDir, page))) {
      files.add(page);
    }
  }
  for (const dir of CONFIG.walkDirs) {
    for (const f of await collectHtmlFiles(dir)) files.add(f);
  }

  const entries = [];
  for (const file of [...files].sort()) {
    entries.push({
      url: urlForFile(file),
      lastmod: await lastModForFile(file),
      ...hintsForUrl(file),
    });
  }

  const urlBlocks = entries
    .map(
      (e) =>
        `  <url>\n` +
        `    <loc>${xmlEscape(e.url)}</loc>\n` +
        `    <lastmod>${e.lastmod}</lastmod>\n` +
        `    <changefreq>${e.changefreq}</changefreq>\n` +
        `    <priority>${e.priority}</priority>\n` +
        `  </url>`
    )
    .join('\n');

  const sitemapXml =
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n` +
    `${urlBlocks}\n` +
    `</urlset>\n`;

  await fs.writeFile(path.join(CONFIG.websiteDir, 'sitemap.xml'), sitemapXml);
  console.log(`   ✓ Wrote sitemap.xml (${entries.length} URLs)`);

  const robotsTxt =
    `User-agent: *\n` +
    `Allow: /\n` +
    `\n` +
    `Sitemap: ${CONFIG.siteUrl}/sitemap.xml\n`;
  await fs.writeFile(path.join(CONFIG.websiteDir, 'robots.txt'), robotsTxt);
  console.log('   ✓ Wrote robots.txt');

  console.log('✅ Sitemap build complete!');
}

if (require.main === module) {
  buildSitemap().catch((err) => {
    console.error('Sitemap build failed:', err);
    process.exit(1);
  });
}

module.exports = { buildSitemap };
