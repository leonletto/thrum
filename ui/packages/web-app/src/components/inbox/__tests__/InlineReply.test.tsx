import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { InlineReply } from '../InlineReply';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useSendMessage: vi.fn(),
    useCurrentUser: vi.fn(),
  };
});

describe('InlineReply', () => {
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

  describe('Basic Rendering', () => {
    it('should render textarea and send button', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByPlaceholderText(/Write a reply/i)).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /send/i })).toBeInTheDocument();
    });

    it('should have send button disabled when content is empty', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();
    });

    it('should enable send button when content is entered', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(textarea, 'Hello world');

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeEnabled();
    });
  });

  describe('Sending Messages', () => {
    it('should send message when send button is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(textarea, 'Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Test message',
          thread_id: 'thread-1',
          body: { format: 'markdown', content: 'Test message' },
        },
        expect.any(Object)
      );
    });

    it('should not send empty messages', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();

      // Try to submit form directly (bypassing button disable)
      const form = sendButton.closest('form');
      if (form) {
        form.dispatchEvent(new Event('submit', { bubbles: true }));
      }

      expect(mockSendMessage).not.toHaveBeenCalled();
    });

    it('should not send messages with only whitespace', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(textarea, '   \n  \t  ');

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();
    });

    it('should clear textarea after successful send', async () => {
      const user = userEvent.setup();
      mockSendMessage.mockImplementation((payload, options) => {
        options.onSuccess?.();
      });

      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(
        /Write a reply/i
      ) as HTMLTextAreaElement;
      await user.type(textarea, 'Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(textarea.value).toBe('');
      });
    });

    it('should disable textarea and button while sending', () => {
      vi.mocked(hooks.useSendMessage).mockReturnValue({
        mutate: mockSendMessage,
        isPending: true,
      } as any);

      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByPlaceholderText(/Write a reply/i)).toBeDisabled();
      // Button shows loader icon when pending, not text "Send"
      // Find the submit button specifically since there are multiple buttons
      const buttons = screen.getAllByRole('button');
      const submitButton = buttons.find((btn) => (btn as HTMLButtonElement).type === 'submit');
      expect(submitButton).toBeDisabled();
    });
  });

  describe('Impersonation', () => {
    it('should not show disclosure checkbox when not impersonating', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    });

    it('should show disclosure checkbox when impersonating', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      expect(screen.getByRole('checkbox')).toBeInTheDocument();
      expect(screen.getByText(/Show "via test-user"/)).toBeInTheDocument();
    });

    it('should have disclosure checkbox checked by default', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      const checkbox = screen.getByRole('checkbox') as HTMLInputElement;
      expect(checkbox).toBeChecked();
    });

    it('should include acting_as and disclosed when impersonating', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(textarea, 'Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Test message',
          thread_id: 'thread-1',
          body: { format: 'markdown', content: 'Test message' },
          acting_as: 'agent:claude',
          disclosed: true,
        },
        expect.any(Object)
      );
    });

    it('should include disclosed: false when checkbox unchecked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      // Uncheck disclosure checkbox
      const checkbox = screen.getByRole('checkbox');
      await user.click(checkbox);

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.type(textarea, 'Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        {
          content: 'Test message',
          thread_id: 'thread-1',
          body: { format: 'markdown', content: 'Test message' },
          acting_as: 'agent:claude',
          disclosed: false,
        },
        expect.any(Object)
      );
    });
  });

  describe('Edge Cases', () => {
    it('should handle very long messages', async () => {
      const user = userEvent.setup();
      const longMessage = 'A'.repeat(10000);

      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      // Use paste instead of type for long strings to avoid timeout
      await user.click(textarea);
      await user.paste(longMessage);

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: longMessage,
          body: { format: 'markdown', content: longMessage },
        }),
        expect.any(Object)
      );
    });

    it('should handle special characters in messages', async () => {
      const user = userEvent.setup();
      const specialMessage = '<script>alert("xss")</script>';

      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Write a reply/i);
      await user.click(textarea);
      await user.paste(specialMessage);

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockSendMessage).toHaveBeenCalledWith(
        expect.objectContaining({
          content: specialMessage,
          body: { format: 'markdown', content: specialMessage },
        }),
        expect.any(Object)
      );
    });
  });

  describe('Mention Support', () => {
    it('should use MentionAutocomplete for reply field', () => {
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const replyField = screen.getByPlaceholderText(/Use @ to mention agents/i);
      expect(replyField).toBeInTheDocument();
    });

    it('should include mentions when sending message with @', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Use @ to mention agents/i);
      await user.click(textarea);
      await user.paste('Hey @assistant can you help?');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(mockSendMessage).toHaveBeenCalledWith(
          expect.objectContaining({
            content: 'Hey @assistant can you help?',
            mentions: ['assistant'],
          }),
          expect.any(Object)
        );
      });
    });

    it('should not include mentions field when no mentions present', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Use @ to mention agents/i);
      await user.type(textarea, 'Plain message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(mockSendMessage).toHaveBeenCalledWith(
          expect.objectContaining({
            content: 'Plain message',
          }),
          expect.any(Object)
        );
        // Should not have mentions field
        expect(mockSendMessage.mock.calls[0][0]).not.toHaveProperty('mentions');
      });
    });

    it('should handle multiple mentions', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <InlineReply
          threadId="thread-1"
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const textarea = screen.getByPlaceholderText(/Use @ to mention agents/i);
      await user.click(textarea);
      await user.paste('@assistant and @researcher please review');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(mockSendMessage).toHaveBeenCalledWith(
          expect.objectContaining({
            mentions: ['assistant', 'researcher'],
          }),
          expect.any(Object)
        );
      });
    });
  });
});
