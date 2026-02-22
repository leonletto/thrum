import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ThemeToggle } from '../components/ThemeToggle';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { AgentListSkeleton } from '../components/agents/AgentListSkeleton';
import { SkipLink } from '../components/SkipLink';

// Component that can throw errors for testing
function ThrowError({ shouldThrow }: { shouldThrow: boolean }) {
  if (shouldThrow) {
    throw new Error('Test error for error boundary');
  }
  return <div>No error</div>;
}

describe('UI Polish Integration Tests', () => {
  beforeEach(() => {
    // Mock window.matchMedia for theme tests
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });

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

    // Suppress console.error for error boundary tests
    vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  describe('Theme Toggle Integration', () => {
    it('persists theme selection to localStorage', async () => {
      const user = userEvent.setup();

      render(<ThemeToggle />);

      const trigger = screen.getByRole('button', { name: /toggle theme/i });
      await user.click(trigger);

      const darkOption = screen.getByText('Dark');
      await user.click(darkOption);

      // Verify localStorage was called
      await waitFor(() => {
        expect(localStorage.setItem).toHaveBeenCalledWith('thrum-theme', 'dark');
      });
    });

    it('applies theme class to document root', async () => {
      const user = userEvent.setup();

      // Mock document.documentElement
      const mockClassList = {
        toggle: vi.fn(),
        add: vi.fn(),
        remove: vi.fn(),
      };
      Object.defineProperty(document, 'documentElement', {
        writable: true,
        value: {
          classList: mockClassList,
        },
      });

      render(<ThemeToggle />);

      const trigger = screen.getByRole('button', { name: /toggle theme/i });
      await user.click(trigger);

      const darkOption = screen.getByText('Dark');
      await user.click(darkOption);

      // Verify dark class was toggled
      await waitFor(() => {
        expect(mockClassList.toggle).toHaveBeenCalled();
      });
    });
  });

  describe('Error Boundary Integration', () => {
    it('catches errors and displays error UI', () => {
      render(
        <ErrorBoundary>
          <ThrowError shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByText('Something went wrong')).toBeInTheDocument();
      expect(screen.getByText(/test error/i)).toBeInTheDocument();
    });

    it('shows retry button after error', () => {
      render(
        <ErrorBoundary>
          <ThrowError shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByText('Something went wrong')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
    });

    it('renders custom fallback when provided', () => {
      render(
        <ErrorBoundary fallback={<div>Custom error message</div>}>
          <ThrowError shouldThrow={true} />
        </ErrorBoundary>
      );

      expect(screen.getByText('Custom error message')).toBeInTheDocument();
    });
  });

  describe('Loading Skeletons Integration', () => {
    it('renders agent list skeleton during loading', () => {
      const { container } = render(<AgentListSkeleton />);

      const skeletons = container.querySelectorAll('.animate-pulse');
      expect(skeletons.length).toBeGreaterThan(0);
    });

    it('agent skeleton has proper accessibility markup', () => {
      const { container } = render(<AgentListSkeleton />);

      // Skeletons should be presentational (not interactive)
      const buttons = container.querySelectorAll('button');
      expect(buttons.length).toBe(0);
    });
  });

  describe('Keyboard Navigation Integration', () => {
    it('skip link is keyboard accessible', async () => {
      const user = userEvent.setup();

      render(<SkipLink />);

      const link = screen.getByText('Skip to main content');

      // Tab to focus the link
      await user.tab();

      // Link should be focused
      expect(document.activeElement).toBe(link);
    });

    it('theme toggle is keyboard accessible', async () => {
      const user = userEvent.setup();

      render(<ThemeToggle />);

      const trigger = screen.getByRole('button', { name: /toggle theme/i });

      // Tab to focus the button
      await user.tab();

      // Button should be focusable
      trigger.focus();
      expect(document.activeElement).toBe(trigger);

      // Enter should activate the button
      await user.keyboard('{Enter}');

      // Dropdown should open
      await waitFor(() => {
        expect(screen.getByText('Light')).toBeInTheDocument();
      });
    });
  });

  describe('Full Integration Flow', () => {
    it('complete user flow: load -> error -> retry -> theme change', async () => {
      const user = userEvent.setup();
      let hasError = true;

      const { rerender } = render(
        <div>
          {/* Show loading skeleton */}
          {hasError ? <AgentListSkeleton /> : null}

          {/* Error boundary wraps content */}
          <ErrorBoundary>
            {hasError ? (
              <ThrowError shouldThrow={true} />
            ) : (
              <div>Content loaded</div>
            )}
          </ErrorBoundary>

          {/* Theme toggle available */}
          <ThemeToggle />
        </div>
      );

      // 1. Loading state shows skeleton
      expect(document.querySelector('.animate-pulse')).toBeInTheDocument();

      // 2. Error occurs
      expect(screen.getByText('Something went wrong')).toBeInTheDocument();

      // 3. User retries
      const retryButton = screen.getByRole('button', { name: /try again/i });
      await user.click(retryButton);

      // 4. Content loads successfully
      hasError = false;
      rerender(
        <div>
          <ErrorBoundary>
            <div>Content loaded</div>
          </ErrorBoundary>
          <ThemeToggle />
        </div>
      );

      await waitFor(() => {
        expect(screen.getByText('Content loaded')).toBeInTheDocument();
      });

      // 5. User changes theme
      const themeButton = screen.getByRole('button', { name: /toggle theme/i });
      await user.click(themeButton);
      await user.click(screen.getByText('Dark'));

      // Verify theme was persisted
      await waitFor(() => {
        expect(localStorage.setItem).toHaveBeenCalledWith('thrum-theme', 'dark');
      });
    });
  });
});
