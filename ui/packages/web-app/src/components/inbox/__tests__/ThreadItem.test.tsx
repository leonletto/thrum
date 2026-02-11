import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThreadItem } from '../ThreadItem';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useThread: vi.fn(),
    useMarkAsRead: vi.fn(),
  };
});

describe('ThreadItem', () => {
  let queryClient: QueryClient;
  const mockOnToggle = vi.fn();
  const mockMarkAsRead = vi.fn();

  const mockThread = {
    thread_id: 'thread-1',
    title: 'Test Thread',
    created_by: 'user:test',
    created_at: '2024-01-01T00:00:00Z',
    message_count: 5,
    last_activity: '2024-01-01T12:00:00Z',
    unread_count: 2,
    last_sender: 'agent:claude',
    preview: 'This is a preview of the last message...',
  };

  beforeEach(() => {
    vi.useFakeTimers();
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    // Default mock for useMarkAsRead
    vi.mocked(hooks.useMarkAsRead).mockReturnValue({
      mutate: mockMarkAsRead,
      isPending: false,
    } as any);

    // Default mock for useThread (not expanded)
    vi.mocked(hooks.useThread).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    } as any);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Collapsed State', () => {
    it('should render thread title and metadata when collapsed', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('Test Thread')).toBeInTheDocument();
      expect(screen.getByText('5 messages')).toBeInTheDocument();
    });

    it('should show unread badge when unread_count > 0', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('2 new')).toBeInTheDocument();
    });

    it('should not show unread badge when unread_count is 0', () => {
      renderWithProvider(
        <ThreadItem
          thread={{ ...mockThread, unread_count: 0 }}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.queryByText(/new/)).not.toBeInTheDocument();
    });

    it('should show preview when available', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText(/This is a preview/)).toBeInTheDocument();
    });

    it('should call onToggle when clicked', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Click the thread header (CardHeader with role="button")
      const header = screen.getByRole('button', { name: /Thread: Test Thread/ });
      fireEvent.click(header);

      expect(mockOnToggle).toHaveBeenCalledTimes(1);
    });
  });

  describe('Expanded State', () => {
    const mockMessages = [
      {
        message_id: 'msg-1',
        thread_id: 'thread-1',
        agent_id: 'user:test',
        body: { format: 'markdown', content: 'First message' },
        created_at: '2024-01-01T10:00:00Z',
        is_read: true,
      },
      {
        message_id: 'msg-2',
        thread_id: 'thread-1',
        agent_id: 'agent:claude',
        body: { format: 'markdown', content: 'Second message' },
        created_at: '2024-01-01T11:00:00Z',
        is_read: false,
      },
      {
        message_id: 'msg-3',
        thread_id: 'thread-1',
        agent_id: 'user:test',
        body: { format: 'markdown', content: 'Third message' },
        created_at: '2024-01-01T12:00:00Z',
        is_read: false,
      },
    ];

    beforeEach(() => {
      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test Thread',
          messages: mockMessages,
          total_messages: 3,
        },
        isLoading: false,
        error: null,
      } as any);
    });

    it('should fetch thread messages when expanded', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(hooks.useThread).toHaveBeenCalledWith(
        'thread-1',
        expect.objectContaining({ enabled: true })
      );
    });

    it('should not fetch thread messages when collapsed', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={false}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(hooks.useThread).toHaveBeenCalledWith(
        'thread-1',
        expect.objectContaining({ enabled: false })
      );
    });

    it('should render all messages when expanded', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('First message')).toBeInTheDocument();
      expect(screen.getByText('Second message')).toBeInTheDocument();
      expect(screen.getByText('Third message')).toBeInTheDocument();
    });

    it('should show inline reply when expanded', () => {
      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByPlaceholderText(/Write a reply/i)).toBeInTheDocument();
    });

    it('should show loading state while fetching messages', () => {
      vi.mocked(hooks.useThread).mockReturnValue({
        data: undefined,
        isLoading: true,
        error: null,
      } as any);

      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('Loading messages...')).toBeInTheDocument();
    });
  });

  describe('Mark as Read', () => {
    const mockMessagesWithUnread = [
      {
        message_id: 'msg-1',
        thread_id: 'thread-1',
        agent_id: 'agent:claude',
        body: { format: 'markdown', content: 'Unread message 1' },
        created_at: '2024-01-01T10:00:00Z',
        is_read: false,
      },
      {
        message_id: 'msg-2',
        thread_id: 'thread-1',
        agent_id: 'agent:claude',
        body: { format: 'markdown', content: 'Unread message 2' },
        created_at: '2024-01-01T11:00:00Z',
        is_read: false,
      },
      {
        message_id: 'msg-3',
        thread_id: 'thread-1',
        agent_id: 'user:test',
        body: { format: 'markdown', content: 'Already read' },
        created_at: '2024-01-01T12:00:00Z',
        is_read: true,
      },
    ];

    beforeEach(() => {
      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test Thread',
          messages: mockMessagesWithUnread,
          total_messages: 3,
        },
        isLoading: false,
        error: null,
      } as any);
    });

    it('should mark unread messages as read after 500ms debounce', async () => {
      vi.useRealTimers(); // Use real timers for this test

      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Initially, markAsRead should not be called
      expect(mockMarkAsRead).not.toHaveBeenCalled();

      // Wait for debounce to complete (500ms + buffer)
      await waitFor(
        () => {
          expect(mockMarkAsRead).toHaveBeenCalledWith(['msg-1', 'msg-2']);
        },
        { timeout: 1000 }
      );

      vi.useFakeTimers(); // Restore fake timers for other tests
    });

    it('should not mark messages as read if all are already read', () => {
      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test Thread',
          messages: [
            {
              message_id: 'msg-1',
              thread_id: 'thread-1',
              agent_id: 'agent:claude',
              body: { format: 'markdown', content: 'Already read' },
              created_at: '2024-01-01T10:00:00Z',
              is_read: true,
            },
          ],
          total_messages: 1,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      vi.advanceTimersByTime(500);

      expect(mockMarkAsRead).not.toHaveBeenCalled();
    });

    it('should cancel debounce timer when collapsed', () => {
      const { rerender } = renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Fast-forward 300ms (not enough to trigger)
      vi.advanceTimersByTime(300);

      // Collapse the thread
      rerender(
        <QueryClientProvider client={queryClient}>
          <ThreadItem
            thread={mockThread}
            expanded={false}
            onToggle={mockOnToggle}
            sendingAs="user:test"
            isImpersonating={false}
          />
        </QueryClientProvider>
      );

      // Fast-forward remaining time
      vi.advanceTimersByTime(300);

      // markAsRead should not be called because timer was cancelled
      expect(mockMarkAsRead).not.toHaveBeenCalled();
    });
  });

  describe('Impersonation', () => {
    it('should pass isImpersonating to InlineReply', () => {
      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test Thread',
          messages: [],
          total_messages: 0,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(
        <ThreadItem
          thread={mockThread}
          expanded={true}
          onToggle={mockOnToggle}
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      // Should show disclosure checkbox in InlineReply
      expect(screen.getByRole('checkbox')).toBeInTheDocument();
    });
  });
});
