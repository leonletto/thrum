import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@/test/test-utils';
import userEvent from '@testing-library/user-event';
import { DashboardPage } from '../../pages/DashboardPage';
import { selectLiveFeed } from '@thrum/shared-logic';
import * as sharedLogic from '@thrum/shared-logic';
import { mockHookReturns } from '@/test/mocks';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    useMessageList: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
    useSendMessage: vi.fn(),
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

/**
 * Integration tests for navigation flow across the entire app.
 * Tests how Sidebar, DashboardPage, and views work together.
 */
describe('Navigation Integration', () => {
  beforeEach(() => {
    selectLiveFeed();
    vi.mocked(sharedLogic.useCurrentUser).mockReturnValue(mockHookReturns.useCurrentUser());
    vi.mocked(sharedLogic.useMessageList).mockReturnValue({
      data: { messages: [], page: 1, page_size: 50, total_messages: 0, total_pages: 1 },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useAgentList).mockReturnValue(mockHookReturns.useAgentList([
      {
        agent_id: 'agent:claude-daemon',
        kind: 'agent' as const,
        role: 'daemon',
        module: 'core',
        display: 'Claude Daemon',
        registered_at: '2024-01-01T00:00:00Z',
        last_seen_at: '2024-01-01T12:00:00Z',
      },
      {
        agent_id: 'agent:claude-cli',
        kind: 'agent' as const,
        role: 'cli',
        module: 'core',
        display: 'Claude CLI',
        registered_at: '2024-01-01T00:00:00Z',
        last_seen_at: '2024-01-01T11:50:00Z',
      },
    ]) as any);
    vi.mocked(sharedLogic.useGroupList).mockReturnValue({
      data: { groups: [] },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useSendMessage).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMarkAsRead).mockReturnValue(mockHookReturns.useMutation() as any);
  });

  it('should render complete app layout with all components', () => {
    render(<DashboardPage />);

    // Header
    expect(screen.getByText('Thrum')).toBeInTheDocument();

    // Sidebar sections
    const sidebar = screen.getByRole('complementary');
    expect(within(sidebar).getAllByText('Live Feed')[0]).toBeInTheDocument();
    expect(within(sidebar).getAllByText('My Inbox')[0]).toBeInTheDocument();

    // Content area - FeedView by default shows Activity Feed
    expect(
      screen.getByText(/activity feed/i)
    ).toBeInTheDocument();
  });

  it('should navigate from Live Feed to My Inbox', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Verify starting on Activity Feed (FeedView)
    expect(
      screen.getByText(/activity feed/i)
    ).toBeInTheDocument();

    // Click My Inbox in sidebar
    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    // Verify navigation to inbox (InboxHeader shows username)
    expect(
      screen.getByRole('heading', { name: 'testuser' })
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/activity feed/i)
    ).not.toBeInTheDocument();
  });

  it('should navigate from My Inbox back to Live Feed', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Navigate to My Inbox first
    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);
    expect(
      screen.getByRole('heading', { name: 'testuser' })
    ).toBeInTheDocument();

    // Navigate back to Live Feed
    const feedButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('Live Feed')
    );
    await user.click(feedButton!);

    expect(
      screen.getByText(/activity feed/i)
    ).toBeInTheDocument();
  });

  it('should navigate to agent inbox when clicking agent card', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Click first agent button (AgentCard uses agent-item class)
    const agentButtons = container.querySelectorAll('button.agent-item');
    await user.click(agentButtons[0] as HTMLElement);

    // Verify navigation to agent inbox (InboxHeader shows agent_id)
    expect(screen.getByRole('heading', { name: 'agent:claude-daemon' })).toBeInTheDocument();
  });

  it('should highlight active navigation item', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Get sidebar
    const sidebar = container.querySelector('aside');

    // Find sidebar nav-item buttons
    const sidebarButtons = sidebar?.querySelectorAll('button.nav-item');
    expect(sidebarButtons && sidebarButtons.length >= 2).toBe(true);

    // Click My Inbox button
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    // Now My Inbox button should have 'active' class
    expect(inboxButton?.className).toContain('active');
  });

  it('should show correct agent as selected in agent list', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Click on an agent
    const agentButtons = container.querySelectorAll('button.agent-item');
    const secondAgent = agentButtons[1] as HTMLElement;
    await user.click(secondAgent);

    // Verify agent has active styling (ring-2)
    expect(secondAgent.className).toContain('ring-2');
  });

  it('should clear agent selection when navigating to Live Feed', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // First, select an agent
    const agentButtons = container.querySelectorAll('button.agent-item');
    const firstAgent = agentButtons[0] as HTMLElement;
    await user.click(firstAgent);
    expect(screen.getByRole('heading', { name: 'agent:claude-daemon' })).toBeInTheDocument();

    // Agent should be active (has ring-2)
    expect(firstAgent.className).toContain('ring-2');

    // Navigate to Live Feed
    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const feedButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('Live Feed')
    );
    await user.click(feedButton!);

    // Agent should no longer have active styling
    expect(firstAgent.className).not.toContain('ring-2');
  });
});
