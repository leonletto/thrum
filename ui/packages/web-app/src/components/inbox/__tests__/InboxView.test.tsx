import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import userEvent from '@testing-library/user-event';
import { InboxView } from '../InboxView';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useThreadList: vi.fn(),
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

    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: { threads: [] },
      isLoading: false,
      error: null,
    } as any);
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  it('should render loading state with skeleton', () => {
    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as any);

    const { container } = renderWithProvider(<InboxView />);
    // Loading state shows ThreadListSkeleton which renders skeleton cards
    const skeletons = container.querySelectorAll('.h-4, .h-3');
    expect(skeletons.length).toBeGreaterThan(0);
  });

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

  it('should render thread list when threads are available', () => {
    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: {
        threads: [
          {
            thread_id: 'thread-1',
            title: 'Test Thread',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 5,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 0,
            preview: null,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as any);

    renderWithProvider(<InboxView />);
    expect(screen.getByText('Test Thread')).toBeInTheDocument();
    expect(screen.getByText(/5 messages/i)).toBeInTheDocument();
  });

  it('should display unread count badge when there are unread messages', () => {
    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: {
        threads: [
          {
            thread_id: 'thread-1',
            title: 'Test Thread 1',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 5,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 3,
            preview: null,
          },
          {
            thread_id: 'thread-2',
            title: 'Test Thread 2',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 2,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 2,
            preview: null,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as any);

    renderWithProvider(<InboxView />);
    // Total unread count should be 3 + 2 = 5
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('should filter to show only unread threads when unread filter is active', async () => {
    const user = userEvent.setup();
    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: {
        threads: [
          {
            thread_id: 'thread-1',
            title: 'Unread Thread',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 5,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 3,
            preview: null,
          },
          {
            thread_id: 'thread-2',
            title: 'Read Thread',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 2,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 0,
            preview: null,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as any);

    renderWithProvider(<InboxView />);

    // Initially both threads should be visible
    expect(screen.getByText('Unread Thread')).toBeInTheDocument();
    expect(screen.getByText('Read Thread')).toBeInTheDocument();

    // Click unread filter
    await user.click(screen.getByText('Unread'));

    // Only unread thread should be visible
    expect(screen.getByText('Unread Thread')).toBeInTheDocument();
    expect(screen.queryByText('Read Thread')).not.toBeInTheDocument();
  });

  it('should show all threads when switching back to all filter', async () => {
    const user = userEvent.setup();
    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: {
        threads: [
          {
            thread_id: 'thread-1',
            title: 'Unread Thread',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 5,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 3,
            preview: null,
          },
          {
            thread_id: 'thread-2',
            title: 'Read Thread',
            created_by: 'user:test',
            created_at: '2024-01-01T00:00:00Z',
            message_count: 2,
            last_activity: '2024-01-01T12:00:00Z',
            unread_count: 0,
            preview: null,
          },
        ],
      },
      isLoading: false,
      error: null,
    } as any);

    renderWithProvider(<InboxView />);

    // Click unread filter
    await user.click(screen.getByText('Unread'));
    expect(screen.queryByText('Read Thread')).not.toBeInTheDocument();

    // Click all filter
    await user.click(screen.getByText('All'));

    // Both threads should be visible again
    expect(screen.getByText('Unread Thread')).toBeInTheDocument();
    expect(screen.getByText('Read Thread')).toBeInTheDocument();
  });
});
