import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MessageList } from '../MessageList';
import type { Message } from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

const mockMarkAsReadMutate = vi.fn();

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    // Let groupByConversation and useDebounce run for real (they are pure)
    useMarkAsRead: () => ({
      mutate: mockMarkAsReadMutate,
      isPending: false,
    }),
    useAgentList: () => ({
      data: {
        agents: [
          {
            agent_id: 'user:alice',
            kind: 'agent' as const,
            role: 'user',
            module: 'core',
            display: 'Alice',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T12:00:00Z',
          },
          {
            agent_id: 'user:bob',
            kind: 'agent' as const,
            role: 'user',
            module: 'core',
            display: 'Bob',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T12:00:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
    useCurrentUser: () => ({
      user_id: 'user:alice',
      username: 'alice',
      display_name: 'Alice',
    }),
    useEditMessage: () => ({
      mutateAsync: vi.fn(),
      isPending: false,
    }),
    useDeleteMessage: () => ({
      mutateAsync: vi.fn(),
      isPending: false,
    }),
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

function renderWithProvider(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
  );
}

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('MessageList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // 1. Empty state
  it('renders empty state when messages array is empty', () => {
    renderWithProvider(
      <MessageList messages={[]} isLoading={false} />
    );

    expect(screen.getByText('No messages')).toBeInTheDocument();
  });

  // 2. Loading skeleton
  it('shows loading skeleton when isLoading is true', () => {
    const { container } = renderWithProvider(
      <MessageList messages={[]} isLoading={true} />
    );

    // MessageListSkeleton renders multiple Skeleton elements with animate-pulse
    const skeletons = container.querySelectorAll('.animate-pulse');
    expect(skeletons.length).toBeGreaterThan(0);
    expect(screen.queryByText('No messages')).not.toBeInTheDocument();
  });

  // 3. Groups messages by conversation — renders root and replies
  it('groups messages by conversation and renders root + replies', async () => {
    const user = userEvent.setup();
    const root = makeMessage({
      message_id: 'root-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Root message' },
      agent_id: 'user:alice',
    });
    const reply1 = makeMessage({
      message_id: 'reply-1',
      created_at: '2024-01-01T10:01:00Z',
      body: { format: 'text', content: 'First reply' },
      agent_id: 'user:bob',
      reply_to: 'root-1',
    });

    renderWithProvider(
      <MessageList messages={[root, reply1]} isLoading={false} />
    );

    // Root message is always rendered
    expect(screen.getByText('Root message')).toBeInTheDocument();

    // Reply is hidden until expanded
    expect(screen.queryByText('First reply')).not.toBeInTheDocument();

    // Expand to reveal reply
    const toggleButton = screen.getByRole('button', { name: /show 1 reply/i });
    await user.click(toggleButton);

    expect(screen.getByText('First reply')).toBeInTheDocument();
  });

  // 4. Collapse/expand toggle
  it('collapse/expand toggle works', async () => {
    const user = userEvent.setup();
    const root = makeMessage({
      message_id: 'root-2',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Root 2' },
    });
    const reply = makeMessage({
      message_id: 'reply-2',
      created_at: '2024-01-01T10:02:00Z',
      body: { format: 'text', content: 'Reply 2' },
      reply_to: 'root-2',
    });

    renderWithProvider(
      <MessageList messages={[root, reply]} isLoading={false} />
    );

    // Initially collapsed — reply not visible
    expect(screen.queryByText('Reply 2')).not.toBeInTheDocument();

    // Expand
    await user.click(screen.getByRole('button', { name: /show 1 reply/i }));
    expect(screen.getByText('Reply 2')).toBeInTheDocument();

    // Collapse again
    await user.click(screen.getByRole('button', { name: /collapse replies/i }));
    expect(screen.queryByText('Reply 2')).not.toBeInTheDocument();
  });

  // 5. Reply count badge
  it('shows reply count badge with correct count', () => {
    const root = makeMessage({
      message_id: 'root-3',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Root 3' },
    });
    const replies = [1, 2, 3].map(i =>
      makeMessage({
        message_id: `reply-3-${i}`,
        created_at: `2024-01-01T10:0${i}:00Z`,
        body: { format: 'text', content: `Reply ${i}` },
        reply_to: 'root-3',
      })
    );

    renderWithProvider(
      <MessageList messages={[root, ...replies]} isLoading={false} />
    );

    // Badge should show "3 replies"
    expect(screen.getByText('3 replies')).toBeInTheDocument();
  });

  // 6. Calls onReply when reply button clicked
  it('calls onReply with messageId and senderName when reply button is clicked', async () => {
    const user = userEvent.setup();
    const onReply = vi.fn();
    const root = makeMessage({
      message_id: 'root-4',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Root 4' },
      agent_id: 'user:alice',
    });

    renderWithProvider(
      <MessageList messages={[root]} isLoading={false} onReply={onReply} />
    );

    const replyButton = screen.getByRole('button', { name: /reply to user:alice/i });
    await user.click(replyButton);

    expect(onReply).toHaveBeenCalledWith('root-4', 'user:alice');
  });

  // 7. Marks unread messages as read
  it('marks unread messages as read after debounce', async () => {
    const unreadMessage = makeMessage({
      message_id: 'unread-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Unread message' },
      is_read: false,
    });

    renderWithProvider(
      <MessageList messages={[unreadMessage]} isLoading={false} />
    );

    await waitFor(() => {
      expect(mockMarkAsReadMutate).toHaveBeenCalledWith(['unread-1']);
    }, { timeout: 1500 });
  });

  // Extra: does not call markAsRead when all messages are already read
  it('does not call markAsRead when all messages are already read', async () => {
    const readMessage = makeMessage({
      message_id: 'read-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Read message' },
      is_read: true,
    });

    renderWithProvider(
      <MessageList messages={[readMessage]} isLoading={false} />
    );

    // Wait a bit to confirm no call
    await new Promise(r => setTimeout(r, 700));
    expect(mockMarkAsReadMutate).not.toHaveBeenCalled();
  });

  // Extra: renders multiple independent conversations
  it('renders multiple independent conversations', () => {
    const msg1 = makeMessage({
      message_id: 'conv-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Conversation one' },
    });
    const msg2 = makeMessage({
      message_id: 'conv-2',
      created_at: '2024-01-01T11:00:00Z',
      body: { format: 'text', content: 'Conversation two' },
    });

    renderWithProvider(
      <MessageList messages={[msg1, msg2]} isLoading={false} />
    );

    expect(screen.getByText('Conversation one')).toBeInTheDocument();
    expect(screen.getByText('Conversation two')).toBeInTheDocument();
  });

  // Extra: no reply buttons when onReply prop is not provided
  it('does not render reply buttons when onReply is not provided', () => {
    const root = makeMessage({
      message_id: 'root-nr',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Root no-reply' },
      agent_id: 'user:alice',
    });

    renderWithProvider(
      <MessageList messages={[root]} isLoading={false} />
    );

    expect(screen.queryByRole('button', { name: /reply to/i })).not.toBeInTheDocument();
  });

  // Pagination: Load More button
  it('shows Load More button when hasMore is true', () => {
    const root = makeMessage({
      message_id: 'pag-1',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Paginated message' },
    });
    const onLoadMore = vi.fn();

    renderWithProvider(
      <MessageList
        messages={[root]}
        isLoading={false}
        hasMore={true}
        onLoadMore={onLoadMore}
        totalCount={100}
      />
    );

    expect(
      screen.getByRole('button', { name: /load more messages/i })
    ).toBeInTheDocument();
  });

  it('does not show Load More button when hasMore is false', () => {
    const root = makeMessage({
      message_id: 'pag-2',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Paginated message 2' },
    });

    renderWithProvider(
      <MessageList
        messages={[root]}
        isLoading={false}
        hasMore={false}
        totalCount={1}
      />
    );

    expect(
      screen.queryByRole('button', { name: /load more messages/i })
    ).not.toBeInTheDocument();
  });

  it('does not show Load More button when hasMore is not provided', () => {
    const root = makeMessage({
      message_id: 'pag-3',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Paginated message 3' },
    });

    renderWithProvider(
      <MessageList messages={[root]} isLoading={false} />
    );

    expect(
      screen.queryByRole('button', { name: /load more messages/i })
    ).not.toBeInTheDocument();
  });

  it('calls onLoadMore when Load More button is clicked', async () => {
    const user = userEvent.setup();
    const onLoadMore = vi.fn();
    const root = makeMessage({
      message_id: 'pag-4',
      created_at: '2024-01-01T10:00:00Z',
      body: { format: 'text', content: 'Paginated message 4' },
    });

    renderWithProvider(
      <MessageList
        messages={[root]}
        isLoading={false}
        hasMore={true}
        onLoadMore={onLoadMore}
      />
    );

    await user.click(screen.getByRole('button', { name: /load more messages/i }));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('shows message count when totalCount is provided and hasMore is true', () => {
    const messages = [1, 2].map(i =>
      makeMessage({
        message_id: `count-${i}`,
        created_at: `2024-01-01T10:0${i}:00Z`,
        body: { format: 'text', content: `Count message ${i}` },
      })
    );

    renderWithProvider(
      <MessageList
        messages={messages}
        isLoading={false}
        hasMore={true}
        onLoadMore={vi.fn()}
        totalCount={50}
      />
    );

    expect(screen.getByText(/showing 2 of 50 messages/i)).toBeInTheDocument();
  });
});
