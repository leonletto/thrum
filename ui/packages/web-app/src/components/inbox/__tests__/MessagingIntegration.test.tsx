import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InboxView } from '../InboxView';
import * as hooks from '@thrum/shared-logic';

// Mock all shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    useThreadList: vi.fn(),
    useThread: vi.fn(),
    useSendMessage: vi.fn(),
    useCreateThread: vi.fn(),
    useMarkAsRead: vi.fn(),
  };
});

describe('Messaging Integration Tests', () => {
  let queryClient: QueryClient;
  const mockSendMessage = vi.fn();
  const mockCreateThread = vi.fn();
  const mockMarkAsRead = vi.fn();

  const mockCurrentUser = {
    user_id: 'user:leon',
    username: 'leon',
    display_name: 'Leon',
    created_at: '2024-01-01T00:00:00Z',
  };

  const mockThreads = [
    {
      thread_id: 'thread-1',
      title: 'Help with API',
      created_by: 'user:leon',
      created_at: '2024-01-01T10:00:00Z',
      message_count: 3,
      last_activity: '2024-01-01T12:00:00Z',
      unread_count: 1,
      last_sender: 'agent:claude',
      preview: 'Sure, I can help with that',
    },
  ];

  const mockMessages = [
    {
      message_id: 'msg-1',
      thread_id: 'thread-1',
      agent_id: 'user:leon',
      body: { format: 'markdown', content: 'Can you help me with the API?' },
      created_at: '2024-01-01T10:00:00Z',
      scopes: [],
      refs: [],
      is_read: true,
    },
    {
      message_id: 'msg-2',
      thread_id: 'thread-1',
      agent_id: 'agent:claude',
      body: { format: 'markdown', content: 'Sure, I can help with that!' },
      created_at: '2024-01-01T11:00:00Z',
      scopes: [],
      refs: [],
      is_read: false,
    },
  ];

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    // Setup default mocks
    vi.mocked(hooks.useCurrentUser).mockReturnValue(mockCurrentUser);

    vi.mocked(hooks.useThreadList).mockReturnValue({
      data: { threads: mockThreads },
      isLoading: false,
      error: null,
    } as any);

    vi.mocked(hooks.useThread).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: null,
    } as any);

    vi.mocked(hooks.useSendMessage).mockReturnValue({
      mutate: mockSendMessage,
      isPending: false,
    } as any);

    vi.mocked(hooks.useCreateThread).mockReturnValue({
      mutate: mockCreateThread,
      isPending: false,
    } as any);

    vi.mocked(hooks.useMarkAsRead).mockReturnValue({
      mutate: mockMarkAsRead,
      isPending: false,
    } as any);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Complete User Flow: Own Inbox', () => {
    it('should view inbox, expand thread, and send reply', async () => {
      const user = userEvent.setup({ delay: null });

      // Mock thread details to load when expanded
      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Help with API',
          messages: mockMessages,
          total_messages: 2,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(<InboxView />);

      // 1. Verify inbox loads with user's identity
      expect(screen.getByText('leon')).toBeInTheDocument();
      expect(screen.getByText('Help with API')).toBeInTheDocument();

      // 2. Expand thread
      const threadCard = screen.getByText('Help with API').closest('div[role="button"]');
      if (threadCard) {
        await user.click(threadCard);
      }

      // 3. Verify messages appear
      await waitFor(() => {
        expect(screen.getByText('Can you help me with the API?')).toBeInTheDocument();
        expect(screen.getByText('Sure, I can help with that!')).toBeInTheDocument();
      }, { timeout: 3000 });

      // 4. Verify markAsRead was called for unread messages
      await waitFor(() => {
        expect(mockMarkAsRead).toHaveBeenCalledWith(['msg-2']);
      }, { timeout: 3000 });

      // 5. Send reply
      const replyTextarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(replyTextarea, 'Thank you!');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      // 6. Verify message sent
      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Thank you!',
          thread_id: 'thread-1',
          body: { format: 'markdown', content: 'Thank you!' },
        },
        expect.objectContaining({ onSuccess: expect.any(Function) })
      );
    }, 15000);
  });

  describe('Complete User Flow: Agent Impersonation', () => {
    it('should view agent inbox, see warning, and send with disclosure', async () => {
      const user = userEvent.setup({ delay: null });

      // Mock thread in agent's inbox
      const agentThreads = [
        {
          thread_id: 'thread-2',
          title: 'Agent task',
          created_by: 'user:other',
          created_at: '2024-01-01T10:00:00Z',
          message_count: 1,
          last_activity: '2024-01-01T10:00:00Z',
          unread_count: 1,
        },
      ];

      const agentMessages = [
        {
          message_id: 'msg-3',
          thread_id: 'thread-2',
          agent_id: 'user:other',
          body: { format: 'markdown', content: 'Please handle this task' },
          created_at: '2024-01-01T10:00:00Z',
          scopes: [],
          refs: [],
          is_read: false,
        },
      ];

      vi.mocked(hooks.useThreadList).mockReturnValue({
        data: { threads: agentThreads },
        isLoading: false,
        error: null,
      } as any);

      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-2',
          title: 'Agent task',
          messages: agentMessages,
          total_messages: 1,
        },
        isLoading: false,
        error: null,
      } as any);

      // Render with agent identity
      renderWithProvider(<InboxView identityId="agent:claude-daemon" />);

      // 1. Verify impersonation indicator
      expect(screen.getByText(/Sending as agent:claude-daemon/)).toBeInTheDocument();

      // 2. Expand thread
      const threadCard = screen.getByText('Agent task').closest('div[role="button"]');
      await user.click(threadCard!);

      // 3. Verify disclosure checkbox appears
      await waitFor(() => {
        expect(screen.getByRole('checkbox')).toBeInTheDocument();
        expect(screen.getByText(/Show "via leon"/)).toBeInTheDocument();
      });

      // 4. Verify checkbox is checked by default
      const checkbox = screen.getByRole('checkbox') as HTMLInputElement;
      expect(checkbox).toBeChecked();

      // 5. Send reply with disclosure
      const replyTextarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(replyTextarea, 'Task acknowledged');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      // 6. Verify message sent with impersonation fields
      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Task acknowledged',
          thread_id: 'thread-2',
          body: { format: 'markdown', content: 'Task acknowledged' },
          acting_as: 'agent:claude-daemon',
          disclosed: true,
        },
        expect.objectContaining({ onSuccess: expect.any(Function) })
      );
    });

    it('should send with disclosed:false when checkbox unchecked', async () => {
      const user = userEvent.setup({ delay: null });

      const agentThreads = [
        {
          thread_id: 'thread-2',
          title: 'Agent task',
          created_by: 'user:other',
          created_at: '2024-01-01T10:00:00Z',
          message_count: 1,
          last_activity: '2024-01-01T10:00:00Z',
          unread_count: 0,
        },
      ];

      const agentMessages = [
        {
          message_id: 'msg-3',
          thread_id: 'thread-2',
          agent_id: 'user:other',
          body: { format: 'markdown', content: 'Please handle this task' },
          created_at: '2024-01-01T10:00:00Z',
          scopes: [],
          refs: [],
          is_read: true,
        },
      ];

      vi.mocked(hooks.useThreadList).mockReturnValue({
        data: { threads: agentThreads },
        isLoading: false,
        error: null,
      } as any);

      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-2',
          title: 'Agent task',
          messages: agentMessages,
          total_messages: 1,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(<InboxView identityId="agent:claude-daemon" />);

      // Expand thread
      const threadCard = screen.getByText('Agent task').closest('div[role="button"]');
      await user.click(threadCard!);

      // Uncheck disclosure
      await waitFor(() => {
        expect(screen.getByRole('checkbox')).toBeInTheDocument();
      });

      const checkbox = screen.getByRole('checkbox');
      await user.click(checkbox);

      // Send reply
      const replyTextarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(replyTextarea, 'Hidden identity reply');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      // Verify disclosed is false
      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Hidden identity reply',
          thread_id: 'thread-2',
          body: { format: 'markdown', content: 'Hidden identity reply' },
          acting_as: 'agent:claude-daemon',
          disclosed: false,
        },
        expect.objectContaining({ onSuccess: expect.any(Function) })
      );
    });
  });

  describe('Complete User Flow: Compose New Thread', () => {
    it('should open compose modal and create thread', async () => {
      const user = userEvent.setup({ delay: null });

      renderWithProvider(<InboxView />);

      // 1. Click Compose button
      const composeButton = screen.getByText('+ COMPOSE');
      await user.click(composeButton);

      // 2. Verify modal opens
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      });

      // 3. Fill in subject
      const subjectField = screen.getByLabelText(/subject/i);
      await user.type(subjectField, 'New Discussion');

      // 4. Submit
      const sendButton = screen.getAllByRole('button', { name: /send/i })[0];
      await user.click(sendButton);

      // 5. Verify thread created
      expect(mockCreateThread).toHaveBeenCalledWith(
        { title: 'New Discussion' },
        expect.any(Object)
      );
    });
  });

  describe('Message Display with Impersonation', () => {
    it('should show [via] badge for disclosed impersonated messages', async () => {
      const user = userEvent.setup({ delay: null });
      const messagesWithImpersonation = [
        {
          message_id: 'msg-1',
          thread_id: 'thread-1',
          agent_id: 'agent:claude',
          authored_by: 'user:leon',
          disclosed: true,
          body: { format: 'markdown', content: 'I am helping as Claude' },
          created_at: '2024-01-01T10:00:00Z',
          scopes: [],
          refs: [],
          is_read: true,
        },
      ];

      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test',
          messages: messagesWithImpersonation,
          total_messages: 1,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(<InboxView />);

      // Expand thread
      const threadCard = screen.getByText('Help with API').closest('div[role="button"]');
      await user.click(threadCard!);

      // Verify via badge appears
      await waitFor(() => {
        expect(screen.getByText(/via user:leon/)).toBeInTheDocument();
      });
    });

    it('should not show [via] badge for non-disclosed impersonated messages', async () => {
      const user = userEvent.setup({ delay: null });
      const messagesWithHiddenImpersonation = [
        {
          message_id: 'msg-1',
          thread_id: 'thread-1',
          agent_id: 'agent:claude',
          authored_by: 'user:leon',
          disclosed: false,
          body: { format: 'markdown', content: 'Hidden identity' },
          created_at: '2024-01-01T10:00:00Z',
          scopes: [],
          refs: [],
          is_read: true,
        },
      ];

      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Test',
          messages: messagesWithHiddenImpersonation,
          total_messages: 1,
        },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(<InboxView />);

      // Expand thread
      const threadCard = screen.getByText('Help with API').closest('div[role="button"]');
      await user.click(threadCard!);

      // Verify via badge does NOT appear
      await waitFor(() => {
        expect(screen.queryByText(/via/)).not.toBeInTheDocument();
      });
    });
  });

  describe('Error Handling', () => {
    it('should handle thread loading errors', () => {
      vi.mocked(hooks.useThreadList).mockReturnValue({
        data: undefined,
        isLoading: false,
        error: new Error('Failed to load threads'),
      } as any);

      renderWithProvider(<InboxView />);

      // Should show error state (component implementation dependent)
      // For now, verify it doesn't crash
      expect(screen.getByText('leon')).toBeInTheDocument();
    });

    it('should handle send message errors gracefully', async () => {
      const user = userEvent.setup({ delay: null });

      vi.mocked(hooks.useThread).mockReturnValue({
        data: {
          thread_id: 'thread-1',
          title: 'Help with API',
          messages: mockMessages,
          total_messages: 2,
        },
        isLoading: false,
        error: null,
      } as any);

      mockSendMessage.mockImplementation((payload, options) => {
        options?.onError?.(new Error('Network error'));
      });

      renderWithProvider(<InboxView />);

      // Expand thread
      const threadCard = screen.getByText('Help with API').closest('div[role="button"]');
      await user.click(threadCard!);

      // Wait for messages to appear
      await waitFor(() => {
        expect(screen.getByPlaceholderText(/Write a reply/i)).toBeInTheDocument();
      });

      // Try to send message
      const replyTextarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(replyTextarea, 'This will fail');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      // Verify send was attempted but failed
      expect(mockSendMessage).toHaveBeenCalled();
    }, 15000);
  });
});
