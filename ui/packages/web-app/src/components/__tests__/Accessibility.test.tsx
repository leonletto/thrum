import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '../../test/test-utils';
import { SkipLink } from '../SkipLink';
import { AppShell } from '../AppShell';

// Mock useAgentList hook
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentList: () => ({
      data: {
        agents: [
          {
            agent_id: 'agent:claude-daemon',
            kind: 'agent' as const,
            role: 'daemon',
            module: 'core',
            display: 'Claude Daemon',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T12:00:00Z',
          },
          {
            agent_id: 'agent:claude-cli',
            kind: 'agent' as const,
            role: 'cli',
            module: 'core',
            display: 'Claude CLI',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T11:50:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
  };
});

describe('Accessibility Features', () => {
  beforeEach(() => {
    // Mock window.matchMedia for useTheme
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
  });
  describe('SkipLink', () => {
    it('renders skip to main content link', () => {
      render(<SkipLink />);

      const link = screen.getByText('Skip to main content');
      expect(link).toBeInTheDocument();
      expect(link).toHaveAttribute('href', '#main-content');
    });

    it('has sr-only class for screen readers', () => {
      const { container } = render(<SkipLink />);

      const link = container.querySelector('.sr-only');
      expect(link).toBeInTheDocument();
    });

    it('becomes visible on focus', () => {
      const { container } = render(<SkipLink />);

      const link = container.querySelector('a');
      expect(link).toHaveClass('sr-only');
      expect(link).toHaveClass('focus:not-sr-only');
    });
  });

  describe('AppShell', () => {
    it('renders main content area with proper landmark', () => {
      const { container } = render(
        <AppShell>
          <div>Test content</div>
        </AppShell>
      );

      const main = container.querySelector('main');
      expect(main).toBeInTheDocument();
      expect(main).toHaveAttribute('id', 'main-content');
      expect(main).toHaveAttribute('role', 'main');
    });

    it('includes skip link', () => {
      render(
        <AppShell>
          <div>Test content</div>
        </AppShell>
      );

      expect(screen.getByText('Skip to main content')).toBeInTheDocument();
    });
  });

  describe('Keyboard Navigation', () => {
    it('skip link is keyboard accessible', () => {
      render(<SkipLink />);

      const link = screen.getByText('Skip to main content');

      // Skip links should be focusable
      link.focus();
      expect(document.activeElement).toBe(link);
    });
  });
});
