export const mockFactories = {
  user: (overrides = {}) => ({
    user_id: 'user:test',
    username: 'testuser',
    session_id: 'session-test-1',
    display_name: 'Test User',
    ...overrides,
  }),

  agent: (overrides = {}) => ({
    agent_id: 'agent:test:ABC123',
    kind: 'agent' as const,
    role: 'test',
    module: 'testing',
    display: '@test',
    registered_at: '2024-01-01T00:00:00Z',
    last_seen_at: '2024-01-01T12:00:00Z',
    ...overrides,
  }),

  message: (overrides = {}) => ({
    message_id: 'msg-test-1',
    thread_id: 'thread-test-1',
    agent_id: 'user:test',
    body: { format: 'markdown', content: 'Test message' },
    created_at: '2024-01-01T10:00:00Z',
    scopes: [],
    refs: [],
    is_read: true,
    ...overrides,
  }),

  feedItem: (overrides = {}) => ({
    message_id: 'feed-test-1',
    from: 'agent:sender',
    to: 'agent:receiver',
    body: { format: 'markdown', content: 'Feed test message' },
    created_at: '2024-01-01T10:00:00Z',
    ...overrides,
  }),
};

export const mockHookReturns = {
  useCurrentUser: (overrides = {}) => mockFactories.user(overrides),

  useAgentList: (agents = [mockFactories.agent()]) => ({
    data: { agents },
    isLoading: false,
    error: null,
  }),

  useAgentListEmpty: () => ({
    data: { agents: [] },
    isLoading: false,
    error: null,
  }),

  useMutation: () => ({
    mutate: () => {},
    mutateAsync: async () => {},
    isPending: false,
    isError: false,
    error: null,
    reset: () => {},
  }),
};
