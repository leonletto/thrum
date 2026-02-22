import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@/test/test-utils';
import { DashboardPage } from '../../pages/DashboardPage';
import * as sharedLogic from '@thrum/shared-logic';
import { mockHookReturns } from '@/test/mocks';

// Mock shared-logic hooks
vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    useMessageList: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
    useSendMessage: vi.fn(),
    useMarkAsRead: vi.fn(),
  };
});

vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({
    user: {
      user_id: 'user:testuser',
      username: 'testuser',
      display_name: 'Test User',
      token: 'tok_test',
      status: 'registered',
    },
    isLoading: false,
    error: null,
  }),
}));

/**
 * Integration tests for overall layout structure.
 * Verifies AppShell, Sidebar, and content area work together correctly.
 */
describe('Layout Integration', () => {
  beforeEach(() => {
    vi.mocked(sharedLogic.useCurrentUser).mockReturnValue(mockHookReturns.useCurrentUser());
    vi.mocked(sharedLogic.useMessageList).mockReturnValue({
      data: { messages: [], page: 1, page_size: 50, total_messages: 0, total_pages: 1 },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useAgentList).mockReturnValue(mockHookReturns.useAgentList([
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
    ]) as any);
    vi.mocked(sharedLogic.useGroupList).mockReturnValue({
      data: { groups: [] },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useSendMessage).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMarkAsRead).mockReturnValue(mockHookReturns.useMutation() as any);
  });

  it('should render complete application structure', () => {
    render(<DashboardPage />);

    // Header should be present
    const header = screen.getByRole('banner');
    expect(header).toBeInTheDocument();
    expect(screen.getByText('Thrum')).toBeInTheDocument();

    // Sidebar should be present
    const sidebar = screen.getByRole('complementary');
    expect(sidebar).toBeInTheDocument();

    // Main content area should be present
    const main = screen.getByRole('main');
    expect(main).toBeInTheDocument();
  });

  it('should render header with user identity and settings', () => {
    render(<DashboardPage />);

    // User identity from auth context
    const header = screen.getByRole('banner');
    expect(within(header).getByText(/Test User/)).toBeInTheDocument();

    // Settings button
    const settingsButton = screen.getByLabelText('Settings');
    expect(settingsButton).toBeInTheDocument();
  });

  it('should render sidebar with all navigation sections', () => {
    render(<DashboardPage />);

    const sidebar = screen.getByRole('complementary');

    // Live Feed section in sidebar
    expect(within(sidebar).getAllByText('Live Feed')[0]).toBeInTheDocument();

    // My Inbox section in sidebar
    expect(within(sidebar).getAllByText('My Inbox')[0]).toBeInTheDocument();

    // Agents section
    expect(within(sidebar).getByText(/Agents/i)).toBeInTheDocument();
  });

  it('should render agent list in sidebar', () => {
    const { container } = render(<DashboardPage />);

    // Agent buttons use agent-item class
    const agentButtons = container.querySelectorAll('button.agent-item');
    expect(agentButtons.length).toBeGreaterThanOrEqual(2);
  });

  it('should show correct layout hierarchy', () => {
    const { container } = render(<DashboardPage />);

    // Check structure: header -> body (sidebar + main)
    const header = container.querySelector('header');
    const aside = container.querySelector('aside');
    const main = container.querySelector('main');

    expect(header).toBeInTheDocument();
    expect(aside).toBeInTheDocument();
    expect(main).toBeInTheDocument();

    // Sidebar should be fixed width
    expect(aside?.className).toContain('w-64');

    // Main should be flexible
    expect(main?.className).toContain('flex-1');
  });

  it('should use semantic HTML elements', () => {
    render(<DashboardPage />);

    // Proper semantic elements
    expect(screen.getByRole('banner')).toBeInTheDocument(); // header
    expect(screen.getByRole('complementary')).toBeInTheDocument(); // aside
    expect(screen.getByRole('main')).toBeInTheDocument(); // main
    expect(screen.getByRole('navigation')).toBeInTheDocument(); // nav
  });
});
