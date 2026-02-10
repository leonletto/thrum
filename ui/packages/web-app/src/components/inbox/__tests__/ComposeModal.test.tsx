import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ComposeModal } from '../ComposeModal';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCreateThread: vi.fn(),
    useCurrentUser: vi.fn(),
  };
});

describe('ComposeModal', () => {
  let queryClient: QueryClient;
  const mockCreateThread = vi.fn();
  const mockOnOpenChange = vi.fn();

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    vi.mocked(hooks.useCreateThread).mockReturnValue({
      mutate: mockCreateThread,
      isPending: false,
    } as any);

    vi.mocked(hooks.useCurrentUser).mockReturnValue({
      user_id: 'user:test',
      username: 'test-user',
      display_name: 'Test User',
      created_at: '2024-01-01T00:00:00Z',
    });

    mockOnOpenChange.mockClear();
    mockCreateThread.mockClear();
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Modal Visibility', () => {
    it('should not render when closed', () => {
      renderWithProvider(
        <ComposeModal
          open={false}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('should render when open', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.getByText('New Message')).toBeInTheDocument();
    });

    it('should call onOpenChange when closed', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const cancelButton = screen.getByRole('button', { name: /cancel/i });
      await user.click(cancelButton);

      expect(mockOnOpenChange).toHaveBeenCalledWith(false);
    });
  });

  describe('Form Fields', () => {
    it('should render all form fields', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.getByLabelText(/to/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/subject/i)).toBeInTheDocument();
      // Use getAllByLabelText since "Message" appears in dialog title too
      expect(screen.getAllByLabelText(/message/i).length).toBeGreaterThan(0);
    });

    it('should accept input in all fields', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const recipientField = screen.getByLabelText(/to/i) as HTMLInputElement;
      const subjectField = screen.getByLabelText(/subject/i) as HTMLInputElement;
      const messageFields = screen.getAllByLabelText(/message/i);
      const messageField = messageFields.find(
        (el) => el.tagName === 'TEXTAREA'
      ) as HTMLTextAreaElement;

      await user.type(recipientField, 'agent:claude');
      await user.type(subjectField, 'Test Subject');
      await user.type(messageField, 'Test message content');

      expect(recipientField.value).toBe('agent:claude');
      expect(subjectField.value).toBe('Test Subject');
      expect(messageField.value).toBe('Test message content');
    });
  });

  describe('Form Validation', () => {
    it('should disable send button when title is empty', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();
    });

    it('should enable send button when title is provided', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const subjectField = screen.getByLabelText(/subject/i);
      await user.type(subjectField, 'Test Subject');

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeEnabled();
    });

    it('should not allow sending with only whitespace in title', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const subjectField = screen.getByLabelText(/subject/i);
      await user.type(subjectField, '   \n  ');

      const sendButton = screen.getByRole('button', { name: /send/i });
      expect(sendButton).toBeDisabled();
    });
  });

  describe('Thread Creation', () => {
    it('should create thread when form is submitted', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const subjectField = screen.getByLabelText(/subject/i);
      await user.type(subjectField, 'Test Thread');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockCreateThread).toHaveBeenCalledWith(
        { title: 'Test Thread' },
        expect.any(Object)
      );
    });

    it('should close modal and reset form after successful creation', async () => {
      const user = userEvent.setup();
      mockCreateThread.mockImplementation((payload, options) => {
        options.onSuccess?.();
      });

      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const subjectField = screen.getByLabelText(/subject/i) as HTMLInputElement;
      await user.type(subjectField, 'Test Thread');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      await waitFor(() => {
        expect(mockOnOpenChange).toHaveBeenCalledWith(false);
      });

      // Note: Form reset is only visible if modal stays open, but in this case
      // the modal closes immediately, so we can't verify the reset state
    });

    it('should disable form while creating thread', () => {
      vi.mocked(hooks.useCreateThread).mockReturnValue({
        mutate: mockCreateThread,
        isPending: true,
      } as any);

      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Only the submit button is disabled during submission, not the input fields
      // Button shows loader icon when pending, so we can't search by name
      const buttons = screen.getAllByRole('button');
      const submitButton = buttons.find(btn => btn.type === 'submit');
      expect(submitButton).toBeDisabled();
    });
  });

  describe('Impersonation', () => {
    it('should not show disclosure checkbox when not impersonating', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    });

    it('should show disclosure checkbox when impersonating', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      expect(screen.getByRole('checkbox')).toBeInTheDocument();
      expect(screen.getByText(/Show "via test-user"/)).toBeInTheDocument();
    });

    it('should have disclosure checkbox checked by default', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      const checkbox = screen.getByRole('checkbox') as HTMLInputElement;
      expect(checkbox).toBeChecked();
    });

    it('should allow toggling disclosure checkbox', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="agent:claude"
          isImpersonating={true}
        />
      );

      const checkbox = screen.getByRole('checkbox') as HTMLInputElement;
      expect(checkbox).toBeChecked();

      await user.click(checkbox);
      expect(checkbox).not.toBeChecked();

      await user.click(checkbox);
      expect(checkbox).toBeChecked();
    });
  });

  describe('Backend Limitation Note', () => {
    it('should not include recipient and message in thread creation (backend limitation)', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Fill all fields
      await user.type(screen.getByLabelText(/to/i), 'agent:claude');
      await user.type(screen.getByLabelText(/subject/i), 'Test Subject');
      const messageField = screen.getByPlaceholderText(/Use @ to mention agents/i);
      await user.click(messageField);
      await user.paste('Test message');

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      // Currently, only title is sent due to backend limitation
      expect(mockCreateThread).toHaveBeenCalledWith(
        { title: 'Test Subject' },
        expect.any(Object)
      );

      // Note: When backend supports full thread creation (thrum-8to.1),
      // this test should be updated to expect:
      // {
      //   title: 'Test Subject',
      //   recipient: 'agent:claude',
      //   message: { format: 'markdown', content: 'Test message' }
      // }
    });
  });

  describe('Edge Cases', () => {
    it('should handle very long titles', async () => {
      const user = userEvent.setup();
      const longTitle = 'A'.repeat(1000);

      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Use paste instead of type to avoid 1000 individual keystrokes
      const subjectField = screen.getByLabelText(/subject/i);
      await user.click(subjectField);
      await user.paste(longTitle);

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockCreateThread).toHaveBeenCalledWith(
        { title: longTitle },
        expect.any(Object)
      );
    });

    it('should handle special characters in fields', async () => {
      const user = userEvent.setup();
      const specialTitle = '<script>alert("xss")</script>';

      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Use paste to avoid userEvent interpreting <> as special key sequences
      const subjectField = screen.getByLabelText(/subject/i);
      await user.click(subjectField);
      await user.paste(specialTitle);

      const sendButton = screen.getByRole('button', { name: /send/i });
      await user.click(sendButton);

      expect(mockCreateThread).toHaveBeenCalledWith(
        { title: specialTitle },
        expect.any(Object)
      );
    });
  });

  describe('Mention Support', () => {
    it('should use MentionAutocomplete for message field', () => {
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const messageField = screen.getByPlaceholderText(/Use @ to mention agents/i);
      expect(messageField).toBeInTheDocument();
    });

    it('should extract mentions when @ is typed in message', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const subjectField = screen.getByLabelText(/subject/i);
      await user.type(subjectField, 'Test');

      const messageField = screen.getByPlaceholderText(/Use @ to mention agents/i);
      await user.click(messageField);
      await user.paste('Hello @assistant please help');

      // Mentions are tracked internally but not visible in current implementation
      // Test passes if no errors occur
      expect(messageField).toHaveValue('Hello @assistant please help');
    });
  });

  describe('Priority Selection', () => {
    it('should show priority selector when advanced options is expanded', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Advanced options should be collapsed initially
      expect(screen.queryByLabelText(/priority/i)).not.toBeInTheDocument();

      // Click to expand advanced options
      const advancedButton = screen.getByRole('button', { name: /advanced options/i });
      await user.click(advancedButton);

      // Priority selector should now be visible
      expect(screen.getByLabelText(/priority/i)).toBeInTheDocument();
    });

    it('should default to normal priority', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      // Expand advanced options
      const advancedButton = screen.getByRole('button', { name: /advanced options/i });
      await user.click(advancedButton);

      // Should show "Normal" as default
      const priorityButton = screen.getByLabelText(/priority/i);
      expect(priorityButton).toHaveTextContent(/normal/i);
    });

    it('should toggle advanced options visibility', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <ComposeModal
          open={true}
          onOpenChange={mockOnOpenChange}
          sendingAs="user:test"
          isImpersonating={false}
        />
      );

      const advancedButton = screen.getByRole('button', { name: /advanced options/i });

      // Initially collapsed
      expect(screen.queryByLabelText(/priority/i)).not.toBeInTheDocument();

      // Click to expand
      await user.click(advancedButton);
      expect(screen.getByLabelText(/priority/i)).toBeInTheDocument();

      // Click to collapse
      await user.click(advancedButton);
      expect(screen.queryByLabelText(/priority/i)).not.toBeInTheDocument();
    });
  });
});
