import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { themeStore, setTheme } from '../themeStore';

describe('themeStore', () => {
  beforeEach(() => {
    // Mock localStorage
    const localStorageMock = {
      getItem: vi.fn(),
      setItem: vi.fn(),
      clear: vi.fn(),
      removeItem: vi.fn(),
      length: 0,
      key: vi.fn(),
    };
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
      writable: true,
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('initializes with system theme by default', () => {
    expect(themeStore.state.theme).toBe('system');
  });

  it('loads theme from localStorage on initialization', async () => {
    const localStorageMock = {
      getItem: vi.fn().mockReturnValue('dark'),
      setItem: vi.fn(),
      clear: vi.fn(),
      removeItem: vi.fn(),
      length: 0,
      key: vi.fn(),
    };
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
      writable: true,
    });

    // Import fresh to test initialization
    vi.resetModules();
    const { themeStore: freshStore } = await import('../themeStore');

    // Assert that theme was loaded from localStorage
    expect(localStorageMock.getItem).toHaveBeenCalledWith('thrum-theme');
    expect(freshStore.state.theme).toBe('dark');
  });

  it('defaults to system theme when localStorage is empty', async () => {
    const localStorageMock = {
      getItem: vi.fn().mockReturnValue(null),
      setItem: vi.fn(),
      clear: vi.fn(),
      removeItem: vi.fn(),
      length: 0,
      key: vi.fn(),
    };
    Object.defineProperty(window, 'localStorage', {
      value: localStorageMock,
      writable: true,
    });

    // Import fresh to test initialization
    vi.resetModules();
    const { themeStore: freshStore } = await import('../themeStore');

    // Assert that theme defaults to 'system' when localStorage is empty
    expect(localStorageMock.getItem).toHaveBeenCalledWith('thrum-theme');
    expect(freshStore.state.theme).toBe('system');
  });

  it('updates theme state', () => {
    setTheme('dark');
    expect(themeStore.state.theme).toBe('dark');

    setTheme('light');
    expect(themeStore.state.theme).toBe('light');

    setTheme('system');
    expect(themeStore.state.theme).toBe('system');
  });

  it('persists theme to localStorage on change', () => {
    setTheme('dark');
    expect(localStorage.setItem).toHaveBeenCalledWith('thrum-theme', 'dark');

    setTheme('light');
    expect(localStorage.setItem).toHaveBeenCalledWith('thrum-theme', 'light');
  });
});
