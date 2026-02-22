import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CreateGroupDialog } from '../CreateGroupDialog';
import * as hooks from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useGroupCreate: vi.fn(),
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

let mockMutate: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();

  mockMutate = vi.fn();

  vi.mocked(hooks.useGroupCreate).mockReturnValue({
    mutate: mockMutate,
    isPending: false,
    isError: false,
    isSuccess: false,
    reset: vi.fn(),
  } as any);
});

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('CreateGroupDialog', () => {
  // 1. Renders when open
  it('renders dialog content when open=true', () => {
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    expect(screen.getByText('Create Group')).toBeInTheDocument();
    expect(screen.getByTestId('group-name-input')).toBeInTheDocument();
    expect(screen.getByTestId('group-description-input')).toBeInTheDocument();
    expect(screen.getByTestId('create-group-submit')).toBeInTheDocument();
  });

  it('does not render dialog content when open=false', () => {
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={false} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    expect(screen.queryByText('Create Group')).not.toBeInTheDocument();
  });

  // 2. Submits with name and description
  it('calls mutate with name and description when both are provided', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('group-name-input'), 'backend');
    await user.type(screen.getByTestId('group-description-input'), 'Backend team');
    await user.click(screen.getByTestId('create-group-submit'));

    expect(mockMutate).toHaveBeenCalledWith(
      { name: 'backend', description: 'Backend team' },
      expect.any(Object)
    );
  });

  it('calls mutate with only name when description is empty', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('group-name-input'), 'frontend');
    await user.click(screen.getByTestId('create-group-submit'));

    expect(mockMutate).toHaveBeenCalledWith(
      { name: 'frontend', description: undefined },
      expect.any(Object)
    );
  });

  // 3. Closes on successful creation
  it('calls onOpenChange(false) on success callback', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();

    // Simulate mutate calling onSuccess immediately
    mockMutate.mockImplementation((_params: unknown, callbacks: { onSuccess?: () => void }) => {
      callbacks?.onSuccess?.();
    });

    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={onOpenChange} />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('group-name-input'), 'ops');
    await user.click(screen.getByTestId('create-group-submit'));

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  // 4. Doesn't submit with empty name
  it('does not call mutate when name is empty', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByTestId('create-group-submit'));

    expect(mockMutate).not.toHaveBeenCalled();
  });

  it('shows error message when name is empty on submit', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByTestId('create-group-submit'));

    expect(screen.getByTestId('create-group-error')).toHaveTextContent(
      'Name is required'
    );
  });

  it('does not call mutate when name is only whitespace', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={vi.fn()} />,
      { wrapper: Wrapper }
    );

    await user.type(screen.getByTestId('group-name-input'), '   ');
    await user.click(screen.getByTestId('create-group-submit'));

    expect(mockMutate).not.toHaveBeenCalled();
  });

  // Cancel button
  it('calls onOpenChange(false) when Cancel is clicked', async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    const { Wrapper } = makeWrapper();
    render(
      <CreateGroupDialog open={true} onOpenChange={onOpenChange} />,
      { wrapper: Wrapper }
    );

    await user.click(screen.getByRole('button', { name: /cancel/i }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
