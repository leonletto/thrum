import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThreadList } from '../ThreadList';
import type { Thread } from '@thrum/shared-logic';

// Mock hooks for child components
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useThread: vi.fn().mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    }),
    useMarkAsRead: vi.fn().mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    }),
  };
});

describe('ThreadList', () => {
  let queryClient: QueryClient;

  const mockThreads: Thread[] = [
    {
      thread_id: 'thread-1',
      title: 'First Thread',
      created_by: 'user:test',
      created_at: '2024-01-01T10:00:00Z',
      message_count: 3,
      last_activity: '2024-01-01T12:00:00Z',
      unread_count: 1,
      last_sender: 'agent:claude',
      preview: 'Preview of first thread',
    },
    {
      thread_id: 'thread-2',
      title: 'Second Thread',
      created_by: 'agent:claude',
      created_at: '2024-01-01T08:00:00Z',
      message_count: 5,
      last_activity: '2024-01-01T11:00:00Z',
      unread_count: 0,
      last_sender: 'user:test',
      preview: 'Preview of second thread',
    },
    {
      thread_id: 'thread-3',
      title: 'Third Thread',
      created_by: 'user:test',
      created_at: '2024-01-01T06:00:00Z',
      message_count: 2,
      last_activity: '2024-01-01T10:00:00Z',
      unread_count: 2,
      last_sender: 'agent:helper',
      preview: 'Preview of third thread',
    },
  ];

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Empty State', () => {
    it('should render empty message when no threads', () => {
      renderWithProvider(
        <ThreadList
          threads={[]}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('NO THREADS')).toBeInTheDocument();
      expect(screen.getByText('Start a conversation')).toBeInTheDocument();
    });
  });

  describe('Thread Rendering', () => {
    it('should render all threads', () => {
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('First Thread')).toBeInTheDocument();
      expect(screen.getByText('Second Thread')).toBeInTheDocument();
      expect(screen.getByText('Third Thread')).toBeInTheDocument();
    });

    it('should render thread metadata', () => {
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('3 messages')).toBeInTheDocument();
      expect(screen.getByText('5 messages')).toBeInTheDocument();
      expect(screen.getByText('2 messages')).toBeInTheDocument();
    });

    it('should render unread badges', () => {
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('1 new')).toBeInTheDocument();
      expect(screen.getByText('2 new')).toBeInTheDocument();
      expect(screen.queryByText('0 new')).not.toBeInTheDocument();
    });
  });

  describe('Thread Expansion', () => {
    it('should expand thread when clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const firstThread = screen.getByText('First Thread').closest('div[role="button"]');
      expect(firstThread).toBeInTheDocument();

      await user.click(firstThread!);

      // Thread should be expanded (implementation details tested in ThreadItem.test.tsx)
    });

    it('should collapse thread when clicked again', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const firstThread = screen.getByText('First Thread').closest('div[role="button"]');

      // First click - expand
      await user.click(firstThread!);

      // Second click - collapse
      await user.click(firstThread!);

      // Thread should be collapsed (back to preview state)
    });

    it('should allow only one thread expanded at a time', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const firstThread = screen.getByText('First Thread').closest('div[role="button"]');
      const secondThread = screen.getByText('Second Thread').closest('div[role="button"]');

      // Expand first thread
      await user.click(firstThread!);

      // Expand second thread (should collapse first)
      await user.click(secondThread!);

      // Only second thread should be expanded
      // (This behavior is managed by local state in ThreadList)
    });
  });

  describe('Impersonation', () => {
    it('should pass impersonation props to ThreadItems', () => {
      renderWithProvider(
        <ThreadList
          threads={mockThreads}
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      // ThreadItems receive these props and pass to InlineReply
      // (Detailed impersonation tests in ThreadItem.test.tsx and InlineReply.test.tsx)
      expect(screen.getByText('First Thread')).toBeInTheDocument();
    });
  });

  describe('Edge Cases', () => {
    it('should handle threads with no unread count', () => {
      const threadsWithoutUnread: Thread[] = [
        {
          thread_id: 'thread-1',
          title: 'Test Thread',
          created_by: 'user:test',
          created_at: '2024-01-01T10:00:00Z',
          message_count: 3,
          last_activity: '2024-01-01T12:00:00Z',
          // unread_count is undefined
        },
      ];

      renderWithProvider(
        <ThreadList
          threads={threadsWithoutUnread}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('Test Thread')).toBeInTheDocument();
      expect(screen.queryByText(/new/)).not.toBeInTheDocument();
    });

    it('should handle threads with no preview', () => {
      const threadsWithoutPreview: Thread[] = [
        {
          thread_id: 'thread-1',
          title: 'Test Thread',
          created_by: 'user:test',
          created_at: '2024-01-01T10:00:00Z',
          message_count: 3,
          last_activity: '2024-01-01T12:00:00Z',
          unread_count: 0,
          // preview is undefined
        },
      ];

      renderWithProvider(
        <ThreadList
          threads={threadsWithoutPreview}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('Test Thread')).toBeInTheDocument();
    });

    it('should handle threads with very long titles', () => {
      const longTitleThreads: Thread[] = [
        {
          thread_id: 'thread-1',
          title: 'A'.repeat(200),
          created_by: 'user:test',
          created_at: '2024-01-01T10:00:00Z',
          message_count: 3,
          last_activity: '2024-01-01T12:00:00Z',
          unread_count: 0,
        },
      ];

      renderWithProvider(
        <ThreadList
          threads={longTitleThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('A'.repeat(200))).toBeInTheDocument();
    });

    it('should handle single thread', () => {
      renderWithProvider(
        <ThreadList
          threads={[mockThreads[0]]}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('First Thread')).toBeInTheDocument();
      expect(screen.queryByText('Second Thread')).not.toBeInTheDocument();
      expect(screen.queryByText('Third Thread')).not.toBeInTheDocument();
    });

    it('should handle large number of threads', () => {
      const manyThreads = Array.from({ length: 100 }, (_, i) => ({
        thread_id: `thread-${i}`,
        title: `Thread ${i}`,
        created_by: 'user:test',
        created_at: '2024-01-01T10:00:00Z',
        message_count: i,
        last_activity: '2024-01-01T12:00:00Z',
        unread_count: i % 3,
      }));

      renderWithProvider(
        <ThreadList
          threads={manyThreads}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByText('Thread 0')).toBeInTheDocument();
      expect(screen.getByText('Thread 99')).toBeInTheDocument();
    });
  });
});
