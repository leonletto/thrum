/**
 * Documentation Viewer Navigation
 * Task: thrum-235d.5
 *
 * Loads index.json, builds sidebar navigation grouped by category,
 * fetches doc HTML on click, and uses hash-based routing.
 *
 * Security note: Doc HTML is fetched from same-origin assets/docs/ directory
 * (build output from our own build script). No user-supplied content.
 * innerHTML is used intentionally to render trusted build output.
 */

(function () {
  'use strict';

  var DOCS_BASE = 'assets/docs/';
  var INDEX_URL = DOCS_BASE + 'index.json';

  // Display order and labels for categories
  var CATEGORY_ORDER = [
    'overview',
    'quickstart',
    'cli',
    'messaging',
    'identity',
    'guides',
    'api',
    'daemon',
    'mcp',
    'sync',
    'development'
  ];

  var CATEGORY_LABELS = {
    overview: 'Overview',
    quickstart: 'Getting Started',
    cli: 'CLI',
    messaging: 'Messaging',
    identity: 'Identity',
    guides: 'Guides',
    api: 'API Reference',
    daemon: 'Daemon',
    mcp: 'MCP Server',
    sync: 'Sync',
    development: 'Development'
  };

  var sidebarNav = document.getElementById('sidebar-nav');
  var contentInner = document.getElementById('docs-content-inner');
  var sidebarToggle = document.getElementById('sidebar-toggle');
  var sidebar = document.getElementById('docs-sidebar');

  var docsIndex = [];
  var currentPath = null;

  // Expose for search.js
  window.__docsNav = {
    loadDoc: function (path) { loadDoc(path); },
    getIndex: function () { return docsIndex; }
  };

  // ── Bootstrap ──────────────────────────────────────────────────────────

  fetch(INDEX_URL)
    .then(function (res) { return res.json(); })
    .then(function (data) {
      docsIndex = data;
      buildSidebar(data);
      navigateFromHash();
    })
    .catch(function () {
      contentInner.textContent = 'Failed to load documentation index.';
      contentInner.className = 'docs-content-inner docs-error';
    });

  // ── Sidebar ────────────────────────────────────────────────────────────

  function buildSidebar(docs) {
    // Group by category
    var groups = {};
    docs.forEach(function (doc) {
      // Skip uncategorized/plan docs from sidebar
      if (doc.category === 'uncategorized') return;
      if (!groups[doc.category]) groups[doc.category] = [];
      groups[doc.category].push(doc);
    });

    // Sort within each group by order
    Object.keys(groups).forEach(function (cat) {
      groups[cat].sort(function (a, b) { return a.order - b.order; });
    });

    // Build sidebar using DOM methods
    var fragment = document.createDocumentFragment();

    CATEGORY_ORDER.forEach(function (cat) {
      if (!groups[cat]) return;
      var label = CATEGORY_LABELS[cat] || cat;

      var catDiv = document.createElement('div');
      catDiv.className = 'sidebar-category';
      catDiv.setAttribute('data-category', cat);

      var catLabel = document.createElement('span');
      catLabel.className = 'sidebar-category-label';
      catLabel.textContent = label;
      catDiv.appendChild(catLabel);

      groups[cat].forEach(function (doc) {
        var link = document.createElement('a');
        link.className = 'sidebar-link';
        link.href = '#' + doc.path;
        link.setAttribute('data-path', doc.path);
        link.title = doc.description || '';
        link.textContent = doc.title;
        catDiv.appendChild(link);
      });

      fragment.appendChild(catDiv);
    });

    sidebarNav.textContent = '';
    sidebarNav.appendChild(fragment);

    // Attach click handlers
    sidebarNav.addEventListener('click', function (e) {
      var link = e.target.closest('.sidebar-link');
      if (!link) return;
      e.preventDefault();
      var path = link.getAttribute('data-path');
      loadDoc(path);
      // Close mobile sidebar
      if (sidebar.classList.contains('open')) {
        sidebar.classList.remove('open');
      }
    });
  }

  // ── Mobile Toggle ──────────────────────────────────────────────────────

  if (sidebarToggle && sidebar) {
    sidebarToggle.addEventListener('click', function () {
      sidebar.classList.toggle('open');
    });
  }

  // ── Navigation ─────────────────────────────────────────────────────────

  function navigateFromHash() {
    var hash = window.location.hash.slice(1);
    if (hash) {
      loadDoc(hash);
    } else {
      // Default to overview
      loadDoc('overview.html');
    }
  }

  window.addEventListener('hashchange', navigateFromHash);

  function loadDoc(path) {
    if (path === currentPath) return;
    currentPath = path;

    // Update hash without triggering hashchange
    if (window.location.hash.slice(1) !== path) {
      history.pushState(null, '', '#' + path);
    }

    // Update active sidebar link
    var links = sidebarNav.querySelectorAll('.sidebar-link');
    links.forEach(function (link) {
      link.classList.toggle('active', link.getAttribute('data-path') === path);
    });

    // Update page title
    var doc = docsIndex.find(function (d) { return d.path === path; });
    if (doc) {
      document.title = doc.title + ' \u2014 Thrum';
    }

    // Show loading state
    contentInner.textContent = 'Loading...';
    contentInner.className = 'docs-content-inner docs-loading';

    // Fetch content from same-origin build output (trusted)
    fetch(DOCS_BASE + path)
      .then(function (res) {
        if (!res.ok) throw new Error('Not found');
        return res.text();
      })
      .then(function (html) {
        // Trusted same-origin build output from our build-docs.js script
        // This is NOT user-supplied content - it's pre-built HTML from markdown
        contentInner.className = 'docs-content-inner';
        setTrustedContent(contentInner, html);
        rewriteInternalLinks();
        window.scrollTo(0, 0);
      })
      .catch(function () {
        contentInner.textContent = 'Document not found: ' + path;
        contentInner.className = 'docs-content-inner docs-error';
      });
  }

  /**
   * Set pre-built HTML content from our build pipeline.
   * The HTML is generated by scripts/build-docs.js from markdown source
   * files within the same repository - not from user input or external sources.
   */
  function setTrustedContent(el, html) {
    // Using a document fragment via DOMParser for safety
    var parser = new DOMParser();
    var parsed = parser.parseFromString(html, 'text/html');
    el.textContent = '';
    // Move all child nodes from parsed body into our container
    while (parsed.body.firstChild) {
      el.appendChild(parsed.body.firstChild);
    }
  }

  function rewriteInternalLinks() {
    var links = contentInner.querySelectorAll('a[href]');
    links.forEach(function (a) {
      var href = a.getAttribute('href');
      // Skip external links and anchors
      if (!href || href.startsWith('http') || href.startsWith('#') || href.startsWith('mailto:')) return;
      // Convert .md links to .html (markdown source references)
      if (href.endsWith('.md') || href.indexOf('.md#') !== -1) {
        href = href.replace(/\.md(#|$)/, '.html$1');
      }
      // Rewrite relative .html links to hash navigation
      if (href.endsWith('.html') || href.indexOf('.html#') !== -1) {
        a.setAttribute('href', '#' + href);
        a.addEventListener('click', function (e) {
          e.preventDefault();
          var target = this.getAttribute('href').slice(1);
          loadDoc(target);
        });
      }
    });
  }

})();
