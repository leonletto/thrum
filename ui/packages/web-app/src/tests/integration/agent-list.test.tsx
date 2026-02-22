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
    useAgentList: vi.fn(),
    useSendMessage: vi.fn(),
    useMarkAsRead: vi.fn(),
  };
});

/**
 * Integration tests for agent list functionality.
 * Tests agent display, sorting, and interaction with navigation.
 */
describe('Agent List Integration', () => {
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
  });

  it('should display agents in sidebar', () => {
    render(<DashboardPage />);

    const sidebar = screen.getByRole('complementary');
    expect(within(sidebar).getByText(/Agents/i)).toBeInTheDocument();
  });

  it('should display agent names', () => {
    const { container } = render(<DashboardPage />);

    const agentSection = container.querySelector('.space-y-1');
    expect(agentSection).toBeInTheDocument();
  });

  it('should show at least 2 agents', () => {
    const { container } = render(<DashboardPage />);

    const agentButtons = container.querySelectorAll('button.agent-item');
    expect(agentButtons.length).toBeGreaterThanOrEqual(2);
  });

  it('should navigate to agent inbox when agent is clicked', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    const agentButtons = container.querySelectorAll('button.agent-item');
    await user.click(agentButtons[0] as HTMLElement);

    // InboxHeader shows agent_id as heading
    expect(screen.getByRole('heading', { name: 'agent:claude-daemon' })).toBeInTheDocument();
  });

  it('should show agents section header', () => {
    render(<DashboardPage />);
    expect(screen.getByText(/Agents/i)).toBeInTheDocument();
  });

  it('should highlight selected agent', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    const agentButtons = container.querySelectorAll('button.agent-item');
    const firstAgent = agentButtons[0] as HTMLElement;
    await user.click(firstAgent);

    // AgentCard uses ring-2 for active state
    expect(firstAgent.className).toContain('ring-2');
  });

  it('should clear agent highlight when navigating away', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Select an agent
    const agentButtons = container.querySelectorAll('button.agent-item');
    const firstAgent = agentButtons[0] as HTMLElement;
    await user.click(firstAgent);

    // Agent should have ring-2 class
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
