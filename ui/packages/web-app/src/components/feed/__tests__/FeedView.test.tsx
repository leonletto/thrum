import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '../../../test/test-utils';
import { userEvent } from '@testing-library/user-event';
import { FeedView } from '../FeedView';

// ─── Mutable state for mock hooks ────────────────────────────────────────────

const mockState = {
  messages: [] as ReturnType<typeof makeMockMessage>[],
  sessions: [] as ReturnType<typeof makeMockSession>[],
  agents: [] as ReturnType<typeof makeMockAgent>[],
  messagesLoading: false,
  sessionsLoading: false,
  agentsLoading: false,
};

function makeMockAgent(overrides: Record<string, unknown> = {}) {
  return {
    agent_id: 'agent:impl_1:aaa',
    kind: 'agent' as const,
    role: 'implementer',
    module: 'ui',
    display: 'impl_1',
    registered_at: '2024-01-01T10:00:00Z',
    last_seen_at: '2024-01-01T12:00:00Z',
    ...overrides,
  };
}

function makeMockSession(overrides: Record<string, unknown> = {}) {
  return {
    session_id: 'ses-1',
    agent_id: 'agent:impl_2:bbb',
    started_at: '2024-01-01T11:45:00Z',
    ended_at: undefined as string | undefined,
    active: true,
    ...overrides,
  };
}

function makeMockMessage(overrides: Record<string, unknown> = {}) {
  return {
    message_id: 'msg-1',
    thread_id: 'thread-1',
    agent_id: 'agent:impl_1:aaa',
    body: { format: 'markdown', content: 'Starting work...' } as Record<string, string>,
    created_at: '2024-01-01T12:00:00Z',
    scopes: [{ type: 'to', value: 'agent:coordinator:ccc' }] as { type: string; value: string }[],
    refs: [],
    is_read: false,
    ...overrides,
  };
}

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useMessageList: () => ({
      data: mockState.messagesLoading ? undefined : { messages: mockState.messages },
      isLoading: mockState.messagesLoading,
      error: null,
    }),
    useSessionList: () => ({
      data: mockState.sessionsLoading ? undefined : { sessions: mockState.sessions },
      isLoading: mockState.sessionsLoading,
      error: null,
    }),
    useAgentList: () => ({
      data: mockState.agentsLoading ? undefined : { agents: mockState.agents },
      isLoading: mockState.agentsLoading,
      error: null,
    }),
  };
});

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Find a button by partial text content across all child nodes */
function findButtonContaining(text: string) {
  return screen.getAllByRole('button').find((b) => b.textContent?.includes(text));
}

// ─── Tests ───────────────────────────────────────────────────────────────────

describe('FeedView', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2024-01-01T12:05:00Z'));
    // Reset state
    mockState.messages = [];
    mockState.sessions = [];
    mockState.agents = [];
    mockState.messagesLoading = false;
    mockState.sessionsLoading = false;
    mockState.agentsLoading = false;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  test('renders loading state when any data source is loading', () => {
    mockState.messagesLoading = true;

    render(<FeedView />);
    expect(screen.getByText(/loading feed/i)).toBeInTheDocument();
  });

  test('shows messages from message.list', () => {
    mockState.messages = [
      makeMockMessage({ message_id: 'msg-1' }),
      makeMockMessage({
        message_id: 'msg-2',
        body: { format: 'markdown', content: 'Deploy ready' },
        created_at: '2024-01-01T11:30:00Z',
        scopes: [{ type: 'to', value: '@everyone' }],
      }),
    ];

    render(<FeedView />);

    // Preview text is in a nested span — use button content search
    expect(findButtonContaining('Starting work...')).toBeDefined();
    expect(findButtonContaining('Deploy ready')).toBeDefined();
  });

  test('shows session started and ended events', () => {
    mockState.sessions = [
      makeMockSession({
        session_id: 'ses-1',
        agent_id: 'agent:impl_2:bbb',
        started_at: '2024-01-01T11:45:00Z',
        ended_at: undefined,
        active: true,
      }),
      makeMockSession({
        session_id: 'ses-2',
        agent_id: 'agent:impl_1:aaa',
        started_at: '2024-01-01T11:00:00Z',
        ended_at: '2024-01-01T11:50:00Z',
        active: false,
      }),
    ];

    render(<FeedView />);

    expect(findButtonContaining('started session')).toBeDefined();
    expect(findButtonContaining('ended session')).toBeDefined();
  });

  test('shows agent registration events', () => {
    mockState.agents = [
      makeMockAgent({ agent_id: 'agent:impl_1:aaa', display: 'impl_1', role: 'implementer', registered_at: '2024-01-01T10:00:00Z' }),
      makeMockAgent({ agent_id: 'agent:impl_2:bbb', display: 'impl_2', role: 'implementer', registered_at: '2024-01-01T09:00:00Z' }),
      makeMockAgent({ agent_id: 'agent:coordinator:ccc', display: 'coordinator', role: 'coordinator', registered_at: '2024-01-01T08:00:00Z' }),
    ];

    render(<FeedView />);

    // Each agent row contains "registered"
    const registeredButtons = screen.getAllByRole('button').filter((b) =>
      b.textContent?.includes('registered')
    );
    expect(registeredButtons.length).toBeGreaterThanOrEqual(3);

    // Role labels should appear
    const implementerButtons = screen.getAllByRole('button').filter((b) =>
      b.textContent?.includes('implementer')
    );
    expect(implementerButtons.length).toBeGreaterThanOrEqual(2);

    const coordinatorButton = screen.getAllByRole('button').find((b) =>
      b.textContent?.includes('coordinator')
    );
    expect(coordinatorButton).toBeDefined();
  });

  test('merges and sorts all items chronologically (descending)', () => {
    mockState.messages = [
      makeMockMessage({
        message_id: 'msg-1',
        created_at: '2024-01-01T12:00:00Z',
      }),
      makeMockMessage({
        message_id: 'msg-2',
        body: { format: 'markdown', content: 'Deploy ready' },
        created_at: '2024-01-01T11:30:00Z',
      }),
    ];
    mockState.sessions = [
      makeMockSession({
        session_id: 'ses-1',
        started_at: '2024-01-01T11:45:00Z',
        ended_at: '2024-01-01T11:50:00Z',
        active: false,
      }),
    ];
    mockState.agents = [
      makeMockAgent({ registered_at: '2024-01-01T10:00:00Z' }),
    ];

    render(<FeedView />);

    const buttons = screen.getAllByRole('button');

    // Verify all events are present
    const startWorkBtn = buttons.find((b) => b.textContent?.includes('Starting work...'));
    const deployBtn = buttons.find((b) => b.textContent?.includes('Deploy ready'));
    const startedBtn = buttons.find((b) => b.textContent?.includes('started session'));
    const endedBtn = buttons.find((b) => b.textContent?.includes('ended session'));
    const registeredBtn = buttons.find((b) => b.textContent?.includes('registered'));

    expect(startWorkBtn).toBeDefined();
    expect(deployBtn).toBeDefined();
    expect(startedBtn).toBeDefined();
    expect(endedBtn).toBeDefined();
    expect(registeredBtn).toBeDefined();

    // msg-1 (12:00) is newest — should precede msg-2 (11:30) in DOM
    if (startWorkBtn && deployBtn) {
      expect(
        startWorkBtn.compareDocumentPosition(deployBtn) & Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy();
    }
  });

  test('filter "Messages only" hides session and agent events', async () => {
    vi.useRealTimers();
    const user = userEvent.setup();

    mockState.messages = [
      makeMockMessage(),
    ];
    mockState.sessions = [
      makeMockSession({ ended_at: '2024-01-01T11:50:00Z', active: false }),
    ];
    mockState.agents = [
      makeMockAgent(),
    ];

    render(<FeedView />);

    // Open filter dropdown
    const filterBtn = screen.getByRole('button', { name: /filter/i });
    await user.click(filterBtn);
    await user.click(screen.getByRole('option', { name: 'Messages only' }));

    // Message preview should be visible
    const messageBtn = screen.getAllByRole('button').find((b) =>
      b.textContent?.includes('Starting work...')
    );
    expect(messageBtn).toBeDefined();

    // Session and agent events should be gone
    const sessionOrAgentButtons = screen.getAllByRole('button').filter((b) =>
      b.textContent?.includes('started session') ||
      b.textContent?.includes('ended session') ||
      b.textContent?.includes('registered')
    );
    expect(sessionOrAgentButtons.length).toBe(0);
  });

  test('filter "Agent events only" hides messages', async () => {
    vi.useRealTimers();
    const user = userEvent.setup();

    mockState.messages = [
      makeMockMessage(),
    ];
    mockState.sessions = [
      makeMockSession({ ended_at: '2024-01-01T11:50:00Z', active: false }),
    ];
    mockState.agents = [
      makeMockAgent(),
    ];

    render(<FeedView />);

    // Open filter dropdown
    const filterBtn = screen.getByRole('button', { name: /filter/i });
    await user.click(filterBtn);
    await user.click(screen.getByRole('option', { name: 'Agent events only' }));

    // Message preview should be gone
    const messageButtons = screen.getAllByRole('button').filter((b) =>
      b.textContent?.includes('Starting work...')
    );
    expect(messageButtons.length).toBe(0);

    // Agent and session events should still be visible
    const sessionStartBtn = screen.getAllByRole('button').find((b) =>
      b.textContent?.includes('started session')
    );
    expect(sessionStartBtn).toBeDefined();

    const sessionEndBtn = screen.getAllByRole('button').find((b) =>
      b.textContent?.includes('ended session')
    );
    expect(sessionEndBtn).toBeDefined();

    const agentRegBtn = screen.getAllByRole('button').find((b) =>
      b.textContent?.includes('registered')
    );
    expect(agentRegBtn).toBeDefined();
  });
});
