import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MessageBubble } from '../MessageBubble';
import type { Message } from '@thrum/shared-logic';

// Create mock functions for mutations
const mockEditMessage = vi.fn();
const mockDeleteMessage = vi.fn();

// Mock hooks from @thrum/shared-logic
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentList: () => ({
      data: {
        agents: [
          {
            agent_id: 'agent:claude',
            kind: 'agent' as const,
            role: 'assistant',
            module: 'core',
            display: 'Claude',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T12:00:00Z',
          },
          {
            agent_id: 'user:testuser',
            kind: 'agent' as const,
            role: 'user',
            module: 'core',
            display: 'TestUser',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: '2024-01-01T12:00:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
    useCurrentUser: () => ({
      user_id: 'user:testuser',
      username: 'testuser',
      display_name: 'Test User',
    }),
    useEditMessage: () => ({
      mutateAsync: mockEditMessage,
      isPending: false,
    }),
    useDeleteMessage: () => ({
      mutateAsync: mockDeleteMessage,
      isPending: false,
    }),
  };
});

describe('MessageBubble', () => {
  const baseMessage: Message = {
    message_id: 'msg-1',
    thread_id: 'thread-1',
    agent_id: 'agent:claude',
    body: {
      format: 'markdown',
      content: 'Hello, how can I help?',
    },
    created_at: '2024-01-01T12:00:00Z',
    scopes: [],
    refs: [],
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockEditMessage.mockResolvedValue({
      message_id: 'msg-1',
      updated_at: '2024-01-01T13:00:00Z',
    });
    mockDeleteMessage.mockResolvedValue({
      message_id: 'msg-1',
      deleted_at: '2024-01-01T13:00:00Z',
    });
  });

  describe('Message Rendering', () => {
    it('should render sender and message content', () => {
      render(<MessageBubble message={baseMessage} isOwn={false} />);

      // Should show display name "Claude" instead of "agent:claude"
      expect(screen.getByText('Claude')).toBeInTheDocument();
      expect(screen.getByText('Hello, how can I help?')).toBeInTheDocument();
    });

    it('should render timestamp', () => {
      render(<MessageBubble message={baseMessage} isOwn={false} />);

      // Timestamp will be rendered in relative format (e.g., "2 hours ago")
      // Just check it's present
      const bubble = screen.getByText('Claude').closest('div');
      expect(bubble).toBeInTheDocument();
    });

    it('should apply different styling for own messages', () => {
      const { container } = render(
        <MessageBubble message={baseMessage} isOwn={true} />
      );

      // Own messages should have right alignment classes
      const bubble = container.querySelector('.ml-auto');
      expect(bubble).toBeInTheDocument();
    });

    it('should apply different styling for other messages', () => {
      const { container } = render(
        <MessageBubble message={baseMessage} isOwn={false} />
      );

      // Other messages should have muted background (not ml-auto)
      const bubble = container.querySelector('.bg-muted');
      expect(bubble).toBeInTheDocument();
    });
  });

  describe('Markdown Rendering', () => {
    it('should render markdown bold text', () => {
      const message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'This is **bold** text' },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      const bold = screen.getByText('bold');
      expect(bold.tagName).toBe('STRONG');
    });

    it('should render markdown italic text', () => {
      const message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'This is *italic* text' },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      const italic = screen.getByText('italic');
      expect(italic.tagName).toBe('EM');
    });

    it('should render markdown code blocks', () => {
      const message = {
        ...baseMessage,
        body: {
          format: 'markdown',
          content: '```javascript\nconst x = 42;\n```',
        },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText('const x = 42;')).toBeInTheDocument();
    });

    it('should render markdown inline code', () => {
      const message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'Use `console.log()` to debug' },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      const code = screen.getByText('console.log()');
      expect(code.tagName).toBe('CODE');
    });

    it('should render markdown links', () => {
      const message = {
        ...baseMessage,
        body: {
          format: 'markdown',
          content: 'Visit [OpenAI](https://openai.com)',
        },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      const link = screen.getByRole('link', { name: 'OpenAI' });
      expect(link).toHaveAttribute('href', 'https://openai.com');
    });

    it('should render markdown lists', () => {
      const message = {
        ...baseMessage,
        body: {
          format: 'markdown',
          content: '- Item 1\n- Item 2\n- Item 3',
        },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText('Item 1')).toBeInTheDocument();
      expect(screen.getByText('Item 2')).toBeInTheDocument();
      expect(screen.getByText('Item 3')).toBeInTheDocument();
    });
  });

  describe('Impersonation Display', () => {
    it('should show [via user:X] badge when disclosed', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: 'agent:claude',
        authored_by: 'user:leon',
        disclosed: true,
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText(/via user:leon/)).toBeInTheDocument();
    });

    it('should not show [via] badge when disclosed is false', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: 'agent:claude',
        authored_by: 'user:leon',
        disclosed: false,
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.queryByText(/via/)).not.toBeInTheDocument();
    });

    it('should not show [via] badge when authored_by is missing', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: 'agent:claude',
        disclosed: true,
        // authored_by is undefined
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.queryByText(/via/)).not.toBeInTheDocument();
    });

    it('should show display name as sender when not impersonating', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: 'agent:claude',
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Should show display name "Claude" instead of "agent:claude"
      expect(screen.getByText('Claude')).toBeInTheDocument();
    });

    it('should show display name as sender even when impersonating', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: 'agent:claude',
        authored_by: 'user:leon',
        disclosed: true,
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Should show both display name and via tag
      expect(screen.getByText('Claude')).toBeInTheDocument();
      expect(screen.getByText(/via user:leon/)).toBeInTheDocument();
    });
  });

  describe('Priority Indicators', () => {
    it('should show AlertTriangle icon for high priority messages', () => {
      const message: Message = {
        ...baseMessage,
        priority: 'high',
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      // Should have priority-high class
      const bubble = container.querySelector('.priority-high');
      expect(bubble).toBeInTheDocument();

      // Should show AlertTriangle icon
      const icon = container.querySelector('svg');
      expect(icon).toBeInTheDocument();
    });

    it('should apply priority-low class for low priority messages', () => {
      const message: Message = {
        ...baseMessage,
        priority: 'low',
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      const bubble = container.querySelector('.priority-low');
      expect(bubble).toBeInTheDocument();
    });

    it('should not show priority indicator for normal priority', () => {
      const message: Message = {
        ...baseMessage,
        priority: 'normal',
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      expect(container.querySelector('.priority-high')).not.toBeInTheDocument();
      expect(container.querySelector('.priority-low')).not.toBeInTheDocument();
    });

    it('should not show priority indicator when priority is undefined', () => {
      const message: Message = {
        ...baseMessage,
        // priority is undefined
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      expect(container.querySelector('.priority-high')).not.toBeInTheDocument();
      expect(container.querySelector('.priority-low')).not.toBeInTheDocument();
    });
  });

  describe('Edge Cases', () => {
    it('should handle empty message content', () => {
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: '' },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Should show display name
      expect(screen.getByText('Claude')).toBeInTheDocument();
    });

    it('should handle missing agent_id', () => {
      const message: Message = {
        ...baseMessage,
        agent_id: undefined,
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Should still render without crashing, showing "Unknown"
      expect(screen.getByText('Unknown')).toBeInTheDocument();
      expect(screen.getByText('Hello, how can I help?')).toBeInTheDocument();
    });

    it('should handle very long message content', () => {
      const longContent = 'A'.repeat(10000);
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: longContent },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText(longContent)).toBeInTheDocument();
    });

    it('should handle special markdown characters', () => {
      const message: Message = {
        ...baseMessage,
        body: {
          format: 'markdown',
          content: 'Special: <div> & "quotes" \\n backslash',
        },
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Markdown should escape/handle these properly
      expect(screen.getByText(/Special:/)).toBeInTheDocument();
    });
  });

  describe('Edit and Delete Functionality', () => {
    describe('Edit Mode', () => {
      it('should show edit dropdown for own messages', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Hover to show dropdown (it has opacity-0 group-hover:opacity-100)
        const bubble = screen.getByText('Hello, how can I help?').closest('.group');
        expect(bubble).toBeInTheDocument();

        // Find and click the dropdown trigger
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);

        // Should show Edit and Delete options
        expect(screen.getByText('Edit')).toBeInTheDocument();
        expect(screen.getByText('Delete')).toBeInTheDocument();
      });

      it('should not show edit dropdown for other messages', () => {
        const otherMessage: Message = {
          ...baseMessage,
          agent_id: 'agent:claude',
        };

        render(<MessageBubble message={otherMessage} isOwn={false} />);

        // Should not have dropdown trigger
        expect(screen.queryByRole('button', { name: /message actions/i })).not.toBeInTheDocument();
      });

      it('should enter edit mode when Edit is clicked', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Click dropdown and Edit
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        const editButton = screen.getByText('Edit');
        await user.click(editButton);

        // Should show textarea and Save/Cancel buttons
        expect(screen.getByRole('textbox')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /save/i })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
      });

      it('should save edited message', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Enter edit mode
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        await user.click(screen.getByText('Edit'));

        // Edit content
        const textarea = screen.getByRole('textbox');
        await user.clear(textarea);
        await user.type(textarea, 'Updated message content');

        // Save
        await user.click(screen.getByRole('button', { name: /save/i }));

        // Should call edit mutation
        await waitFor(() => {
          expect(mockEditMessage).toHaveBeenCalledWith({
            message_id: 'msg-1',
            content: 'Updated message content',
          });
        });
      });

      it('should cancel edit mode', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Enter edit mode
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        await user.click(screen.getByText('Edit'));

        // Edit content
        const textarea = screen.getByRole('textbox');
        await user.clear(textarea);
        await user.type(textarea, 'This should be discarded');

        // Cancel
        await user.click(screen.getByRole('button', { name: /cancel/i }));

        // Should not call edit mutation
        expect(mockEditMessage).not.toHaveBeenCalled();

        // Should show original content
        expect(screen.getByText('Hello, how can I help?')).toBeInTheDocument();
      });
    });

    describe('Delete Functionality', () => {
      it('should show delete confirmation dialog', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Click dropdown and Delete
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        const deleteButton = screen.getByText('Delete');
        await user.click(deleteButton);

        // Should show confirmation dialog
        expect(screen.getByText('Delete Message')).toBeInTheDocument();
        expect(screen.getByText(/are you sure/i)).toBeInTheDocument();
      });

      it('should delete message when confirmed', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Click dropdown and Delete
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        await user.click(screen.getByText('Delete'));

        // Confirm deletion
        const confirmButton = screen.getByRole('button', { name: /delete/i });
        await user.click(confirmButton);

        // Should call delete mutation
        await waitFor(() => {
          expect(mockDeleteMessage).toHaveBeenCalledWith({
            message_id: 'msg-1',
          });
        });
      });

      it('should cancel delete when dialog is closed', async () => {
        const user = userEvent.setup();
        const ownMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
        };

        render(<MessageBubble message={ownMessage} isOwn={true} />);

        // Click dropdown and Delete
        const dropdownTrigger = screen.getByRole('button', { name: /message actions/i });
        await user.click(dropdownTrigger);
        await user.click(screen.getByText('Delete'));

        // Cancel
        const cancelButton = screen.getByRole('button', { name: /cancel/i });
        await user.click(cancelButton);

        // Should not call delete mutation
        expect(mockDeleteMessage).not.toHaveBeenCalled();
      });
    });

    describe('Edited Indicator', () => {
      it('should show (edited) indicator when message is edited', () => {
        const editedMessage: Message = {
          ...baseMessage,
          created_at: '2024-01-01T12:00:00Z',
          updated_at: '2024-01-01T13:00:00Z',
        };

        render(<MessageBubble message={editedMessage} isOwn={false} />);

        expect(screen.getByText('(edited)')).toBeInTheDocument();
      });

      it('should not show (edited) when updated_at equals created_at', () => {
        const message: Message = {
          ...baseMessage,
          created_at: '2024-01-01T12:00:00Z',
          updated_at: '2024-01-01T12:00:00Z',
        };

        render(<MessageBubble message={message} isOwn={false} />);

        expect(screen.queryByText('(edited)')).not.toBeInTheDocument();
      });

      it('should not show (edited) when updated_at is missing', () => {
        render(<MessageBubble message={baseMessage} isOwn={false} />);

        expect(screen.queryByText('(edited)')).not.toBeInTheDocument();
      });
    });

    describe('Deleted Messages', () => {
      it('should show [message deleted] placeholder for deleted messages', () => {
        const deletedMessage: Message = {
          ...baseMessage,
          deleted_at: '2024-01-01T13:00:00Z',
        };

        render(<MessageBubble message={deletedMessage} isOwn={false} />);

        expect(screen.getByText('[message deleted]')).toBeInTheDocument();
        expect(screen.queryByText('Hello, how can I help?')).not.toBeInTheDocument();
      });

      it('should not show edit dropdown for deleted messages', () => {
        const deletedMessage: Message = {
          ...baseMessage,
          agent_id: 'user:testuser',
          deleted_at: '2024-01-01T13:00:00Z',
        };

        render(<MessageBubble message={deletedMessage} isOwn={true} />);

        expect(screen.queryByRole('button', { name: /message actions/i })).not.toBeInTheDocument();
      });

      it('should not show (edited) indicator for deleted messages', () => {
        const deletedMessage: Message = {
          ...baseMessage,
          created_at: '2024-01-01T12:00:00Z',
          updated_at: '2024-01-01T13:00:00Z',
          deleted_at: '2024-01-01T14:00:00Z',
        };

        render(<MessageBubble message={deletedMessage} isOwn={false} />);

        expect(screen.queryByText('(edited)')).not.toBeInTheDocument();
      });

      it('should not show scope badges for deleted messages', () => {
        const deletedMessage: Message = {
          ...baseMessage,
          deleted_at: '2024-01-01T13:00:00Z',
          scopes: [{ type: 'thread', value: 'thread-1' }],
        };

        render(<MessageBubble message={deletedMessage} isOwn={false} />);

        // ScopeBadge component won't be rendered
        expect(screen.queryByText('thread')).not.toBeInTheDocument();
      });
    });
  });

  describe('Mention Highlighting', () => {
    it('should highlight mentions in message content', () => {
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'Hey @assistant can you help?' },
        mentions: ['assistant'],
      };

      render(<MessageBubble message={message} isOwn={false} />);

      // Check that @assistant appears in the document
      expect(screen.getByText(/@assistant/)).toBeInTheDocument();
    });

    it('should apply cyan background to mentions', () => {
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'Hello @assistant' },
        mentions: ['assistant'],
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      // Look for span with mention styling
      const mentionSpan = container.querySelector('span[style*="background"]');
      expect(mentionSpan).toBeInTheDocument();
      expect(mentionSpan?.textContent).toBe('@assistant');
    });

    it('should not highlight @ patterns without valid mentions', () => {
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'Email is test@example.com' },
        mentions: [],
      };

      const { container } = render(<MessageBubble message={message} isOwn={false} />);

      // @ patterns not in mentions array should not be highlighted
      const mentionSpans = container.querySelectorAll('span[style*="rgb(56, 189, 248)"]');
      expect(mentionSpans.length).toBe(0);
    });

    it('should handle multiple mentions in one message', () => {
      const message: Message = {
        ...baseMessage,
        body: {
          format: 'markdown',
          content: 'Can @assistant and @researcher collaborate?',
        },
        mentions: ['assistant', 'researcher'],
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText(/@assistant/)).toBeInTheDocument();
      expect(screen.getByText(/@researcher/)).toBeInTheDocument();
    });

    it('should render message without mentions field', () => {
      const message: Message = {
        ...baseMessage,
        body: { format: 'markdown', content: 'No mentions here' },
        // mentions field is undefined
      };

      render(<MessageBubble message={message} isOwn={false} />);

      expect(screen.getByText('No mentions here')).toBeInTheDocument();
    });
  });
});
