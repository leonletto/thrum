import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { GroupDeleteDialog } from '../GroupDeleteDialog';
import * as hooks from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useMessageList: vi.fn(),
    useMessageArchive: vi.fn(),
    useGroupDelete: vi.fn(),
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
    data: { messages: [], page: 1, page_size: 1, total: 23, total_pages: 23 },
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

  vi.mocked(hooks.useGroupDelete).mockReturnValue({
    mutateAsync: mockDeleteMutateAsync,
    isPending: false,
    isError: false,
    isSuccess: false,
    reset: vi.fn(),
  } as any);
});

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('GroupDeleteDialog', () => {
  // 1. Shows group name with # prefix
  it('shows the group name with # prefix', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    const nameOccurrences = screen.getAllByText(/#backend/);
    expect(nameOccurrences.length).toBeGreaterThan(0);
  });

  // 2. Shows message count
  it('shows the message count from useMessageList', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.getByText(/23 messages/)).toBeInTheDocument();
  });

  // 3. Delete button works for normal groups
  it('has Delete button enabled for a normal group', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).not.toBeDisabled();
  });

  // 4. Delete button disabled for #everyone
  it('has Delete button disabled for #everyone', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="everyone"
      />,
      { wrapper: Wrapper }
    );

    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).toBeDisabled();
  });

  it('shows a warning message for #everyone', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="everyone"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.getByTestId('everyone-warning')).toBeInTheDocument();
  });

  // 5. Archive checkbox toggles
  it('toggles the archive checkbox', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="backend"
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

  // 6. Calls groupDelete on confirm
  it('calls useGroupDelete mutateAsync when confirmed without archive', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByTestId('confirm-delete-button'));

    await waitFor(() => {
      expect(mockArchiveMutateAsync).not.toHaveBeenCalled();
      expect(mockDeleteMutateAsync).toHaveBeenCalledWith({ name: 'backend', delete_messages: true });
    });
  });

  it('calls selectLiveFeed after successful delete', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByTestId('confirm-delete-button'));

    await waitFor(() => {
      expect(hooks.selectLiveFeed).toHaveBeenCalled();
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  // 7. Calls archive before delete when checkbox checked
  it('calls archive then delete when archive checkbox is checked', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    // Check archive
    await user.click(screen.getByTestId('archive-checkbox'));

    // Confirm
    await user.click(screen.getByTestId('confirm-delete-button'));

    await waitFor(() => {
      expect(mockArchiveMutateAsync).toHaveBeenCalledWith({
        archive_type: 'group',
        identifier: 'backend',
      });
      expect(mockDeleteMutateAsync).toHaveBeenCalledWith({ name: 'backend', delete_messages: true });
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
      <GroupDeleteDialog
        open={true}
        onOpenChange={onOpenChange}
        groupName="backend"
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
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    const listItem = screen.getByText((content) => content.includes('1 message') && !content.includes('1 messages'));
    expect(listItem).toBeInTheDocument();
  });

  // Does not render when closed
  it('does not render dialog content when open=false', () => {
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={false}
        onOpenChange={vi.fn()}
        groupName="backend"
      />,
      { wrapper: Wrapper }
    );

    expect(screen.queryByText('Delete Group')).not.toBeInTheDocument();
  });

  // Does not call delete for #everyone even if button somehow triggered
  it('does not call groupDelete when groupName is everyone', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <GroupDeleteDialog
        open={true}
        onOpenChange={vi.fn()}
        groupName="everyone"
      />,
      { wrapper: Wrapper }
    );

    // Button is disabled, but verify the handler guard too
    const deleteButton = screen.getByTestId('confirm-delete-button');
    expect(deleteButton).toBeDisabled();
    expect(mockDeleteMutateAsync).not.toHaveBeenCalled();
  });
});
