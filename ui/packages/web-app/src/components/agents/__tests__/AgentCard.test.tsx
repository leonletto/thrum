import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { AgentCard } from '../AgentCard';
import type { Agent } from '@thrum/shared-logic';

// Mock shared-logic to control useCurrentUser
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: () => ({ user_id: 'user:test', username: 'test', token: 'tok', status: 'existing' }),
    ensureConnected: vi.fn().mockResolvedValue(undefined),
    wsClient: { call: vi.fn() },
  };
});

// Mock @tanstack/react-query useQuery to intercept unread-count queries for agents
vi.mock('@tanstack/react-query', async () => {
  const actual = await vi.importActual('@tanstack/react-query');
  return {
    ...actual,
    useQuery: (options: any) => {
      // Detect agent unread queries by their query key shape
      const key = options?.queryKey;
      if (
        Array.isArray(key) &&
        key[0] === 'messages' &&
        key[1] === 'list' &&
        key[2]?.for_agent !== undefined
      ) {
        const agentId = key[2].for_agent as string;
        // Return 5 unread for agent:claude-daemon, 0 for others
        const total = agentId === 'agent:claude-daemon' ? 5 : 0;
        return { data: { messages: [], page: 1, page_size: 1, total, total_pages: 1 }, isLoading: false };
      }
      return (actual as any).useQuery(options);
    },
  };
});

describe('AgentCard', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2024-01-01T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const mockAgent: Agent = {
    agent_id: 'agent:claude-daemon',
    kind: 'agent' as const,
    role: 'daemon',
    module: 'core',
    display: 'Claude Daemon',
    registered_at: '2024-01-01T00:00:00Z',
    last_seen_at: new Date('2024-01-01T11:58:00Z').toISOString(),
  };

  test('renders agent display name', () => {
    render(<AgentCard agent={mockAgent} active={false} onClick={() => {}} />);

    expect(screen.getByText('Claude Daemon')).toBeInTheDocument();
  });

  test('renders agent_id when display is not provided', () => {
    const agentWithoutDisplay = { ...mockAgent, display: undefined };
    render(<AgentCard agent={agentWithoutDisplay} active={false} onClick={() => {}} />);

    expect(screen.getByText('agent:claude-daemon')).toBeInTheDocument();
  });

  test('shows unread badge when there are unread messages', () => {
    render(<AgentCard agent={mockAgent} active={false} onClick={() => {}} />);

    // agent:claude-daemon has 5 unread messages in the mock
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  test('does not show unread badge when unread count is zero', () => {
    const agentWithNoUnread = { ...mockAgent, agent_id: 'agent:claude-cli', display: 'Claude CLI' };
    render(<AgentCard agent={agentWithNoUnread} active={false} onClick={() => {}} />);

    // agent:claude-cli has 0 unread messages in the mock
    expect(screen.queryByText('0')).not.toBeInTheDocument();
  });

  test('does not render relative time (removed from UI)', () => {
    const { container } = render(<AgentCard agent={mockAgent} active={false} onClick={() => {}} />);

    // Time display was removed in the redesign
    expect(screen.queryByText(/ago/i)).not.toBeInTheDocument();
    // Suppress unused variable warning
    void container;
  });

  test('renders status indicator', () => {
    const { container } = render(
      <AgentCard agent={mockAgent} active={false} onClick={() => {}} />
    );

    const statusIndicator = container.querySelector('.status-indicator');
    expect(statusIndicator).toBeInTheDocument();
  });

  test('applies active styling when active is true', () => {
    render(<AgentCard agent={mockAgent} active={true} onClick={() => {}} />);

    const button = screen.getByRole('button');
    expect(button).toHaveClass('ring-2', 'ring-[var(--accent-color)]');
  });

  test('calls onClick when clicked', async () => {
    vi.useRealTimers(); // Use real timers for userEvent
    const user = userEvent.setup();
    const handleClick = vi.fn();

    render(<AgentCard agent={mockAgent} active={false} onClick={handleClick} />);

    await user.click(screen.getByRole('button'));
    expect(handleClick).toHaveBeenCalledTimes(1);
  });
});
