import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { AgentCard } from '../AgentCard';
import type { Agent } from '@thrum/shared-logic';

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

  test('unread count no longer shown (removed from new Agent type)', () => {
    const { container } = render(<AgentCard agent={mockAgent} active={false} onClick={() => {}} />);

    const unreadBadge = container.querySelector('.unread-badge');
    expect(unreadBadge).not.toBeInTheDocument();
  });

  test('does not render relative time (removed from UI)', () => {
    const { container } = render(<AgentCard agent={mockAgent} active={false} onClick={() => {}} />);

    // Time display was removed in the redesign
    expect(screen.queryByText(/ago/i)).not.toBeInTheDocument();
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
    expect(button).toHaveClass('ring-2', 'ring-cyan-500');
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
