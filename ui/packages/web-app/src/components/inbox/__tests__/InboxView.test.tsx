import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InboxView } from '../InboxView';
import * as hooks from '@thrum/shared-logic';
import type { Message } from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    loadStoredUser: vi.fn(),
    useMessageListPaged: vi.fn(),
    useMarkAsRead: vi.fn(),
    useSendMessage: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
  };
});

// ─── Helpers ──────────────────────────────────────────────────────────────────

function makeMessage(
  overrides: Partial<Message> & { message_id: string; created_at: string }
): Message {
  return {
    body: { format: 'text', content: `Message ${overrides.message_id}` },
    ...overrides,
  };
}

/** Default paged return value — empty inbox, not loading */
function makePagedReturn(messages: Message[] = [], isLoading = false) {
  return {
    messages,
    total: messages.length,
    isLoading,
    hasMore: false,
    loadMore: vi.fn(),
    isLoadingMore: false,
  } as any;
}

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('InboxView', () => {
  let queryClient: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();

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

    vi.mocked(hooks.loadStoredUser).mockReturnValue(null);

    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn());

    vi.mocked(hooks.useMarkAsRead).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as any);

    vi.mocked(hooks.useSendMessage).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as any);

    vi.mocked(hooks.useAgentList).mockReturnValue({
      data: { agents: [] },
      isLoading: false,
      error: null,
    } as any);

    vi.mocked(hooks.useGroupList).mockReturnValue({
      data: { groups: [] },
      isLoading: false,
      error: null,
    } as any);
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  // ─── 1. Loading state ───────────────────────────────────────────────────────

  it('renders loading skeleton when isLoading is true', () => {
    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn([], true));

    const { container } = renderWithProvider(<InboxView />);

    // MessageListSkeleton renders multiple Skeleton elements with animate-pulse
    const skeletons = container.querySelectorAll('.animate-pulse');
    expect(skeletons.length).toBeGreaterThan(0);
  });

  // ─── 2. Shows messages from message.list for current user ──────────────────

  it('shows messages from message.list for current user', () => {
    const msg1 = makeMessage({
      message_id: 'msg-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Hello from inbox' },
      agent_id: 'user:test',
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn([msg1]));

    renderWithProvider(<InboxView />);

    expect(screen.getByText('Hello from inbox')).toBeInTheDocument();
  });

  it('calls useMessageListPaged with for_agent set to current user identity', () => {
    renderWithProvider(<InboxView />);

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.objectContaining({
        for_agent: 'test-user',
        page_size: 50,
        sort_order: 'desc',
      })
    );
  });

  // ─── 3. Impersonation banner ────────────────────────────────────────────────

  it('does not show impersonation banner when viewing own inbox', () => {
    renderWithProvider(<InboxView />);

    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows impersonation banner when identityId differs from current user', () => {
    renderWithProvider(<InboxView identityId="agent:impl_1" />);

    const banner = screen.getByRole('alert');
    expect(banner).toBeInTheDocument();
    expect(banner).toHaveTextContent('Viewing as:');
    expect(banner).toHaveTextContent('agent:impl_1');
  });

  it('calls useMessageListPaged with for_agent set to identityId when impersonating', () => {
    renderWithProvider(<InboxView identityId="agent:impl_1" />);

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.objectContaining({
        for_agent: 'agent:impl_1',
        page_size: 50,
        sort_order: 'desc',
      })
    );
  });

  // ─── 4. Unread toggle filters messages ─────────────────────────────────────

  it('renders All and Unread filter toggle buttons', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Unread/i })).toBeInTheDocument();
  });

  it('does not include unread_for_agent filter when "All" is selected (default)', () => {
    renderWithProvider(<InboxView />);

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.not.objectContaining({ unread_for_agent: expect.anything() })
    );
  });

  it('adds unread_for_agent filter when Unread toggle is clicked', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView />);

    const unreadButton = screen.getByRole('button', { name: /Unread/i });
    await user.click(unreadButton);

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.objectContaining({
        for_agent: 'test-user',
        unread_for_agent: 'test-user',
      })
    );
  });

  it('removes unread_for_agent filter when switching back to All', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView />);

    // Switch to Unread
    await user.click(screen.getByRole('button', { name: /Unread/i }));
    // Switch back to All
    await user.click(screen.getByRole('button', { name: 'All' }));

    // Last call should not include unread_for_agent
    const lastCall = vi.mocked(hooks.useMessageListPaged).mock.calls.at(-1)?.[0];
    expect(lastCall).not.toHaveProperty('unread_for_agent');
  });

  // ─── Mentions filter ────────────────────────────────────────────────────────

  it('renders Mentions filter button', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByRole('button', { name: 'Mentions' })).toBeInTheDocument();
  });

  it('does not include mention filter when "All" is selected (default)', () => {
    renderWithProvider(<InboxView />);

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.not.objectContaining({ mention: expect.anything() })
    );
  });

  it('adds mention filter when Mentions tab is clicked', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView />);

    await user.click(screen.getByRole('button', { name: 'Mentions' }));

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.objectContaining({
        for_agent: 'test-user',
        mention: 'test-user',
      })
    );
  });

  it('does not include unread_for_agent when Mentions tab is active', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView />);

    await user.click(screen.getByRole('button', { name: 'Mentions' }));

    const lastCall = vi.mocked(hooks.useMessageListPaged).mock.calls.at(-1)?.[0];
    expect(lastCall).not.toHaveProperty('unread_for_agent');
    expect(lastCall).toHaveProperty('mention', 'test-user');
  });

  it('removes mention filter when switching from Mentions back to All', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView />);

    // Switch to Mentions
    await user.click(screen.getByRole('button', { name: 'Mentions' }));
    // Switch back to All
    await user.click(screen.getByRole('button', { name: 'All' }));

    // Last call should not include mention
    const lastCall = vi.mocked(hooks.useMessageListPaged).mock.calls.at(-1)?.[0];
    expect(lastCall).not.toHaveProperty('mention');
  });

  it('uses identityId as mention filter when viewing another agent inbox in Mentions tab', async () => {
    const user = userEvent.setup();
    renderWithProvider(<InboxView identityId="agent:other" />);

    await user.click(screen.getByRole('button', { name: 'Mentions' }));

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith(
      expect.objectContaining({
        for_agent: 'agent:other',
        mention: 'agent:other',
      })
    );
  });

  // ─── 5. ComposeBar renders at bottom ───────────────────────────────────────

  it('renders ComposeBar at the bottom', () => {
    renderWithProvider(<InboxView />);

    // ComposeBar renders a data-testid="compose-bar"
    expect(screen.getByTestId('compose-bar')).toBeInTheDocument();
  });

  it('renders ComposeBar with Send button', () => {
    renderWithProvider(<InboxView />);

    expect(screen.getByRole('button', { name: /send/i })).toBeInTheDocument();
  });

  // ─── 6. Reply flow ──────────────────────────────────────────────────────────

  it('sets replyTo state in ComposeBar when a message reply button is clicked', async () => {
    const user = userEvent.setup();
    const msg = makeMessage({
      message_id: 'msg-reply-test',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Reply target message' },
      agent_id: 'agent:sender',
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn([msg]));

    renderWithProvider(<InboxView />);

    // Click Reply on the message
    const replyButton = screen.getByRole('button', {
      name: /reply to agent:sender/i,
    });
    await user.click(replyButton);

    // ComposeBar should now show the reply chip
    await waitFor(() => {
      expect(
        screen.getByText(/Replying to: @agent:sender/i)
      ).toBeInTheDocument();
    });
  });

  it('clears replyTo when clear reply button is clicked in ComposeBar', async () => {
    const user = userEvent.setup();
    const msg = makeMessage({
      message_id: 'msg-clear-test',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Clear reply test message' },
      agent_id: 'agent:sender',
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn([msg]));

    renderWithProvider(<InboxView />);

    // Click Reply to enter reply mode
    await user.click(
      screen.getByRole('button', { name: /reply to agent:sender/i })
    );

    await waitFor(() => {
      expect(screen.getByText(/Replying to: @agent:sender/i)).toBeInTheDocument();
    });

    // Clear the reply
    await user.click(screen.getByRole('button', { name: /clear reply/i }));

    await waitFor(() => {
      expect(
        screen.queryByText(/Replying to: @agent:sender/i)
      ).not.toBeInTheDocument();
    });
  });

  // ─── 7. Header ─────────────────────────────────────────────────────────────

  it('renders inbox header with current user identity when no identityId given', () => {
    renderWithProvider(<InboxView />);
    expect(screen.getByText('test-user')).toBeInTheDocument();
  });

  it('renders inbox header with agent identity when identityId is provided', () => {
    renderWithProvider(<InboxView identityId="agent:claude" />);
    // Identity appears in the h1 heading (and may also appear in impersonation banner)
    expect(screen.getByRole('heading', { name: 'agent:claude' })).toBeInTheDocument();
  });

  it('shows impersonation text in header when viewing agent inbox', () => {
    renderWithProvider(<InboxView identityId="agent:claude" />);
    expect(screen.getByText(/Sending as agent:claude/)).toBeInTheDocument();
  });

  // ─── 8. Identity fallback (thrum-wjt0) ────────────────────────────────────

  it('falls back to stored user username when React Query cache is empty', () => {
    vi.mocked(hooks.useCurrentUser).mockReturnValue(undefined);
    vi.mocked(hooks.loadStoredUser).mockReturnValue({
      user_id: 'user:thrum',
      username: 'thrum',
      token: 'tok',
    });

    renderWithProvider(<InboxView />);

    expect(screen.getByRole('heading', { name: 'thrum' })).toBeInTheDocument();
  });

  it('falls back to "Thrum User" when both React Query cache and localStorage are empty', () => {
    vi.mocked(hooks.useCurrentUser).mockReturnValue(undefined);
    vi.mocked(hooks.loadStoredUser).mockReturnValue(null);

    renderWithProvider(<InboxView />);

    expect(screen.getByRole('heading', { name: 'Thrum User' })).toBeInTheDocument();
  });

  it('does not show "Unknown" as inbox heading when identity cannot be resolved', () => {
    vi.mocked(hooks.useCurrentUser).mockReturnValue(undefined);
    vi.mocked(hooks.loadStoredUser).mockReturnValue(null);

    renderWithProvider(<InboxView />);

    expect(screen.queryByRole('heading', { name: 'Unknown' })).not.toBeInTheDocument();
  });

  // ─── 9. Pagination ─────────────────────────────────────────────────────────

  it('does not render Load More button when hasMore is false', () => {
    vi.mocked(hooks.useMessageListPaged).mockReturnValue(makePagedReturn());

    renderWithProvider(<InboxView />);

    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();
  });

  it('renders Load More button when hasMore is true and there are messages', () => {
    const msg = makeMessage({
      message_id: 'msg-p1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Page 1 message' },
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: [msg],
      total: 100,
      isLoading: false,
      hasMore: true,
      loadMore: vi.fn(),
      isLoadingMore: false,
    } as any);

    renderWithProvider(<InboxView />);

    expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument();
  });

  it('calls loadMore when Load More button is clicked', async () => {
    const user = userEvent.setup();
    const loadMore = vi.fn();
    const msg = makeMessage({
      message_id: 'msg-p1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Page 1 message' },
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: [msg],
      total: 100,
      isLoading: false,
      hasMore: true,
      loadMore,
      isLoadingMore: false,
    } as any);

    renderWithProvider(<InboxView />);

    await user.click(screen.getByRole('button', { name: /load more/i }));

    expect(loadMore).toHaveBeenCalledTimes(1);
  });

  it('shows "Loading..." text on Load More button when isLoadingMore is true', () => {
    const msg = makeMessage({
      message_id: 'msg-p1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Page 1 message' },
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: [msg],
      total: 100,
      isLoading: false,
      hasMore: true,
      loadMore: vi.fn(),
      isLoadingMore: true,
    } as any);

    renderWithProvider(<InboxView />);

    expect(screen.getByRole('button', { name: /load more/i })).toHaveTextContent('Loading...');
  });

  it('shows message count when total is provided and hasMore is true', () => {
    const msg = makeMessage({
      message_id: 'msg-p1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Page 1 message' },
    });

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: [msg],
      total: 42,
      isLoading: false,
      hasMore: true,
      loadMore: vi.fn(),
      isLoadingMore: false,
    } as any);

    renderWithProvider(<InboxView />);

    expect(screen.getByText(/Showing 1 of 42 messages/)).toBeInTheDocument();
  });
});
