import { useEffect } from 'react';

/**
 * Global keyboard shortcuts for the Thrum UI.
 *
 * - Escape: Close any open panel/dialog (handled by Radix UI automatically)
 * - Cmd+Enter / Ctrl+Enter: Submit compose (handled at ComposeBar level)
 *
 * This hook registers at the App level and handles shortcuts that
 * don't conflict with textarea input.
 */
export function useKeyboardShortcuts() {
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Don't intercept if user is typing in an input/textarea
      const target = e.target as HTMLElement;
      const isInput = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable;

      // Escape is handled by Radix UI dialogs/sheets automatically
      // We only need to handle custom shortcuts here

      // Cmd+K / Ctrl+K: Focus search (future)
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        // Future: focus search input
      }

      // Navigation shortcuts (only when not typing)
      if (!isInput) {
        // 1-5 for sidebar navigation (future enhancement)
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, []);
}
