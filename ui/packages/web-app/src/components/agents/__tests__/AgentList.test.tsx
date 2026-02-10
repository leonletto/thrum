import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { AgentList } from '../../AgentList';

// Mock the useAgentList hook from shared-logic
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
            last_seen_at: new Date('2024-01-01T11:58:00Z').toISOString(),
          },
          {
            agent_id: 'agent:claude-cli',
            kind: 'agent' as const,
            role: 'cli',
            module: 'core',
            display: 'Claude CLI',
            registered_at: '2024-01-01T00:00:00Z',
            last_seen_at: new Date('2024-01-01T11:50:00Z').toISOString(),
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
  };
});

describe('AgentList', () => {
  test('renders agents section header', () => {
    render(<AgentList />);

    expect(screen.getByText(/agents \(2\)/i)).toBeInTheDocument();
  });

  test('renders all agent cards with display names', () => {
    render(<AgentList />);

    expect(screen.getByText('Claude Daemon')).toBeInTheDocument();
    expect(screen.getByText('Claude CLI')).toBeInTheDocument();
  });

  test('sorts agents by last_seen_at (most recent first)', () => {
    render(<AgentList />);

    const agentButtons = screen.getAllByRole('button');
    const firstAgent = agentButtons[0];
    const secondAgent = agentButtons[1];

    expect(firstAgent).toHaveTextContent('Claude Daemon');
    expect(secondAgent).toHaveTextContent('Claude CLI');
  });
});
