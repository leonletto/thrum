import { useEffect } from 'react';
import { useStore } from '@tanstack/react-store';
import { themeStore, setTheme } from '../stores/themeStore';

/**
 * Hook to manage application theme
 *
 * Manages theme switching between system, light, and dark modes.
 * Automatically applies the theme to the document root and listens
 * to system theme changes when in 'system' mode.
 *
 * The theme is persisted to localStorage and restored on mount.
 *
 * Example:
 * ```tsx
 * function ThemeToggle() {
 *   const { theme, setTheme } = useTheme();
 *
 *   return (
 *     <select value={theme} onChange={(e) => setTheme(e.target.value)}>
 *       <option value="system">System</option>
 *       <option value="light">Light</option>
 *       <option value="dark">Dark</option>
 *     </select>
 *   );
 * }
 * ```
 */
export function useTheme() {
  const theme = useStore(themeStore, (state) => state.theme);

  useEffect(() => {
    const root = document.documentElement;

    if (theme === 'system') {
      // Listen to system preference changes
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

      const updateTheme = () => {
        root.classList.toggle('dark', mediaQuery.matches);
      };

      updateTheme();
      mediaQuery.addEventListener('change', updateTheme);

      return () => mediaQuery.removeEventListener('change', updateTheme);
    } else {
      // Apply explicit theme
      root.classList.toggle('dark', theme === 'dark');
    }
  }, [theme]);

  return {
    theme,
    setTheme,
  };
}
