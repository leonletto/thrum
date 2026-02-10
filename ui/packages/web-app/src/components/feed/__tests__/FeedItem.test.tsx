import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '../../../test/test-utils';
import { userEvent } from '@testing-library/user-event';
import { FeedItem } from '../FeedItem';
import type { FeedItem as FeedItemType } from '../../../types/feed';

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
            last_seen_at: '2024-01-01T12:00:00Z',
          },
        ],
      },
      isLoading: false,
      error: null,
    }),
  };
});

describe('FeedItem', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2024-01-01T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  const mockMessageItem: FeedItemType = {
    id: 'msg-1',
    type: 'message',
    from: 'agent:claude-daemon',
    to: 'agent:claude-cli',
    preview: 'This is a test message',
    timestamp: new Date('2024-01-01T11:58:00Z').toISOString(),
  };

  test('renders message item with from and to using display names', () => {
    render(<FeedItem item={mockMessageItem} onClick={() => {}} />);

    // Should show display names instead of agent IDs
    expect(screen.getByText('Claude Daemon')).toBeInTheDocument();
    expect(screen.getByText('Claude CLI')).toBeInTheDocument();
    expect(screen.getByText('→')).toBeInTheDocument();
  });

  test('renders preview text', () => {
    render(<FeedItem item={mockMessageItem} onClick={() => {}} />);

    expect(screen.getByText('This is a test message')).toBeInTheDocument();
  });

  test('renders relative timestamp', () => {
    render(<FeedItem item={mockMessageItem} onClick={() => {}} />);

    expect(screen.getByText(/2m ago/i)).toBeInTheDocument();
  });

  test('calls onClick when clicked', async () => {
    vi.useRealTimers();
    const user = userEvent.setup();
    const handleClick = vi.fn();

    render(<FeedItem item={mockMessageItem} onClick={handleClick} />);

    await user.click(screen.getByRole('button'));
    expect(handleClick).toHaveBeenCalledTimes(1);
  });

  test('does not render preview when not provided', () => {
    const itemNoPreview = { ...mockMessageItem, preview: undefined };
    render(<FeedItem item={itemNoPreview} onClick={() => {}} />);

    expect(screen.queryByText('This is a test message')).not.toBeInTheDocument();
  });
});
