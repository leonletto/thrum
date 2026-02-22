import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '../../test/test-utils';
import { userEvent } from '@testing-library/user-event';
import { Sidebar } from '../Sidebar';
import { selectGroup } from '@thrum/shared-logic';

// Mock shared-logic hooks and actions
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentList: () => ({
      data: {
        agents: [
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
        ],
      },
      isLoading: false,
      error: null,
    }),
    useGroupList: () => ({
      data: {
        groups: [
          {
            group_id: 'g1',
            name: 'everyone',
            member_count: 5,
            created_at: '2024-01-01T00:00:00Z',
          },
          {
            group_id: 'g2',
            name: 'backend',
            member_count: 2,
            created_at: '2024-01-02T00:00:00Z',
          },
          {
            group_id: 'g3',
            name: 'reviewers',
            member_count: 3,
            created_at: '2024-01-03T00:00:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
    selectGroup: vi.fn((...args) => (actual as any).selectGroup(...args)),
  };
});

describe('Sidebar', () => {
  test('renders Live Feed navigation item', () => {
    render(<Sidebar />);

    expect(screen.getByRole('button', { name: /live feed/i })).toBeInTheDocument();
  });

  test('renders My Inbox navigation item', () => {
    render(<Sidebar />);

    expect(screen.getByRole('button', { name: /my inbox/i })).toBeInTheDocument();
  });

  test('renders Agent List section', () => {
    render(<Sidebar />);

    // Check for agent list header with count
    expect(screen.getByText(/agents \(2\)/i)).toBeInTheDocument();
  });

  test('Live Feed is selected by default', () => {
    render(<Sidebar />);

    const liveFeedButton = screen.getByRole('button', { name: /live feed/i });
    expect(liveFeedButton).toHaveClass('active');
  });

  test('clicking My Inbox changes selection', async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    const myInboxButton = screen.getByRole('button', { name: /my inbox/i });
    await user.click(myInboxButton);

    expect(myInboxButton).toHaveClass('active');
    expect(screen.getByRole('button', { name: /live feed/i })).not.toHaveClass('active');
  });

  test('renders Groups section header', () => {
    render(<Sidebar />);

    expect(screen.getByText(/groups/i)).toBeInTheDocument();
  });

  test('renders group items with # prefix', () => {
    render(<Sidebar />);

    expect(screen.getByText('# everyone')).toBeInTheDocument();
    expect(screen.getByText('# backend')).toBeInTheDocument();
    expect(screen.getByText('# reviewers')).toBeInTheDocument();
  });

  test('#everyone appears first in the groups list', () => {
    render(<Sidebar />);

    const groupButtons = screen.getAllByRole('button').filter((btn) =>
      btn.textContent?.includes('# ')
    );

    expect(groupButtons[0].textContent).toContain('everyone');
  });

  test('clicking a group calls selectGroup with group name', async () => {
    const user = userEvent.setup();
    render(<Sidebar />);

    const everyoneButton = screen.getByText('# everyone').closest('button')!;
    await user.click(everyoneButton);

    expect(selectGroup).toHaveBeenCalledWith('everyone');
  });

  test('renders all five sections: Feed, Your Inbox, Groups, Agents, Tools', () => {
    render(<Sidebar />);

    // Feed item
    expect(screen.getByRole('button', { name: /live feed/i })).toBeInTheDocument();

    // Your Inbox section header and item
    expect(screen.getByText(/your inbox/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /my inbox/i })).toBeInTheDocument();

    // Groups section header and items
    expect(screen.getByText(/groups/i)).toBeInTheDocument();
    expect(screen.getByText('# everyone')).toBeInTheDocument();

    // Agents section header and agents
    expect(screen.getByText(/agents \(2\)/i)).toBeInTheDocument();

    // Tools section header and items
    expect(screen.getByText(/tools/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /who has/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /settings/i })).toBeInTheDocument();
  });
});
