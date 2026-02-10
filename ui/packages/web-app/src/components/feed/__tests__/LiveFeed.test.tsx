import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '../../../test/test-utils';
import { LiveFeed } from '../LiveFeed';

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

// Mock the useFeed hook
vi.mock('../../../hooks/useFeed', () => ({
  useFeed: vi.fn(() => ({
    data: [
      {
        id: 'msg-1',
        type: 'message',
        from: 'agent:claude-daemon',
        to: 'agent:claude-cli',
        preview: 'Build completed successfully',
        timestamp: new Date('2024-01-01T11:58:00Z').toISOString(),
      },
      {
        id: 'msg-2',
        type: 'message',
        from: 'user:leon',
        to: 'agent:claude-daemon',
        preview: 'Can you check the logs?',
        timestamp: new Date('2024-01-01T11:50:00Z').toISOString(),
      },
    ],
    isLoading: false,
  })),
}));

describe('LiveFeed', () => {
  test('renders Live Feed header', () => {
    render(<LiveFeed />);

    expect(screen.getByText('Live Feed')).toBeInTheDocument();
  });

  test('renders all feed items', () => {
    render(<LiveFeed />);

    // Check for unique preview text
    expect(screen.getByText('Build completed successfully')).toBeInTheDocument();
    expect(screen.getByText('Can you check the logs?')).toBeInTheDocument();

    // Check that display names appear (may appear multiple times as from/to)
    expect(screen.getAllByText('Claude Daemon').length).toBeGreaterThan(0);
    expect(screen.getByText('Claude CLI')).toBeInTheDocument();
    expect(screen.getByText('user:leon')).toBeInTheDocument();
  });
});
