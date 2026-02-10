import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useTheme } from '../useTheme';
import { themeStore } from '../../stores/themeStore';

describe('useTheme', () => {
  let matchMediaMock: any;

  beforeEach(() => {
    // Mock matchMedia
    matchMediaMock = {
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    };

    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation(() => matchMediaMock),
    });

    // Mock document.documentElement
    Object.defineProperty(document, 'documentElement', {
      writable: true,
      value: {
        classList: {
          toggle: vi.fn(),
          add: vi.fn(),
          remove: vi.fn(),
        },
      },
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
    themeStore.setState({ theme: 'system' });
  });

  it('returns current theme from store', () => {
    const { result } = renderHook(() => useTheme());

    expect(result.current.theme).toBe('system');
  });

  it('provides setTheme function', () => {
    const { result } = renderHook(() => useTheme());

    act(() => {
      result.current.setTheme('dark');
    });

    expect(result.current.theme).toBe('dark');
  });

  it('applies dark class when theme is dark', () => {
    const { result } = renderHook(() => useTheme());

    act(() => {
      result.current.setTheme('dark');
    });

    expect(document.documentElement.classList.toggle).toHaveBeenCalledWith('dark', true);
  });

  it('removes dark class when theme is light', () => {
    const { result } = renderHook(() => useTheme());

    act(() => {
      result.current.setTheme('light');
    });

    expect(document.documentElement.classList.toggle).toHaveBeenCalledWith('dark', false);
  });

  it('listens to system preference when theme is system', () => {
    renderHook(() => useTheme());

    expect(window.matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)');
    expect(matchMediaMock.addEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('updates theme when system preference changes', () => {
    matchMediaMock.matches = true;

    renderHook(() => useTheme());

    expect(document.documentElement.classList.toggle).toHaveBeenCalledWith('dark', true);
  });

  it('cleans up system preference listener on unmount', () => {
    const { unmount } = renderHook(() => useTheme());

    unmount();

    expect(matchMediaMock.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('removes listener when switching from system to explicit theme', () => {
    const { result } = renderHook(() => useTheme());

    act(() => {
      result.current.setTheme('dark');
    });

    expect(matchMediaMock.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });

  it('re-adds listener when switching back to system theme', () => {
    const { result } = renderHook(() => useTheme());

    act(() => {
      result.current.setTheme('dark');
    });

    vi.clearAllMocks();

    act(() => {
      result.current.setTheme('system');
    });

    expect(matchMediaMock.addEventListener).toHaveBeenCalledWith('change', expect.any(Function));
  });
});
