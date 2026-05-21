/**
 * Sidebar sub-category collapse handler for static per-doc pages.
 *
 * The sidebar HTML is pre-rendered server-side (build-docs.js → buildSpaSidebar).
 * This script wires the .collapsible click handler + localStorage persistence
 * for sub-category expand/collapse, mirroring js/docs-nav.js's behavior but
 * without rebuilding the sidebar or fetching doc content (those happen at
 * build time for per-doc pages).
 *
 * Storage key matches docs-nav.js so state is shared with the legacy SPA.
 */
(function () {
  'use strict';

  var STORAGE_KEY = 'thrum-docs-collapsed';
  var sidebarNav = document.getElementById('sidebar-nav');
  if (!sidebarNav) return;

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
      // ignore
    }
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

  // Discover sub-categories from the pre-rendered DOM (anything with the
  // .sidebar-subcategory class) and default them to collapsed on first visit
  // unless the user has already toggled them.
  var subCats = sidebarNav.querySelectorAll('.sidebar-subcategory');
  var state = getCollapsedState();
  subCats.forEach(function (el) {
    var cat = el.getAttribute('data-category');
    if (!cat) return;
    if (!(cat in state)) {
      state[cat] = true; // default collapsed
    }
  });
  saveCollapsedState(state);

  // Apply current state to every category we know about
  Object.keys(state).forEach(function (cat) {
    applyCategoryState(cat, state[cat]);
  });

  // Click handler on the collapsible labels
  sidebarNav.addEventListener('click', function (e) {
    var label = e.target.closest('.collapsible');
    if (!label) return;
    var catDiv = label.closest('.sidebar-category');
    if (!catDiv) return;
    var cat = catDiv.getAttribute('data-category');
    if (!cat) return;
    e.preventDefault();
    var s = getCollapsedState();
    s[cat] = !s[cat];
    saveCollapsedState(s);
    applyCategoryState(cat, s[cat]);
  });
})();
