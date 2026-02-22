import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@/test/test-utils';
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
    useAgentList: vi.fn(),
    useSendMessage: vi.fn(),
    useMarkAsRead: vi.fn(),
    useMessageListPaged: vi.fn(),
    useGroupList: vi.fn(),
  };
});

/**
 * Integration tests for Inbox views.
 * Tests My Inbox and Agent Inbox functionality.
 */
describe('Inbox View Integration', () => {
  beforeEach(() => {
    selectLiveFeed();
    vi.mocked(sharedLogic.useCurrentUser).mockReturnValue(mockHookReturns.useCurrentUser());
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
    vi.mocked(sharedLogic.useSendMessage).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMarkAsRead).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMessageListPaged).mockReturnValue({
      messages: [],
      total: 0,
      isLoading: false,
      hasMore: false,
      loadMore: vi.fn(),
      isLoadingMore: false,
    } as any);
    vi.mocked(sharedLogic.useGroupList).mockReturnValue({
      data: { groups: [] },
      isLoading: false,
      error: null,
    } as any);
  });

  it('should show My Inbox when clicking My Inbox in sidebar', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    // InboxHeader renders the username as heading
    expect(
      screen.getByRole('heading', { name: 'testuser' })
    ).toBeInTheDocument();
  });

  it('should show empty state in My Inbox', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    // MessageList empty state shows "No messages"
    expect(screen.getByText('No messages')).toBeInTheDocument();
  });

  it('should show agent-specific inbox when clicking agent', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Click on agent card
    const agentButton = container.querySelector('button.agent-item');
    await user.click(agentButton!);

    // InboxHeader renders identityId (agent_id) as heading
    expect(
      screen.getByRole('heading', { name: 'agent:claude-daemon' })
    ).toBeInTheDocument();
  });

  it('should show empty state in agent inbox', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Navigate to agent inbox
    const agentButton = container.querySelector('button.agent-item');
    await user.click(agentButton!);

    // MessageList empty state shows "No messages"
    expect(screen.getByText('No messages')).toBeInTheDocument();
  });

  it('should switch between different agent inboxes', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Click first agent
    const agentButtons = container.querySelectorAll('button.agent-item');
    await user.click(agentButtons[0] as HTMLElement);
    expect(
      screen.getByRole('heading', { name: 'agent:claude-daemon' })
    ).toBeInTheDocument();

    // Click second agent
    await user.click(agentButtons[1] as HTMLElement);
    expect(
      screen.getByRole('heading', { name: 'agent:claude-cli' })
    ).toBeInTheDocument();
  });

  it('should navigate from agent inbox to My Inbox', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Start at agent inbox
    const agentButton = container.querySelector('button.agent-item');
    await user.click(agentButton!);

    // Navigate to My Inbox
    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    expect(
      screen.getByRole('heading', { name: 'testuser' })
    ).toBeInTheDocument();
  });
});
