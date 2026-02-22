import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InboxView } from '../InboxView';
import * as hooks from '@thrum/shared-logic';

// Mock shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
  };
});

describe('Messaging Integration Tests', () => {
  let queryClient: QueryClient;

  const mockCurrentUser = {
    user_id: 'user:leon',
    username: 'leon',
    display_name: 'Leon',
    created_at: '2024-01-01T00:00:00Z',
  };

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    vi.mocked(hooks.useCurrentUser).mockReturnValue(mockCurrentUser);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('InboxView: Own Inbox', () => {
    it('should render inbox with user identity', () => {
      renderWithProvider(<InboxView />);
      expect(screen.getByText('leon')).toBeInTheDocument();
    });

    it('should show empty state placeholder', () => {
      renderWithProvider(<InboxView />);
      expect(screen.getByText('NO THREADS')).toBeInTheDocument();
      expect(screen.getByText('Start a conversation')).toBeInTheDocument();
    });

    it('should render compose button', () => {
      renderWithProvider(<InboxView />);
      expect(screen.getByText('+ COMPOSE')).toBeInTheDocument();
    });

    it('should open compose modal when compose button is clicked', async () => {
      const user = userEvent.setup({ delay: null });
      renderWithProvider(<InboxView />);

      const composeButton = screen.getByText('+ COMPOSE');
      await user.click(composeButton);

      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });
    });
  });

  describe('InboxView: Agent Impersonation', () => {
    it('should show impersonation warning when viewing agent inbox', () => {
      renderWithProvider(<InboxView identityId="agent:claude-daemon" />);
      expect(screen.getByText(/Sending as agent:claude-daemon/)).toBeInTheDocument();
    });

    it('should show agent identity as heading', () => {
      renderWithProvider(<InboxView identityId="agent:claude-daemon" />);
      expect(screen.getByText('agent:claude-daemon')).toBeInTheDocument();
    });
  });
});
