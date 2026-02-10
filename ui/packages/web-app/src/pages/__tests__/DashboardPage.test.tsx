import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@/test/test-utils';
import { DashboardPage } from '../DashboardPage';
import { selectLiveFeed, selectMyInbox, selectAgent, uiStore } from '@thrum/shared-logic';
import * as sharedLogic from '@thrum/shared-logic';
import { mockHookReturns } from '@/test/mocks';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    useMessageList: vi.fn(),
    useAgentList: vi.fn(),
    useThreadList: vi.fn(),
    useThread: vi.fn(),
    useSendMessage: vi.fn(),
    useCreateThread: vi.fn(),
    useMarkAsRead: vi.fn(),
  };
});

vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({
    user: {
      user_id: 'user:testuser',
      username: 'testuser',
      display_name: 'Test User',
      token: 'tok_test',
      status: 'registered',
    },
    isLoading: false,
    error: null,
  }),
}));

describe('DashboardPage', () => {
  beforeEach(() => {
    selectLiveFeed();
    vi.mocked(sharedLogic.useCurrentUser).mockReturnValue(mockHookReturns.useCurrentUser());
    vi.mocked(sharedLogic.useMessageList).mockReturnValue({
      data: { messages: [], page: 1, page_size: 50, total_messages: 0, total_pages: 1 },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useAgentList).mockReturnValue(mockHookReturns.useAgentList([]) as any);
    vi.mocked(sharedLogic.useThreadList).mockReturnValue(mockHookReturns.useThreadListEmpty() as any);
    vi.mocked(sharedLogic.useThread).mockReturnValue(mockHookReturns.useThreadEmpty() as any);
    vi.mocked(sharedLogic.useSendMessage).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useCreateThread).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMarkAsRead).mockReturnValue(mockHookReturns.useMutation() as any);
  });

  it('should render LiveFeed by default', () => {
    render(<DashboardPage />);
    const heading = screen.getByRole('heading', { name: 'Live Feed' });
    expect(heading).toBeInTheDocument();
  });

  it('should render My Inbox when selectedView is my-inbox', () => {
    selectMyInbox();
    render(<DashboardPage />);
    // InboxHeader renders the identity (username) as heading
    const heading = screen.getByRole('heading', { name: 'testuser' });
    expect(heading).toBeInTheDocument();
  });

  it('should render agent inbox when selectedView is agent-inbox with agentId', () => {
    selectAgent('agent:claude-daemon');
    render(<DashboardPage />);
    // InboxHeader renders the identityId as heading
    const heading = screen.getByRole('heading', {
      name: 'agent:claude-daemon',
    });
    expect(heading).toBeInTheDocument();
  });

  it('should not render agent inbox if selectedView is agent-inbox but no agentId', () => {
    uiStore.setState({
      selectedView: 'agent-inbox',
      selectedAgentId: null,
    });
    render(<DashboardPage />);
    expect(screen.queryByRole('heading', { name: /agent:/ })).not.toBeInTheDocument();
  });

  it('should switch views when store updates', () => {
    const { rerender } = render(<DashboardPage />);
    expect(
      screen.getByRole('heading', { name: 'Live Feed' })
    ).toBeInTheDocument();

    selectMyInbox();
    rerender(<DashboardPage />);
    expect(
      screen.getByRole('heading', { name: 'testuser' })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Live Feed' })
    ).not.toBeInTheDocument();
  });
});
