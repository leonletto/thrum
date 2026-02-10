/**
 * Full-Text Search
 * Task: thrum-235d.6
 *
 * Loads the pre-built MiniSearch index, wires up the search input,
 * and renders results in a dropdown overlay.
 *
 * The tokenizer MUST match the one used in scripts/build-docs.js
 * so that deserialization works correctly.
 */

(function () {
  'use strict';

  var SEARCH_INDEX_URL = 'assets/docs/search-index.json';
  var CATEGORY_LABELS = {
    overview: 'Overview',
    quickstart: 'Getting Started',
    cli: 'CLI',
    messaging: 'Messaging',
    identity: 'Identity',
    api: 'API Reference',
    daemon: 'Daemon',
    mcp: 'MCP Server',
    sync: 'Sync',
    development: 'Development'
  };

  var searchInput = document.getElementById('search-input');
  var searchResults = document.getElementById('search-results');
  var searchIndex = null;
  var debounceTimer = null;
  var selectedIdx = -1;

  if (!searchInput || !searchResults) return;

  // ── Custom tokenizer (must match build-docs.js) ───────────────────────

  function customTokenize(text) {
    var matches = text.toLowerCase().match(/[\w][\w\-_]*/g);
    return matches || [];
  }

  // ── Load index ────────────────────────────────────────────────────────

  fetch(SEARCH_INDEX_URL)
    .then(function (res) {
      if (!res.ok) throw new Error('Failed to load search index');
      return res.text();
    })
    .then(function (json) {
      searchIndex = MiniSearch.loadJSON(json, {
        fields: ['title', 'description', 'content', 'tags'],
        storeFields: ['title', 'description', 'path', 'category'],
        tokenize: customTokenize,
        searchOptions: {
          boost: { title: 2, description: 1.5 },
          fuzzy: 0.2,
          prefix: true
        }
      });
    })
    .catch(function () {
      searchIndex = null;
    });

  // ── Search input handling ─────────────────────────────────────────────

  searchInput.addEventListener('input', function () {
    clearTimeout(debounceTimer);
    var query = this.value.trim();

    if (!query || !searchIndex) {
      hideResults();
      return;
    }

    debounceTimer = setTimeout(function () {
      runSearch(query);
    }, 150);
  });

  searchInput.addEventListener('focus', function () {
    if (this.value.trim() && searchResults.children.length > 0) {
      searchResults.classList.add('visible');
    }
  });

  // Close on outside click
  document.addEventListener('click', function (e) {
    if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
      hideResults();
    }
  });

  // ── Keyboard navigation ───────────────────────────────────────────────

  searchInput.addEventListener('keydown', function (e) {
    var items = searchResults.querySelectorAll('.search-result-item');
    if (!items.length) return;

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      selectedIdx = Math.min(selectedIdx + 1, items.length - 1);
      updateSelection(items);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      selectedIdx = Math.max(selectedIdx - 1, 0);
      updateSelection(items);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (selectedIdx >= 0 && selectedIdx < items.length) {
        var path = items[selectedIdx].getAttribute('data-path');
        if (path) navigateTo(path);
      }
    } else if (e.key === 'Escape') {
      hideResults();
      searchInput.blur();
    }
  });

  // Ctrl+K / Cmd+K shortcut
  document.addEventListener('keydown', function (e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
      e.preventDefault();
      searchInput.focus();
      searchInput.select();
    }
  });

  // ── Search execution ──────────────────────────────────────────────────

  function runSearch(query) {
    var results = searchIndex.search(query).slice(0, 10);
    selectedIdx = -1;

    if (results.length === 0) {
      showNoResults(query);
      return;
    }

    renderResults(results);
  }

  // ── Render ────────────────────────────────────────────────────────────

  function renderResults(results) {
    var fragment = document.createDocumentFragment();

    results.forEach(function (result) {
      var item = document.createElement('button');
      item.className = 'search-result-item';
      item.setAttribute('data-path', result.path);
      item.type = 'button';

      var titleEl = document.createElement('span');
      titleEl.className = 'search-result-title';
      titleEl.textContent = result.title;

      var metaEl = document.createElement('span');
      metaEl.className = 'search-result-meta';
      var catLabel = CATEGORY_LABELS[result.category] || result.category;
      metaEl.textContent = catLabel;

      if (result.description) {
        var descEl = document.createElement('span');
        descEl.className = 'search-result-desc';
        descEl.textContent = truncate(result.description, 100);
        item.appendChild(titleEl);
        item.appendChild(descEl);
        item.appendChild(metaEl);
      } else {
        item.appendChild(titleEl);
        item.appendChild(metaEl);
      }

      item.addEventListener('click', function () {
        navigateTo(result.path);
      });

      fragment.appendChild(item);
    });

    searchResults.textContent = '';
    searchResults.appendChild(fragment);
    searchResults.classList.add('visible');
  }

  function showNoResults(query) {
    searchResults.textContent = '';
    var empty = document.createElement('div');
    empty.className = 'search-no-results';
    empty.textContent = 'No results for "' + query + '"';
    searchResults.appendChild(empty);
    searchResults.classList.add('visible');
  }

  function hideResults() {
    searchResults.classList.remove('visible');
    selectedIdx = -1;
  }

  function updateSelection(items) {
    items.forEach(function (item, i) {
      item.classList.toggle('selected', i === selectedIdx);
    });
    if (selectedIdx >= 0) {
      items[selectedIdx].scrollIntoView({ block: 'nearest' });
    }
  }

  function navigateTo(path) {
    hideResults();
    searchInput.value = '';
    if (window.__docsNav) {
      window.__docsNav.loadDoc(path);
    } else {
      window.location.hash = path;
    }
    // Close mobile sidebar
    var sidebar = document.getElementById('docs-sidebar');
    if (sidebar && sidebar.classList.contains('open')) {
      sidebar.classList.remove('open');
    }
  }

  function truncate(str, len) {
    if (str.length <= len) return str;
    return str.substring(0, len) + '\u2026';
  }

})();
