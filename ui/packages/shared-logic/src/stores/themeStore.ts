import { Store } from '@tanstack/store';

export type Theme = 'system' | 'light' | 'dark';

export interface ThemeState {
  theme: Theme;
}

// Load theme from localStorage, default to 'system'
const getInitialTheme = (): Theme => {
  if (typeof window === 'undefined') return 'system';

  const stored = localStorage.getItem('thrum-theme');
  if (stored === 'light' || stored === 'dark' || stored === 'system') {
    return stored;
  }

  return 'system';
};

const initialState: ThemeState = {
  theme: getInitialTheme(),
};

export const themeStore = new Store<ThemeState>(initialState);

// Subscribe to theme changes and persist to localStorage
themeStore.subscribe(() => {
  const state = themeStore.state;
  if (typeof window !== 'undefined') {
    localStorage.setItem('thrum-theme', state.theme);
  }
});

// Actions
export const setTheme = (theme: Theme) => {
  themeStore.setState({ theme });
};
