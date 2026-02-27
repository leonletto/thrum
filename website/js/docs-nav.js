/**
 * Documentation Viewer Navigation
 * Task: thrum-235d.5
 *
 * Loads index.json, builds sidebar navigation grouped by category,
 * fetches doc HTML on click, and uses hash-based routing.
 * Supports collapsible sub-categories nested under parent categories.
 *
 * Security note: Doc HTML is fetched from same-origin assets/docs/ directory
 * (build output from our own build script). No user-supplied content.
 * innerHTML is used intentionally to render trusted build output.
 */

(function () {
  'use strict';

  var DOCS_BASE = 'assets/docs/';
  var INDEX_URL = DOCS_BASE + 'index.json';
  var STORAGE_KEY = 'thrum-docs-collapsed';

  // Display order and labels for top-level categories
  var CATEGORY_ORDER = [
    'overview',
    'quickstart',
    'webui',
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
    tools: 'Recommended Tools',
    webui: 'Web UI',
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

  // Sub-categories nested under a parent. Rendered indented and collapsible.
  // Key = parent category, value = array of child categories in display order.
  var CATEGORY_CHILDREN = {
    quickstart: ['tools']
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

  // ── Collapse State ─────────────────────────────────────────────────────

  function getCollapsedState() {
    try {
      var raw = localStorage.getItem(STORAGE_KEY);
      return raw ? JSON.parse(raw) : {};
    } catch (e) {
      return {};
    }
  }

  function saveCollapsedState(state) {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    } catch (e) {
      // Ignore storage errors
    }
  }

  function toggleCategory(cat) {
    var state = getCollapsedState();
    state[cat] = !state[cat];
    saveCollapsedState(state);
    applyCategoryState(cat, state[cat]);
  }

  function applyCategoryState(cat, collapsed) {
    var catDiv = sidebarNav.querySelector('[data-category="' + cat + '"]');
    if (!catDiv) return;
    if (collapsed) {
      catDiv.classList.add('collapsed');
    } else {
      catDiv.classList.remove('collapsed');
    }
  }

  function expandCategoryChain(cat) {
    // Expand the given category and its parent (if it's a sub-category)
    var state = getCollapsedState();
    state[cat] = false;
    applyCategoryState(cat, false);

    // Check if this category is a child — expand its parent too
    CATEGORY_ORDER.forEach(function (parentCat) {
      var children = CATEGORY_CHILDREN[parentCat];
      if (children && children.indexOf(cat) !== -1) {
        state[parentCat] = false;
        applyCategoryState(parentCat, false);
      }
    });

    saveCollapsedState(state);
  }

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

  // Build a set of all child categories for quick lookup
  var childCategories = {};
  Object.keys(CATEGORY_CHILDREN).forEach(function (parent) {
    CATEGORY_CHILDREN[parent].forEach(function (child) {
      childCategories[child] = parent;
    });
  });

  function buildCategoryBlock(cat, groups, isSubCategory) {
    if (!groups[cat]) return null;
    var label = CATEGORY_LABELS[cat] || cat;

    var catDiv = document.createElement('div');
    catDiv.className = isSubCategory ? 'sidebar-category sidebar-subcategory' : 'sidebar-category';
    catDiv.setAttribute('data-category', cat);

    var catLabel = document.createElement('span');
    catLabel.className = isSubCategory
      ? 'sidebar-category-label sidebar-subcategory-label collapsible'
      : 'sidebar-category-label';
    catDiv.appendChild(catLabel);

    if (isSubCategory) {
      // Add chevron for collapsible sub-categories
      var chevron = document.createElement('span');
      chevron.className = 'sidebar-chevron';
      chevron.textContent = '\u25B8'; // ▸
      catLabel.appendChild(chevron);

      var labelText = document.createElement('span');
      labelText.textContent = label;
      catLabel.appendChild(labelText);
    } else {
      catLabel.textContent = label;
    }

    // Container for the collapsible content (links + child sub-categories)
    var contentWrap = document.createElement('div');
    contentWrap.className = 'sidebar-category-content';

    groups[cat].forEach(function (doc) {
      var link = document.createElement('a');
      link.className = isSubCategory ? 'sidebar-link sidebar-sublink' : 'sidebar-link';
      link.href = '#' + doc.path;
      link.setAttribute('data-path', doc.path);
      link.setAttribute('data-category', cat);
      link.title = doc.description || '';
      link.textContent = doc.title;
      contentWrap.appendChild(link);
    });

    catDiv.appendChild(contentWrap);

    // Render child sub-categories inside this category
    var children = CATEGORY_CHILDREN[cat];
    if (children) {
      children.forEach(function (childCat) {
        var childBlock = buildCategoryBlock(childCat, groups, true);
        if (childBlock) {
          contentWrap.appendChild(childBlock);
        }
      });
    }

    return catDiv;
  }

  function buildSidebar(docs) {
    // Group by category
    var groups = {};
    docs.forEach(function (doc) {
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
      // Skip categories that are rendered as children of another
      if (childCategories[cat]) return;
      var block = buildCategoryBlock(cat, groups, false);
      if (block) fragment.appendChild(block);
    });

    sidebarNav.textContent = '';
    sidebarNav.appendChild(fragment);

    // Apply saved collapse state — sub-categories default to collapsed
    var state = getCollapsedState();
    Object.keys(childCategories).forEach(function (childCat) {
      if (!(childCat in state)) {
        state[childCat] = true; // Default: sub-categories start collapsed
      }
    });
    saveCollapsedState(state);

    Object.keys(state).forEach(function (cat) {
      applyCategoryState(cat, state[cat]);
    });

    // Attach click handlers
    sidebarNav.addEventListener('click', function (e) {
      // Handle collapsible label clicks
      var label = e.target.closest('.collapsible');
      if (label) {
        var catDiv = label.closest('.sidebar-category');
        if (catDiv) {
          toggleCategory(catDiv.getAttribute('data-category'));
        }
        return;
      }

      // Handle doc link clicks
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

    // Update active sidebar link and auto-expand its category
    var links = sidebarNav.querySelectorAll('.sidebar-link');
    links.forEach(function (link) {
      var isActive = link.getAttribute('data-path') === path;
      link.classList.toggle('active', isActive);
      if (isActive) {
        var cat = link.getAttribute('data-category');
        if (cat) expandCategoryChain(cat);
      }
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
