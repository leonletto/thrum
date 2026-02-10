import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '../../test/test-utils';
import { userEvent } from '@testing-library/user-event';
import { Sidebar } from '../Sidebar';

// Mock useAgentList hook
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
});
