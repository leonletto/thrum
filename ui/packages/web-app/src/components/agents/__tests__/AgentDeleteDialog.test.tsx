import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AgentDeleteDialog } from '../AgentDeleteDialog';
import * as hooks from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useMessageList: vi.fn(),
    useMessageArchive: vi.fn(),
    useAgentDelete: vi.fn(),
    selectLiveFeed: vi.fn(),
  };
});

// ─── Helpers ──────────────────────────────────────────────────────────────────

function makeWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  const Wrapper = ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { queryClient, Wrapper };
}

// ─── Setup ────────────────────────────────────────────────────────────────────

let mockDeleteMutateAsync: ReturnType<typeof vi.fn>;
let mockArchiveMutateAsync: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();

  mockDeleteMutateAsync = vi.fn().mockResolvedValue({});
  mockArchiveMutateAsync = vi.fn().mockResolvedValue({ archived_count: 5, archive_path: '/tmp/archive' });

  vi.mocked(hooks.useMessageList).mockReturnValue({
    data: { messages: [], page: 1, page_size: 1, total: 47, total_pages: 47 },
    isLoading: false,
    error: null,
  } as any);

  vi.mocked(hooks.useMessageArchive).mockReturnValue({
    mutateAsync: mockArchiveMutateAsync,
    isPending: false,
    isError: false,
    isSuccess: false,
    reset: vi.fn(),
  } as any);

  vi.mocked(hooks.useAgentDelete).mockReturnValue({
    mutateAsync: mockDeleteMutateAsync,
    isPending: false,
    isError: false,
    isSuccess: false,
    reset: vi.fn(),
  } as any);
});

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('AgentDeleteDialog', () => {
  // 1. Shows agent name
  it('shows the agent name prominently', () => {
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    // Agent name appears in the description and in the confirm label
    const nameOccurrences = screen.getAllByText(/impl_1/);
    expect(nameOccurrences.length).toBeGreaterThan(0);
  });

  // 2. Shows message count
  it('shows the message count from useMessageList', () => {
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.getByText(/47 messages/)).toBeInTheDocument();
  });

  // 3. Delete button disabled until name typed correctly
  it('has Delete button disabled when confirm text does not match', () => {
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).toBeDisabled();
  });

  it('has Delete button disabled when partial name is typed', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('confirm-input'), 'impl');

    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).toBeDisabled();
  });

  // 4. Delete button enabled when name matches
  it('enables the Delete button when the agent name is typed exactly', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('confirm-input'), 'impl_1');

    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).not.toBeDisabled();
  });

  // 5. Archive checkbox toggles
  it('toggles the archive checkbox', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    const checkbox = screen.getByTestId('archive-checkbox');
    // Initially unchecked
    expect(checkbox).toHaveAttribute('data-state', 'unchecked');

    await user.click(checkbox);
    expect(checkbox).toHaveAttribute('data-state', 'checked');

    await user.click(checkbox);
    expect(checkbox).toHaveAttribute('data-state', 'unchecked');
  });

  // 6. Calls delete on confirm (without archive)
  it('calls useAgentDelete mutateAsync when confirmed without archive', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('confirm-input'), 'impl_1');
    await user.click(screen.getByTestId('confirm-delete-button'));

    await waitFor(() => {
      expect(mockArchiveMutateAsync).not.toHaveBeenCalled();
      expect(mockDeleteMutateAsync).toHaveBeenCalledWith('impl_1');
    });
  });

  // Archive + delete flow
  it('calls archive then delete when archive checkbox is checked', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    // Check archive
    await user.click(screen.getByTestId('archive-checkbox'));

    // Type name
    await user.type(screen.getByTestId('confirm-input'), 'impl_1');

    // Confirm
    await user.click(screen.getByTestId('confirm-delete-button'));

    await waitFor(() => {
      expect(mockArchiveMutateAsync).toHaveBeenCalledWith({
        archive_type: 'agent',
        identifier: 'agent:impl:impl_1',
      });
      expect(mockDeleteMutateAsync).toHaveBeenCalledWith('impl_1');
    });

    // Archive must be called before delete
    const archiveOrder = mockArchiveMutateAsync.mock.invocationCallOrder[0];
    const deleteOrder = mockDeleteMutateAsync.mock.invocationCallOrder[0];
    expect(archiveOrder).toBeLessThan(deleteOrder);
  });

  // Cancel closes without deleting
  it('calls onOpenChange(false) when Cancel is clicked', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(mockDeleteMutateAsync).not.toHaveBeenCalled();
  });

  // Singular message count
  it('shows singular "message" when count is 1', () => {
    vi.mocked(hooks.useMessageList).mockReturnValue({
      data: { messages: [], page: 1, page_size: 1, total: 1, total_pages: 1 },
      isLoading: false,
      error: null,
    } as any);

    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    // When count is 1, should show singular "message" (not "messages")
    const listItem = screen.getByText((content) => content.includes('1 message') && !content.includes('1 messages'));
    expect(listItem).toBeInTheDocument();
  });

  // 0 messages
  it('shows 0 messages when message count is 0', () => {
    vi.mocked(hooks.useMessageList).mockReturnValue({
      data: { messages: [], page: 1, page_size: 1, total: 0, total_pages: 0 },
      isLoading: false,
      error: null,
    } as any);

    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.getByText(/0 messages/)).toBeInTheDocument();
  });

  // Does not render when closed
  it('does not render dialog content when open=false', () => {
    const { Wrapper } = makeWrapper();
    render(
      <AgentDeleteDialog
        open={false}
        onOpenChange={vi.fn()}
        agentName="impl_1"
        agentId="agent:impl:impl_1"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.queryByText('Delete Agent')).not.toBeInTheDocument();
  });
});
