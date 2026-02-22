import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { GroupChannelView } from '../GroupChannelView';
import * as hooks from '@thrum/shared-logic';

// ─── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useMessageList: vi.fn(),
    useMessageListPaged: vi.fn(),
    useGroupInfo: vi.fn(),
    useCurrentUser: vi.fn(),
    useSendMessage: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
    useMarkAsRead: vi.fn(),
    groupByConversation: (msgs: unknown[]) =>
      msgs.map((m: unknown) => ({ rootMessage: m, replies: [] })),
    useDebounce: (fn: () => void, _delay: number) => fn,
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

const mockGroupInfo = {
  group_id: 'g-backend',
  name: 'backend',
  description: 'Backend team channel',
  created_at: '2026-01-01T00:00:00Z',
  created_by: 'user:leon',
  members: [
    {
      member_type: 'agent' as const,
      member_value: 'impl_1',
      added_at: '2026-01-01T00:00:00Z',
    },
    {
      member_type: 'role' as const,
      member_value: 'implementer',
      added_at: '2026-01-02T00:00:00Z',
      added_by: 'user:leon',
    },
    {
      member_type: 'agent' as const,
      member_value: 'reviewer_1',
      added_at: '2026-01-03T00:00:00Z',
    },
  ],
};

const mockEveryoneGroupInfo = {
  group_id: 'g-everyone',
  name: 'everyone',
  created_at: '2026-01-01T00:00:00Z',
  members: [
    {
      member_type: 'agent' as const,
      member_value: 'agent_a',
      added_at: '2026-01-01T00:00:00Z',
    },
  ],
};

// ─── Setup ────────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks();

  vi.mocked(hooks.useCurrentUser).mockReturnValue({
    user_id: 'user:leon',
    username: 'leon',
    display_name: 'Leon',
    token: 'tok',
    status: 'existing',
  });

  // useMessageList is used by GroupDeleteDialog (which is rendered inside GroupChannelView)
  vi.mocked(hooks.useMessageList).mockReturnValue({
    data: { messages: [], page: 1, page_size: 1, total: 0, total_pages: 0 },
    isLoading: false,
    error: null,
  } as any);

  vi.mocked(hooks.useMessageListPaged).mockReturnValue({
    messages: [],
    total: 0,
    isLoading: false,
    hasMore: false,
    loadMore: vi.fn(),
    isLoadingMore: false,
  } as any);

  vi.mocked(hooks.useGroupInfo).mockReturnValue({
    data: mockGroupInfo,
    isLoading: false,
    error: null,
  } as any);

  vi.mocked(hooks.useSendMessage).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as any);

  vi.mocked(hooks.useAgentList).mockReturnValue({
    data: { agents: [] },
    isLoading: false,
    error: null,
  } as any);

  vi.mocked(hooks.useGroupList).mockReturnValue({
    data: { groups: [] },
    isLoading: false,
    error: null,
  } as any);

  vi.mocked(hooks.useMarkAsRead).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as any);
});

// ─── Tests ────────────────────────────────────────────────────────────────────

describe('GroupChannelView', () => {
  // 1. Renders group header with # prefix and name
  it('renders group header with # prefix and group name', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    expect(screen.getByText('#backend')).toBeInTheDocument();
  });

  it('renders # prefix for a different group name', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="frontend" />, { wrapper: Wrapper });

    expect(screen.getByText('#frontend')).toBeInTheDocument();
  });

  // 2. Shows member count badge
  it('shows member count badge with correct count', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    const badge = screen.getByTestId('member-count-badge');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('3 members');
  });

  it('shows singular "member" for count of 1', () => {
    vi.mocked(hooks.useGroupInfo).mockReturnValue({
      data: mockEveryoneGroupInfo,
      isLoading: false,
      error: null,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="everyone" />, { wrapper: Wrapper });

    const badge = screen.getByTestId('member-count-badge');
    expect(badge).toHaveTextContent('1 member');
  });

  it('shows 0 members when groupInfo is not yet loaded', () => {
    vi.mocked(hooks.useGroupInfo).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    const badge = screen.getByTestId('member-count-badge');
    expect(badge).toHaveTextContent('0 members');
  });

  // 3. Fetches messages with correct scope filter
  it('calls useMessageListPaged with the correct group scope filter', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith({
      scope: { type: 'group', value: 'backend' },
      page_size: 50,
      sort_order: 'desc',
    });
  });

  it('uses the groupName prop in the scope filter', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="devops" />, { wrapper: Wrapper });

    expect(hooks.useMessageListPaged).toHaveBeenCalledWith({
      scope: { type: 'group', value: 'devops' },
      page_size: 50,
      sort_order: 'desc',
    });
  });

  // 4. ComposeBar has groupScope set
  it('renders ComposeBar with groupScope equal to groupName', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    // ComposeBar is rendered — when groupScope is set it hides the To field
    // The best signal is that the recipient dropdown trigger is absent
    expect(
      screen.queryByRole('button', { name: /select recipients/i })
    ).not.toBeInTheDocument();

    // The compose bar itself must be present
    expect(screen.getByTestId('compose-bar')).toBeInTheDocument();
  });

  // 5. Members panel opens on button click
  it('opens members panel when Members button is clicked', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    // Panel should not be visible initially
    expect(screen.queryByTestId('members-panel')).not.toBeInTheDocument();

    // Click the Members button
    await user.click(screen.getByRole('button', { name: /view members/i }));

    expect(screen.getByTestId('members-panel')).toBeInTheDocument();
  });

  it('closes members panel when close button is clicked', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    // Open the panel
    await user.click(screen.getByRole('button', { name: /view members/i }));
    expect(screen.getByTestId('members-panel')).toBeInTheDocument();

    // Close the panel
    await user.click(screen.getByRole('button', { name: /close members panel/i }));
    expect(screen.queryByTestId('members-panel')).not.toBeInTheDocument();
  });

  // 6. Members panel shows member list
  it('shows all members in the members panel', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /view members/i }));

    const memberItems = screen.getAllByTestId('member-item');
    expect(memberItems).toHaveLength(3);
  });

  it('displays member_value for each member in the panel', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /view members/i }));

    expect(screen.getByText('impl_1')).toBeInTheDocument();
    expect(screen.getByText('implementer')).toBeInTheDocument();
    expect(screen.getByText('reviewer_1')).toBeInTheDocument();
  });

  it('displays member_type labels in the panel', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /view members/i }));

    // Two agents and one role
    const agentLabels = screen.getAllByText('agent');
    expect(agentLabels.length).toBeGreaterThanOrEqual(2);

    const roleLabels = screen.getAllByText('role');
    expect(roleLabels.length).toBeGreaterThanOrEqual(1);
  });

  it('shows loading state in members panel when group info is not loaded', async () => {
    vi.mocked(hooks.useGroupInfo).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as any);

    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /view members/i }));

    expect(screen.getByText(/loading members/i)).toBeInTheDocument();
  });

  // Settings button
  it('opens settings panel when settings button is clicked', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    expect(screen.queryByTestId('settings-panel')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /group settings/i }));

    expect(screen.getByTestId('settings-panel')).toBeInTheDocument();
  });

  it('closes settings panel when close button is clicked', async () => {
    const user = userEvent.setup();
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /group settings/i }));
    expect(screen.getByTestId('settings-panel')).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: /close settings panel/i })
    );
    expect(screen.queryByTestId('settings-panel')).not.toBeInTheDocument();
  });

  // #everyone — no delete buttons
  it('does not show delete-related buttons for #everyone group', () => {
    vi.mocked(hooks.useGroupInfo).mockReturnValue({
      data: mockEveryoneGroupInfo,
      isLoading: false,
      error: null,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="everyone" />, { wrapper: Wrapper });

    expect(
      screen.queryByRole('button', { name: /delete/i })
    ).not.toBeInTheDocument();
  });

  // MessageList receives messages
  it('passes messages to MessageList', async () => {
    const mockMessages = [
      {
        message_id: 'msg-1',
        created_at: '2026-01-01T10:00:00Z',
        body: { format: 'text', content: 'Hello backend team' },
        agent_id: 'user:leon',
      },
    ];

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: mockMessages,
      total: 1,
      isLoading: false,
      hasMore: false,
      loadMore: vi.fn(),
      isLoadingMore: false,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await waitFor(() => {
      expect(screen.getByText('Hello backend team')).toBeInTheDocument();
    });
  });

  // ─── Pagination ─────────────────────────────────────────────────────────────

  it('renders Load More button when hasMore is true and there are messages', () => {
    const mockMessages = [
      {
        message_id: 'msg-1',
        created_at: '2026-01-01T10:00:00Z',
        body: { format: 'text', content: 'First message' },
        agent_id: 'user:leon',
      },
    ];

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: mockMessages,
      total: 200,
      isLoading: false,
      hasMore: true,
      loadMore: vi.fn(),
      isLoadingMore: false,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument();
  });

  it('calls loadMore when Load More button is clicked', async () => {
    const user = userEvent.setup();
    const loadMore = vi.fn();
    const mockMessages = [
      {
        message_id: 'msg-1',
        created_at: '2026-01-01T10:00:00Z',
        body: { format: 'text', content: 'First message' },
        agent_id: 'user:leon',
      },
    ];

    vi.mocked(hooks.useMessageListPaged).mockReturnValue({
      messages: mockMessages,
      total: 200,
      isLoading: false,
      hasMore: true,
      loadMore,
      isLoadingMore: false,
    } as any);

    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    await user.click(screen.getByRole('button', { name: /load more/i }));

    expect(loadMore).toHaveBeenCalledTimes(1);
  });

  it('does not render Load More button when hasMore is false', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    // Default mock has hasMore: false
    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();
  });

  // Header is always visible
  it('renders the header with Members and Settings buttons', () => {
    const { Wrapper } = makeWrapper();
    render(<GroupChannelView groupName="backend" />, { wrapper: Wrapper });

    expect(
      screen.getByRole('button', { name: /view members/i })
    ).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /group settings/i })
    ).toBeInTheDocument();
  });
});
