import { describe, test, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@/test/test-utils';
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

/** Build a minimal UseQueryResult-shaped object for useAgentContext */
function makeQueryResult(data: AgentContext[] | undefined, isLoading: boolean) {
  return {
    data,
    isLoading,
    error: null,
    refetch: vi.fn(),
    isError: false,
    isFetching: false,
    isPending: isLoading,
    isSuccess: !isLoading,
    status: isLoading ? ('pending' as const) : ('success' as const),
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
    fetchStatus: 'idle' as const,
    promise: Promise.resolve(data),
  };
}

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

  // ── Loading state ─────────────────────────────────────────────────────────

  test('renders loading skeleton', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult(undefined, true)
    );

    const { container } = render(<AgentContextPanel agentId="agent:test" />);

    // Loading state has animate-pulse class
    const loadingEl = container.querySelector('[data-testid="agent-context-loading"]');
    expect(loadingEl).toBeInTheDocument();
    expect(loadingEl).toHaveClass('animate-pulse');
  });

  // ── Empty state (no active session) ──────────────────────────────────────

  test('renders thin header bar when no active session', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([], false)
    );

    render(<AgentContextPanel agentId="agent:test" />);

    const header = screen.getByTestId('agent-context-header');
    expect(header).toBeInTheDocument();
    // Header should not be a large card — check it has h-10 height class
    expect(header.className).toContain('h-10');
    expect(screen.getByText('No active session')).toBeInTheDocument();
  });

  test('gear icon opens slide-out panel in empty state', async () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([], false)
    );

    render(<AgentContextPanel agentId="agent:test" />);

    // Panel should not be visible initially
    expect(screen.queryByTestId('agent-details-panel')).not.toBeInTheDocument();

    // Click the gear/settings button
    const gearBtn = screen.getByTestId('agent-settings-button');
    fireEvent.click(gearBtn);

    expect(screen.getByTestId('agent-details-panel')).toBeInTheDocument();
  });

  test('Delete Agent button is inside slide-out panel (not inline) in empty state', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([], false)
    );

    render(<AgentContextPanel agentId="agent:test" />);

    // Before opening the panel, Delete Agent button should not be visible
    expect(screen.queryByTestId('open-delete-dialog')).not.toBeInTheDocument();

    // Open the panel
    fireEvent.click(screen.getByTestId('agent-settings-button'));

    // Now Delete Agent button is inside the slide-out panel
    expect(screen.getByTestId('open-delete-dialog')).toBeInTheDocument();
  });

  // ── Active session ────────────────────────────────────────────────────────

  test('renders thin header bar with agent name, role badge, and intent in active session', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext], false)
    );

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    const header = screen.getByTestId('agent-context-header');
    expect(header).toBeInTheDocument();
    // Should be ~40px (h-10) — not a large card
    expect(header.className).toContain('h-10');

    // Display name (last segment of agent_id)
    expect(screen.getByText('ABC123')).toBeInTheDocument();
    // Role badge
    expect(screen.getByText('claude')).toBeInTheDocument();
    // Intent in header
    expect(screen.getByText('Build new dashboard component')).toBeInTheDocument();
  });

  test('gear icon opens slide-out panel with full agent details', async () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext], false)
    );

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    // Panel not visible initially
    expect(screen.queryByTestId('agent-details-panel')).not.toBeInTheDocument();

    // Open panel
    fireEvent.click(screen.getByTestId('agent-settings-button'));

    const panel = screen.getByTestId('agent-details-panel');
    expect(panel).toBeInTheDocument();

    // Full details in slide-out
    expect(screen.getByText('AGENT ID')).toBeInTheDocument();
    expect(screen.getByText('agent:claude:ABC123')).toBeInTheDocument();
    expect(screen.getByText('ROLE')).toBeInTheDocument();
    expect(screen.getByText('INTENT')).toBeInTheDocument();
    expect(screen.getByText('TASK')).toBeInTheDocument();
    expect(screen.getByText('Implement feature X')).toBeInTheDocument();
    expect(screen.getByText('BRANCH')).toBeInTheDocument();
    expect(screen.getByText('feature/new-feature')).toBeInTheDocument();
    expect(screen.getByText(/UNCOMMITTED/)).toBeInTheDocument();
    expect(screen.getByText('file1.ts')).toBeInTheDocument();
    expect(screen.getByText('file2.ts')).toBeInTheDocument();
    expect(screen.getByText('CHANGED')).toBeInTheDocument();
    expect(screen.getByText('HEARTBEAT')).toBeInTheDocument();
  });

  test('close button hides the slide-out panel', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext], false)
    );

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    fireEvent.click(screen.getByTestId('agent-settings-button'));
    expect(screen.getByTestId('agent-details-panel')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /close agent details panel/i }));
    expect(screen.queryByTestId('agent-details-panel')).not.toBeInTheDocument();
  });

  test('Delete Agent button is inside slide-out panel, not inline', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext], false)
    );

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    // Not visible before opening panel
    expect(screen.queryByTestId('open-delete-dialog')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('agent-settings-button'));

    // Now visible inside slide-out
    expect(screen.getByTestId('open-delete-dialog')).toBeInTheDocument();
  });

  test('renders without intent and task sections when not provided', () => {
    const contextWithoutIntentTask: AgentContext = {
      ...mockContext,
      intent: '',
      current_task: '',
    };

    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([contextWithoutIntentTask], false)
    );

    render(<AgentContextPanel agentId="agent:test" />);

    // Intent not shown in header
    expect(screen.queryByText('Build new dashboard component')).not.toBeInTheDocument();

    // Open panel and check INTENT/TASK labels absent
    fireEvent.click(screen.getByTestId('agent-settings-button'));
    expect(screen.queryByText('INTENT')).not.toBeInTheDocument();
    expect(screen.queryByText('TASK')).not.toBeInTheDocument();

    // Other fields still present
    expect(screen.getByText('BRANCH')).toBeInTheDocument();
  });

  test('shows the first context when multiple are provided', () => {
    const secondContext: AgentContext = {
      ...mockContext,
      session_id: 'session-456',
      branch: 'main',
    };

    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext, secondContext], false)
    );

    render(<AgentContextPanel agentId="agent:claude:ABC123" />);

    fireEvent.click(screen.getByTestId('agent-settings-button'));

    // First context branch shown
    expect(screen.getByText('feature/new-feature')).toBeInTheDocument();
    expect(screen.queryByText('main')).not.toBeInTheDocument();
  });

  test('panel has correct CSS structure', () => {
    vi.mocked(sharedLogic.useAgentContext).mockReturnValue(
      makeQueryResult([mockContext], false)
    );

    const { container } = render(<AgentContextPanel agentId="agent:test" />);

    const panel = container.querySelector('.agent-context-panel');
    expect(panel).toBeInTheDocument();

    const header = container.querySelector('[data-testid="agent-context-header"]');
    expect(header).toBeInTheDocument();
    expect(header?.className).toContain('h-10');
  });
});
