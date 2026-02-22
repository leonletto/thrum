import { useEffect } from 'react';
import {
  selectLiveFeed,
  selectMyInbox,
  selectSettings,
  selectGroup,
  selectAgent,
} from '@thrum/shared-logic';

export interface KeyboardShortcutOptions {
  /** First group name to navigate to with key '3' */
  firstGroupName?: string | null;
  /** First agent ID to navigate to with key '4' */
  firstAgentId?: string | null;
}

/**
 * Global keyboard shortcuts for the Thrum UI.
 *
 * Shortcuts:
 * - 1: Navigate to Live Feed
 * - 2: Navigate to My Inbox
 * - 3: Navigate to the first group (if available)
 * - 4: Navigate to the first agent (if available)
 * - 5: Navigate to Settings
 * - Cmd+K / Ctrl+K: Focus the sidebar search (or main content if none)
 * - Escape: Return focus to main content (Radix UI dialogs handle their own Escape)
 *
 * Number shortcuts are disabled when the user is typing in an input or textarea.
 */
export function useKeyboardShortcuts(options: KeyboardShortcutOptions = {}) {
  const { firstGroupName, firstAgentId } = options;

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      const target = e.target as HTMLElement;
      const isInput =
        target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.isContentEditable ||
        target.getAttribute?.('contenteditable') === 'true';

      // Cmd+K / Ctrl+K: Focus search or main content
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        // Try to focus a search input in the sidebar first
        const searchInput = document.querySelector<HTMLInputElement>(
          'aside input[type="search"], aside input[type="text"], aside input:not([type])'
        );
        if (searchInput) {
          searchInput.focus();
        } else {
          // Fall back to focusing main content
          const main = document.getElementById('main-content');
          if (main) {
            main.focus();
          }
        }
        return;
      }

      // Escape: Return focus to main content
      // Radix UI dialogs handle their own Escape; this handles the non-dialog case.
      if (e.key === 'Escape' && !isInput) {
        const main = document.getElementById('main-content');
        if (main) {
          main.focus();
        }
        return;
      }

      // Number key navigation shortcuts (only when not typing)
      if (isInput) return;

      switch (e.key) {
        case '1':
          e.preventDefault();
          selectLiveFeed();
          break;

        case '2':
          e.preventDefault();
          selectMyInbox();
          break;

        case '3':
          e.preventDefault();
          if (firstGroupName) {
            selectGroup(firstGroupName);
          }
          break;

        case '4':
          e.preventDefault();
          if (firstAgentId) {
            selectAgent(firstAgentId);
          }
          break;

        case '5':
          e.preventDefault();
          selectSettings();
          break;
      }
    }

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [firstGroupName, firstAgentId]);
}
