import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { WhoHasView } from '../WhoHasView';
import * as hooks from '@thrum/shared-logic';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentContext: vi.fn(),
    useDebounce: (value: string) => value, // No delay in tests
  };
});

describe('WhoHasView', () => {
  let queryClient: QueryClient;

  const mockContexts = [
    {
      session_id: 'sess-1',
      agent_id: 'agent:claude',
      branch: 'feature/auth',
      worktree_path: '/workspace/thrum',
      unmerged_commits: [],
      uncommitted_files: ['src/auth.ts', 'src/login.tsx'],
      changed_files: ['src/auth.ts', 'src/login.tsx', 'src/utils.ts'],
      git_updated_at: new Date().toISOString(),
      current_task: 'Implementing auth',
      task_updated_at: new Date().toISOString(),
      intent: 'Building authentication system',
      intent_updated_at: new Date().toISOString(),
    },
    {
      session_id: 'sess-2',
      agent_id: 'agent:researcher',
      branch: 'feature/docs',
      worktree_path: '/workspace/thrum-docs',
      unmerged_commits: [],
      uncommitted_files: ['docs/api.md'],
      changed_files: ['docs/api.md', 'docs/readme.md'],
      git_updated_at: new Date().toISOString(),
      current_task: 'Writing docs',
      task_updated_at: new Date().toISOString(),
      intent: 'Documenting the API',
      intent_updated_at: new Date().toISOString(),
    },
  ];

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    vi.mocked(hooks.useAgentContext).mockReturnValue({
      data: mockContexts,
      isLoading: false,
      error: null,
    } as any);
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Basic Rendering', () => {
    it('should render heading and search input', () => {
      renderWithProvider(<WhoHasView />);

      expect(screen.getByRole('heading', { name: /who has/i })).toBeInTheDocument();
      expect(screen.getByPlaceholderText(/search by file path/i)).toBeInTheDocument();
    });

    it('should show empty state when no search is entered', () => {
      renderWithProvider(<WhoHasView />);

      expect(screen.getByText(/search for a file to see who is editing it/i)).toBeInTheDocument();
    });
  });

  describe('Search and Filtering', () => {
    it('should show results when searching for a file', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'auth');

      await waitFor(() => {
        expect(screen.getByText('claude')).toBeInTheDocument();
        expect(screen.getByText('feature/auth')).toBeInTheDocument();
      });
    });

    it('should filter to only matching agents', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'docs/api');

      await waitFor(() => {
        expect(screen.getByText('researcher')).toBeInTheDocument();
        expect(screen.queryByText('claude')).not.toBeInTheDocument();
      });
    });

    it('should show no results state when no agents match', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'nonexistent-file.xyz');

      await waitFor(() => {
        expect(screen.getByText(/no agents are currently editing this file/i)).toBeInTheDocument();
      });
    });

    it('should show agent intent in results', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'auth');

      await waitFor(() => {
        expect(screen.getByText('Building authentication system')).toBeInTheDocument();
      });
    });

    it('should show column headers when results exist', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'auth');

      await waitFor(() => {
        expect(screen.getByText('Agent')).toBeInTheDocument();
        expect(screen.getByText('Branch')).toBeInTheDocument();
        expect(screen.getByText('Intent')).toBeInTheDocument();
        expect(screen.getByText('Last Seen')).toBeInTheDocument();
      });
    });
  });

  describe('Loading State', () => {
    it('should show loading skeleton when data is loading', async () => {
      vi.mocked(hooks.useAgentContext).mockReturnValue({
        data: undefined,
        isLoading: true,
        error: null,
      } as any);

      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'auth');

      await waitFor(() => {
        const skeletons = document.querySelectorAll('.animate-pulse');
        expect(skeletons.length).toBeGreaterThan(0);
      });
    });
  });

  describe('Edge Cases', () => {
    it('should handle empty agent context list', async () => {
      vi.mocked(hooks.useAgentContext).mockReturnValue({
        data: [],
        isLoading: false,
        error: null,
      } as any);

      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'auth');

      await waitFor(() => {
        expect(screen.getByText(/no agents are currently editing this file/i)).toBeInTheDocument();
      });
    });

    it('should be case-insensitive in search', async () => {
      const user = userEvent.setup();
      renderWithProvider(<WhoHasView />);

      const searchInput = screen.getByPlaceholderText(/search by file path/i);
      await user.type(searchInput, 'AUTH');

      await waitFor(() => {
        expect(screen.getByText('claude')).toBeInTheDocument();
      });
    });
  });
});
