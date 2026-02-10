import { useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MentionAutocomplete } from '../MentionAutocomplete';
import * as hooks from '@thrum/shared-logic';

// Mock the shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentList: vi.fn(),
  };
});

// Stateful wrapper so the controlled component works with userEvent.type()
function StatefulMentionAutocomplete({
  onChangeSpy,
  initialValue = '',
  ...props
}: Omit<React.ComponentProps<typeof MentionAutocomplete>, 'value' | 'onChange'> & {
  onChangeSpy: (value: string, mentions: string[]) => void;
  initialValue?: string;
}) {
  const [value, setValue] = useState(initialValue);
  const handleChange = (newValue: string, mentions: string[]) => {
    setValue(newValue);
    onChangeSpy(newValue, mentions);
  };
  return <MentionAutocomplete {...props} value={value} onChange={handleChange} />;
}

describe('MentionAutocomplete', () => {
  let queryClient: QueryClient;
  const mockOnChange = vi.fn();

  const mockAgents = [
    {
      agent_id: 'agent:assistant:ABC123',
      kind: 'agent' as const,
      role: 'assistant',
      module: 'core',
      display: 'Assistant Bot',
      registered_at: '2024-01-01T00:00:00Z',
    },
    {
      agent_id: 'agent:researcher:XYZ789',
      kind: 'agent' as const,
      role: 'researcher',
      module: 'research',
      display: 'Research Agent',
      registered_at: '2024-01-01T00:00:00Z',
    },
    {
      agent_id: 'agent:tester:TEST456',
      kind: 'agent' as const,
      role: 'tester',
      module: 'qa',
      display: 'Testing Agent',
      registered_at: '2024-01-01T00:00:00Z',
    },
  ];

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });

    vi.mocked(hooks.useAgentList).mockReturnValue({
      data: { agents: mockAgents },
      isLoading: false,
      error: null,
    } as any);

    mockOnChange.mockClear();
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Basic Rendering', () => {
    it('should render textarea', () => {
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} placeholder="Type here..." />
      );

      expect(screen.getByPlaceholderText('Type here...')).toBeInTheDocument();
    });

    it('should display provided value', () => {
      renderWithProvider(
        <MentionAutocomplete value="Hello world" onChange={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
      expect(textarea.value).toBe('Hello world');
    });
  });

  describe('Mention Autocomplete', () => {
    it('should show dropdown when @ is typed', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });
    });

    it('should filter agents based on search', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@res');

      await waitFor(() => {
        expect(screen.getByText('@researcher')).toBeInTheDocument();
        expect(screen.queryByText('@assistant')).not.toBeInTheDocument();
        expect(screen.queryByText('@tester')).not.toBeInTheDocument();
      });
    });

    it('should insert mention when agent is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@ass');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });

      const assistantOption = screen.getByText('@assistant');
      await user.click(assistantOption);

      await waitFor(() => {
        expect(mockOnChange).toHaveBeenCalledWith('@assistant ', ['assistant']);
      });
    });

    it('should not show dropdown without @ symbol', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'hello world');

      expect(screen.queryByText('@assistant')).not.toBeInTheDocument();
    });

    it('should hide dropdown when @ is preceded by non-whitespace', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'email@');

      expect(screen.queryByText('@assistant')).not.toBeInTheDocument();
    });

    it('should show dropdown when @ is at start of line', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });
    });

    it('should show dropdown when @ follows whitespace', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'Hello @');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });
    });
  });

  describe('Keyboard Navigation', () => {
    it('should navigate dropdown with arrow keys', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });

      // Arrow down should highlight next item
      await user.keyboard('{ArrowDown}');
      const researcherButton = screen.getByText('@researcher').closest('button');
      expect(researcherButton).toHaveClass('bg-accent');

      // Arrow up should go back
      await user.keyboard('{ArrowUp}');
      const assistantButton = screen.getByText('@assistant').closest('button');
      expect(assistantButton).toHaveClass('bg-accent');
    });

    it('should insert mention on Enter key', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });

      await user.keyboard('{Enter}');

      await waitFor(() => {
        expect(mockOnChange).toHaveBeenCalledWith('@assistant ', ['assistant']);
      });
    });

    it('should close dropdown on Escape key', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      await waitFor(() => {
        expect(screen.getByText('@assistant')).toBeInTheDocument();
      });

      await user.keyboard('{Escape}');

      await waitFor(() => {
        expect(screen.queryByText('@assistant')).not.toBeInTheDocument();
      });
    });
  });

  describe('Mention Extraction', () => {
    it('should extract mentions from text', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'Hello @assistant and @researcher');

      await waitFor(() => {
        expect(mockOnChange).toHaveBeenLastCalledWith(
          'Hello @assistant and @researcher',
          ['assistant', 'researcher']
        );
      });
    });

    it('should handle multiple mentions', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'Hey @assistant, can you ask @researcher about @tester?');

      await waitFor(() => {
        expect(mockOnChange).toHaveBeenLastCalledWith(
          'Hey @assistant, can you ask @researcher about @tester?',
          ['assistant', 'researcher', 'tester']
        );
      });
    });

    it('should extract mentions even without using dropdown', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      // Type full mention without selecting from dropdown
      await user.type(textarea, '@assistant hello');

      await waitFor(() => {
        expect(mockOnChange).toHaveBeenLastCalledWith('@assistant hello', ['assistant']);
      });
    });
  });

  describe('Edge Cases', () => {
    it('should handle empty agent list', async () => {
      const user = userEvent.setup();
      vi.mocked(hooks.useAgentList).mockReturnValue({
        data: { agents: [] },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, '@');

      // Dropdown should not appear
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('should handle disabled state', () => {
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} disabled />
      );

      const textarea = screen.getByRole('textbox');
      expect(textarea).toBeDisabled();
    });

    it('should extract @ patterns from email addresses', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <StatefulMentionAutocomplete onChangeSpy={mockOnChange} />
      );

      const textarea = screen.getByRole('textbox');
      await user.type(textarea, 'Email is user@example.com');

      // Note: Will extract 'example' as it matches @word pattern
      // This is expected behavior - UI would filter based on valid agent list
      await waitFor(() => {
        expect(mockOnChange).toHaveBeenLastCalledWith('Email is user@example.com', ['example']);
      });
    });
  });
});
