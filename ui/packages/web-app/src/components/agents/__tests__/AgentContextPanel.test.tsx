import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@/test/test-utils';
import { AgentContextPanel } from '../AgentContextPanel';
import * as sharedLogic from '@thrum/shared-logic';
import type { AgentContext } from '@thrum/shared-logic';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useAgentContext: vi.fn(),
  };
});

describe('AgentContextPanel', () => {
  const mockContext: AgentContext = {
    session_id: 'session-123',
    agent_id: 'agent:claude:ABC123',
    branch: 'feature/new-feature',
    worktree_path: '/home/user/workspace',
    unmerged_commits: [],
    uncommitted_files: ['file1.ts', 'file2.ts'],
    changed_files: ['file3.ts'],
    git_updated_at: '2024-01-01T12:00:00Z',
    current_task: 'Implement feature X',
    task_updated_at: '2024-01-01T12:00:00Z',
    intent: 'Build new dashboard component',
    intent_updated_at: '2024-01-01T12:00:00Z',
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  test('renders loading state', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: true,
      isSuccess: false,
      status: 'pending',
      dataUpdatedAt: 0,
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve(undefined),
    });

    const { container } = render(<AgentContextPanel agentId="agent:test" />);

    const skeletons = container.querySelectorAll('.animate-pulse');
    expect(skeletons.length).toBeGreaterThan(0);
  });

  test('renders empty state when no contexts exist', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: false,
      isSuccess: true,
      status: 'success',
      dataUpdatedAt: Date.now(),
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve([]),
    });

    render(<AgentContextPanel agentId="agent:test" />);

    expect(screen.getByText('No active session')).toBeInTheDocument();
    expect(screen.getByTestId('open-delete-dialog')).toBeInTheDocument();
    expect(screen.getByText('Delete Agent')).toBeInTheDocument();
  });

  test('renders context data', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: [mockContext],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: false,
      isSuccess: true,
      status: 'success',
      dataUpdatedAt: Date.now(),
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve([mockContext]),
    });

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    // Check labels
    expect(screen.getByText('AGENT')).toBeInTheDocument();
    expect(screen.getByText('AGENT ID')).toBeInTheDocument();
    expect(screen.getByText('INTENT')).toBeInTheDocument();
    expect(screen.getByText('TASK')).toBeInTheDocument();
    expect(screen.getByText('BRANCH')).toBeInTheDocument();
    expect(screen.getByText('UNCOMMITTED')).toBeInTheDocument();
    expect(screen.getByText('CHANGED')).toBeInTheDocument();
    expect(screen.getByText('HEARTBEAT')).toBeInTheDocument();

    // Check values
    expect(screen.getByText('ABC123')).toBeInTheDocument(); // Display name from agent_id
    expect(screen.getByText('agent:claude:ABC123')).toBeInTheDocument();
    expect(screen.getByText('Build new dashboard component')).toBeInTheDocument();
    expect(screen.getByText('Implement feature X')).toBeInTheDocument();
    expect(screen.getByText('feature/new-feature')).toBeInTheDocument();
    expect(screen.getByText('2 files')).toBeInTheDocument();
    expect(screen.getByText('1 files')).toBeInTheDocument();
  });

  test('renders without intent and task when not provided', () => {
    const contextWithoutIntentTask: AgentContext = {
      ...mockContext,
      intent: '',
      current_task: '',
    };

    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: [contextWithoutIntentTask],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: false,
      isSuccess: true,
      status: 'success',
      dataUpdatedAt: Date.now(),
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve([contextWithoutIntentTask]),
    });

    render(<AgentContextPanel agentId="agent:test" />);

    // Should not show INTENT or TASK labels
    expect(screen.queryByText('INTENT')).not.toBeInTheDocument();
    expect(screen.queryByText('TASK')).not.toBeInTheDocument();

    // Should still show other fields
    expect(screen.getByText('BRANCH')).toBeInTheDocument();
    expect(screen.getByText('UNCOMMITTED')).toBeInTheDocument();
  });

  test('handles multiple contexts by showing the first one', () => {
    const secondContext: AgentContext = {
      ...mockContext,
      session_id: 'session-456',
      branch: 'main',
    };

    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: [mockContext, secondContext],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: false,
      isSuccess: true,
      status: 'success',
      dataUpdatedAt: Date.now(),
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve([mockContext, secondContext]),
    });

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    // Should show the first context's branch
    expect(screen.getByText('feature/new-feature')).toBeInTheDocument();
    expect(screen.queryByText('main')).not.toBeInTheDocument();
  });

  test('applies correct CSS classes', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue({
      data: [mockContext],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      isError: false,
      isFetching: false,
      isPending: false,
      isSuccess: true,
      status: 'success',
      dataUpdatedAt: Date.now(),
      errorUpdatedAt: 0,
      failureCount: 0,
      failureReason: null,
      errorUpdateCount: 0,
      isLoadingError: false,
      isRefetchError: false,
      isRefetching: false,
      isStale: false,
      isPaused: false,
      isPlaceholderData: false,
      fetchStatus: 'idle',
      promise: Promise.resolve([mockContext]),
    });

    const { container } = render(<AgentContextPanel agentId="agent:test" />);

    const panel = container.querySelector('.agent-context-panel');
    expect(panel).toBeInTheDocument();
    expect(panel).toHaveClass('panel');

    const grid = container.querySelector('.context-grid');
    expect(grid).toBeInTheDocument();

    const labels = container.querySelectorAll('.context-label');
    expect(labels.length).toBeGreaterThan(0);

    const values = container.querySelectorAll('.context-value');
    expect(values.length).toBeGreaterThan(0);
  });
});
