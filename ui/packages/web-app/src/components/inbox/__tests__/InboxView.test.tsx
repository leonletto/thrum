import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InboxView } from '../InboxView';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
  };
});

describe('InboxView', () => {
  let queryClient: QueryClient;

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    // Default mocks
    vi.mocked(hooks.useCurrentUser).mockReturnValue({
      user_id: 'user:test',
      username: 'test-user',
      display_name: 'Test User',
      created_at: '2024-01-01T00:00:00Z',
    });
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  it('should render filter buttons', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByText('All')).toBeInTheDocument();
    expect(screen.getByText('Unread')).toBeInTheDocument();
  });

  it('should render empty thread list', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByText('NO THREADS')).toBeInTheDocument();
    expect(screen.getByText('Start a conversation')).toBeInTheDocument();
  });

  it('should render inbox header with user identity', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByText('test-user')).toBeInTheDocument();
    expect(screen.getByText('+ COMPOSE')).toBeInTheDocument();
  });

  it('should render inbox header with agent identity', () => {
    renderWithProvider(<InboxView identityId="agent:claude" />);
    expect(screen.getByText('agent:claude')).toBeInTheDocument();
  });

  it('should show impersonation warning when viewing agent inbox', () => {
    renderWithProvider(<InboxView identityId="agent:claude" />);
    expect(screen.getByText(/Sending as agent:claude/)).toBeInTheDocument();
  });
});
