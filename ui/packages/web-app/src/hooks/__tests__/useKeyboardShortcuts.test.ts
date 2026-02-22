import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useKeyboardShortcuts } from '../useKeyboardShortcuts';

// Mock all navigation actions from shared-logic
vi.mock('@thrum/shared-logic', () => ({
  selectLiveFeed: vi.fn(),
  selectMyInbox: vi.fn(),
  selectSettings: vi.fn(),
  selectGroup: vi.fn(),
  selectAgent: vi.fn(),
}));

import {
  selectLiveFeed,
  selectMyInbox,
  selectSettings,
  selectGroup,
  selectAgent,
} from '@thrum/shared-logic';

/** Elements added during a test, cleaned up in afterEach */
const createdElements: HTMLElement[] = [];

function addToBody(el: HTMLElement): HTMLElement {
  document.body.appendChild(el);
  createdElements.push(el);
  return el;
}

/**
 * Fire a keydown event.
 *
 * When `target` is provided the event is dispatched on that element (it bubbles
 * up to window so the hook's listener picks it up, and `e.target` will be the
 * element). Without a target the event is dispatched on window directly.
 */
function fireKeyDown(
  key: string,
  options: Partial<KeyboardEventInit> = {},
  target?: HTMLElement
) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...options });
  if (target) {
    target.dispatchEvent(event);
  } else {
    window.dispatchEvent(event);
  }
}

describe('useKeyboardShortcuts', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    // Remove any elements created during the test
    for (const el of createdElements) {
      el.remove();
    }
    createdElements.length = 0;
  });

  describe('hook setup', () => {
    it('registers and cleans up the keydown event listener', () => {
      const addSpy = vi.spyOn(window, 'addEventListener');
      const removeSpy = vi.spyOn(window, 'removeEventListener');

      const { unmount } = renderHook(() => useKeyboardShortcuts());

      expect(addSpy).toHaveBeenCalledWith('keydown', expect.any(Function));

      unmount();

      expect(removeSpy).toHaveBeenCalledWith('keydown', expect.any(Function));

      addSpy.mockRestore();
      removeSpy.mockRestore();
    });
  });

  describe('number key navigation', () => {
    it('navigates to Live Feed on key "1"', () => {
      renderHook(() => useKeyboardShortcuts());
      fireKeyDown('1');
      expect(selectLiveFeed).toHaveBeenCalledTimes(1);
    });

    it('navigates to My Inbox on key "2"', () => {
      renderHook(() => useKeyboardShortcuts());
      fireKeyDown('2');
      expect(selectMyInbox).toHaveBeenCalledTimes(1);
    });

    it('navigates to first group on key "3" when firstGroupName is provided', () => {
      renderHook(() =>
        useKeyboardShortcuts({ firstGroupName: 'everyone', firstAgentId: null })
      );
      fireKeyDown('3');
      expect(selectGroup).toHaveBeenCalledWith('everyone');
    });

    it('does not call selectGroup on key "3" when firstGroupName is null', () => {
      renderHook(() =>
        useKeyboardShortcuts({ firstGroupName: null, firstAgentId: null })
      );
      fireKeyDown('3');
      expect(selectGroup).not.toHaveBeenCalled();
    });

    it('does not call selectGroup on key "3" when firstGroupName is undefined (default)', () => {
      renderHook(() => useKeyboardShortcuts());
      fireKeyDown('3');
      expect(selectGroup).not.toHaveBeenCalled();
    });

    it('navigates to first agent on key "4" when firstAgentId is provided', () => {
      renderHook(() =>
        useKeyboardShortcuts({ firstGroupName: null, firstAgentId: 'agent:claude-daemon' })
      );
      fireKeyDown('4');
      expect(selectAgent).toHaveBeenCalledWith('agent:claude-daemon');
    });

    it('does not call selectAgent on key "4" when firstAgentId is null', () => {
      renderHook(() =>
        useKeyboardShortcuts({ firstGroupName: null, firstAgentId: null })
      );
      fireKeyDown('4');
      expect(selectAgent).not.toHaveBeenCalled();
    });

    it('navigates to Settings on key "5"', () => {
      renderHook(() => useKeyboardShortcuts());
      fireKeyDown('5');
      expect(selectSettings).toHaveBeenCalledTimes(1);
    });

    it('does not navigate on unrelated keys', () => {
      renderHook(() => useKeyboardShortcuts());
      fireKeyDown('6');
      fireKeyDown('a');
      fireKeyDown('Enter');
      expect(selectLiveFeed).not.toHaveBeenCalled();
      expect(selectMyInbox).not.toHaveBeenCalled();
      expect(selectSettings).not.toHaveBeenCalled();
    });
  });

  describe('input guard - number keys ignored when typing', () => {
    it('does not navigate when key "1" target is an INPUT element', () => {
      renderHook(() => useKeyboardShortcuts());

      const input = document.createElement('input');
      addToBody(input);

      fireKeyDown('1', {}, input);
      expect(selectLiveFeed).not.toHaveBeenCalled();
    });

    it('does not navigate when key "2" target is a TEXTAREA element', () => {
      renderHook(() => useKeyboardShortcuts());

      const textarea = document.createElement('textarea');
      addToBody(textarea);

      fireKeyDown('2', {}, textarea);
      expect(selectMyInbox).not.toHaveBeenCalled();
    });

    it('does not navigate when key "5" target is a contentEditable element', () => {
      renderHook(() => useKeyboardShortcuts());

      const div = document.createElement('div');
      div.setAttribute('contenteditable', 'true');
      addToBody(div);

      fireKeyDown('5', {}, div);
      expect(selectSettings).not.toHaveBeenCalled();
    });
  });

  describe('Cmd+K / Ctrl+K shortcut', () => {
    it('focuses sidebar search input when Cmd+K is pressed and a sidebar search input exists', () => {
      renderHook(() => useKeyboardShortcuts());

      const aside = document.createElement('aside');
      const input = document.createElement('input');
      input.type = 'search';
      aside.appendChild(input);
      addToBody(aside);

      const focusSpy = vi.spyOn(input, 'focus');
      fireKeyDown('k', { metaKey: true });

      expect(focusSpy).toHaveBeenCalled();
    });

    it('focuses main content when Cmd+K is pressed and no sidebar search input exists', () => {
      renderHook(() => useKeyboardShortcuts());

      const main = document.createElement('main');
      main.id = 'main-content';
      addToBody(main);

      const focusSpy = vi.spyOn(main, 'focus');
      fireKeyDown('k', { metaKey: true });

      expect(focusSpy).toHaveBeenCalled();
    });

    it('focuses main content when Ctrl+K is pressed', () => {
      renderHook(() => useKeyboardShortcuts());

      const main = document.createElement('main');
      main.id = 'main-content';
      addToBody(main);

      const focusSpy = vi.spyOn(main, 'focus');
      fireKeyDown('k', { ctrlKey: true });

      expect(focusSpy).toHaveBeenCalled();
    });

    it('does not trigger navigation actions when Cmd+K is pressed', () => {
      renderHook(() => useKeyboardShortcuts());

      fireKeyDown('k', { metaKey: true });
      expect(selectLiveFeed).not.toHaveBeenCalled();
      expect(selectMyInbox).not.toHaveBeenCalled();
      expect(selectSettings).not.toHaveBeenCalled();
    });
  });

  describe('Escape shortcut', () => {
    it('focuses main content on Escape when not in an input', () => {
      renderHook(() => useKeyboardShortcuts());

      const main = document.createElement('main');
      main.id = 'main-content';
      addToBody(main);

      const focusSpy = vi.spyOn(main, 'focus');
      fireKeyDown('Escape');

      expect(focusSpy).toHaveBeenCalled();
    });

    it('does not focus main content on Escape when target is an input', () => {
      renderHook(() => useKeyboardShortcuts());

      const main = document.createElement('main');
      main.id = 'main-content';
      addToBody(main);

      const input = document.createElement('input');
      addToBody(input);

      const focusSpy = vi.spyOn(main, 'focus');
      fireKeyDown('Escape', {}, input);

      expect(focusSpy).not.toHaveBeenCalled();
    });
  });

  describe('options reactivity', () => {
    it('re-registers handler when firstGroupName changes', () => {
      const { rerender } = renderHook(
        ({ firstGroupName }: { firstGroupName: string | null }) =>
          useKeyboardShortcuts({ firstGroupName, firstAgentId: null }),
        { initialProps: { firstGroupName: 'everyone' } }
      );

      fireKeyDown('3');
      expect(selectGroup).toHaveBeenCalledWith('everyone');

      vi.clearAllMocks();

      rerender({ firstGroupName: 'backend' });
      fireKeyDown('3');
      expect(selectGroup).toHaveBeenCalledWith('backend');
    });

    it('re-registers handler when firstAgentId changes', () => {
      const { rerender } = renderHook(
        ({ firstAgentId }: { firstAgentId: string | null }) =>
          useKeyboardShortcuts({ firstGroupName: null, firstAgentId }),
        { initialProps: { firstAgentId: 'agent:alpha' } }
      );

      fireKeyDown('4');
      expect(selectAgent).toHaveBeenCalledWith('agent:alpha');

      vi.clearAllMocks();

      rerender({ firstAgentId: 'agent:beta' });
      fireKeyDown('4');
      expect(selectAgent).toHaveBeenCalledWith('agent:beta');
    });
  });
});
