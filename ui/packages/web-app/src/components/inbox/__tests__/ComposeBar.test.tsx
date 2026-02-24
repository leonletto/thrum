import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ComposeBar } from '../ComposeBar';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useSendMessage: vi.fn(),
    useCurrentUser: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
  };
});

describe('ComposeBar', () => {
  let queryClient: QueryClient;
  const mockSendMessage = vi.fn();

  beforeEach(() => {
    mockSendMessage.mockClear();

    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    vi.mocked(hooks.useSendMessage).mockReturnValue({
      mutate: mockSendMessage,
      isPending: false,
    } as any);

    vi.mocked(hooks.useCurrentUser).mockReturnValue({
      user_id: 'user:testuser',
      username: 'testuser',
      display_name: 'Test User',
      created_at: '2024-01-01T00:00:00Z',
    });

    vi.mocked(hooks.useAgentList).mockReturnValue({
      data: {
        agents: [
          {
            agent_id: 'agent:claude',
            kind: 'agent' as const,
            role: 'assistant',
            module: 'core',
            display: 'claude',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: new Date().toISOString(),
          },
          {
            agent_id: 'agent:reviewer',
            kind: 'agent' as const,
            role: 'reviewer',
            module: 'core',
            display: 'reviewer',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2020-01-01T00:00:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    } as any);

    vi.mocked(hooks.useGroupList).mockReturnValue({
      data: {
        groups: [
          {
            group_id: 'group:backend-team',
            name: 'backend-team',
            member_count: 3,
            created_at: '2024-01-01T00:00:00Z',
          },
        ],
      },
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
    it('renders inline (not a modal)', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      // Should NOT render as a dialog/modal
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

      // Should render inline compose bar
      expect(screen.getByTestId('compose-bar')).toBeInTheDocument();
    });

    it('renders textarea and send button', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      expect(
        screen.getByPlaceholderText(/Write a message/i)
      ).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /send/i })).toBeInTheDocument();
    });

    it('disables send button when content is empty', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();
    });

    it('enables send button when content is entered', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Hello world');

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeEnabled();
    });
  });

  describe('To field visibility', () => {
    it('shows To field in inbox view when no groupScope is set', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      expect(screen.getByText('To:')).toBeInTheDocument();
      expect(
        screen.getByRole('button', { name: /add recipients/i })
      ).toBeInTheDocument();
    });

    it('hides To field in group view when groupScope is set', () => {
      renderWithProvider(
        <ComposeBar
          sendingAs="testuser"
          isImpersonating={false}
          groupScope="backend-team"
        />
      );

      expect(screen.queryByText('To:')).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /add recipients/i })
      ).not.toBeInTheDocument();
    });
  });

  describe('Reply mode', () => {
    it('shows reply chip when replyTo is provided', () => {
      renderWithProvider(
        <ComposeBar
          sendingAs="testuser"
          isImpersonating={false}
          replyTo={{ messageId: 'msg-123', senderName: 'alice' }}
        />
      );

      expect(screen.getByText(/Replying to: @alice/i)).toBeInTheDocument();
    });

    it('does not show reply chip when replyTo is not set', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      expect(screen.queryByText(/Replying to/i)).not.toBeInTheDocument();
    });

    it('calls onClearReply when X button is clicked', async () => {
      const user = userEvent.setup();
      const mockClearReply = vi.fn();

      renderWithProvider(
        <ComposeBar
          sendingAs="testuser"
          isImpersonating={false}
          replyTo={{ messageId: 'msg-123', senderName: 'alice' }}
          onClearReply={mockClearReply}
        />
      );

      const clearButton = screen.getByRole('button', { name: /clear reply/i });
      await user.click(clearButton);

      expect(mockClearReply).toHaveBeenCalledOnce();
    });
  });

  describe('Impersonation indicator', () => {
    it('shows impersonation indicator when isImpersonating is true', () => {
      renderWithProvider(
        <ComposeBar sendingAs="agent:claude" isImpersonating={true} />
      );

      expect(screen.getByText(/Sending as: agent:claude/i)).toBeInTheDocument();
    });

    it('does not show impersonation indicator when not impersonating', () => {
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      expect(screen.queryByText(/Sending as:/i)).not.toBeInTheDocument();
    });
  });

  describe('Send behavior', () => {
    it('sends message with correct fields in inbox view', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Hello world');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: 'Hello world',
          caller_agent_id: 'user:testuser',
        }),
        expect.any(Object)
      );
    });

    it('sends message with group scope when groupScope is set', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar
          sendingAs="testuser"
          isImpersonating={false}
          groupScope="backend-team"
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Team update');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: 'Team update',
          scopes: [{ type: 'group', value: 'backend-team' }],
        }),
        expect.any(Object)
      );
    });

    it('sends message with reply_to when replyTo is set', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar
          sendingAs="testuser"
          isImpersonating={false}
          replyTo={{ messageId: 'msg-abc', senderName: 'alice' }}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Sure thing');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: 'Sure thing',
          reply_to: 'msg-abc',
        }),
        expect.any(Object)
      );
    });

    it('sends message with acting_as when impersonating', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="agent:claude" isImpersonating={true} />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Hello from agent');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: 'Hello from agent',
          acting_as: 'agent:claude',
        }),
        expect.any(Object)
      );
    });

    it('does not include acting_as when not impersonating', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const textarea = screen.getByPlaceholderText(/Write a message/i);
      await user.type(textarea, 'Hello');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.not.objectContaining({
          acting_as: expect.anything(),
        }),
        expect.any(Object)
      );
    });
  });

  describe('Content clearing', () => {
    it('clears content after successful send', async () => {
      const user = userEvent.setup();
      mockSendMessage.mockImplementation((_payload: unknown, options: { onSuccess?: (data: unknown) => void }) => {
        options.onSuccess?.({ message_id: 'msg-new', created_at: new Date().toISOString() });
      });

      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const textarea = screen.getByPlaceholderText(
        /Write a message/i
      ) as HTMLTextAreaElement;
      await user.type(textarea, 'Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(textarea.value).toBe('');
      });
    });
  });

  describe('Recipient dropdown', () => {
    it('opens recipient dropdown when Select is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const selectButton = screen.getByRole('button', {
        name: /add recipients/i,
      });
      await user.click(selectButton);

      expect(screen.getByTestId('recipient-dropdown')).toBeInTheDocument();
    });

    it('shows agents section in dropdown', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const selectButton = screen.getByRole('button', {
        name: /add recipients/i,
      });
      await user.click(selectButton);

      expect(screen.getByText('Agents')).toBeInTheDocument();
      expect(screen.getByText('claude')).toBeInTheDocument();
    });

    it('shows groups section in dropdown', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const selectButton = screen.getByRole('button', {
        name: /add recipients/i,
      });
      await user.click(selectButton);

      expect(screen.getByText('Groups')).toBeInTheDocument();
      expect(screen.getByText('@backend-team')).toBeInTheDocument();
    });

    it('closes dropdown when Done is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeBar sendingAs="testuser" isImpersonating={false} />
      );

      const selectButton = screen.getByRole('button', {
        name: /add recipients/i,
      });
      await user.click(selectButton);

      expect(screen.getByTestId('recipient-dropdown')).toBeInTheDocument();

      const doneButton = screen.getByRole('button', { name: /done/i });
      await user.click(doneButton);

      expect(
        screen.queryByTestId('recipient-dropdown')
      ).not.toBeInTheDocument();
    });
  });
});
