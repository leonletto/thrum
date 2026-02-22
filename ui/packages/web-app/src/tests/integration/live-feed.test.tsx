import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@/test/test-utils';
import userEvent from '@testing-library/user-event';
import { DashboardPage } from '../../pages/DashboardPage';
import { selectLiveFeed } from '@thrum/shared-logic';
import * as sharedLogic from '@thrum/shared-logic';
import { mockHookReturns } from '@/test/mocks';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useCurrentUser: vi.fn(),
    useMessageList: vi.fn(),
    useAgentList: vi.fn(),
    useGroupList: vi.fn(),
    useSessionList: vi.fn(),
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

// Create mock messages that FeedView will display
const fiveMinutesAgo = new Date(Date.now() - 5 * 60 * 1000).toISOString();
const tenMinutesAgo = new Date(Date.now() - 10 * 60 * 1000).toISOString();
const twentyMinutesAgo = new Date(Date.now() - 20 * 60 * 1000).toISOString();

const mockMessages = [
  {
    message_id: 'msg-1',
    thread_id: 'thread-1',
    agent_id: 'agent:builder',
    session_id: 'ses-1',
    body: { format: 'markdown', content: 'Build completed successfully' },
    created_at: fiveMinutesAgo,
    scopes: [{ type: 'to', value: 'agent:coordinator' }],
    refs: [],
    is_read: false,
    mentions: [],
    priority: 'normal',
  },
  {
    message_id: 'msg-2',
    thread_id: 'thread-2',
    agent_id: 'agent:reviewer',
    session_id: 'ses-2',
    body: { format: 'markdown', content: 'Can you check the logs?' },
    created_at: tenMinutesAgo,
    scopes: [{ type: 'to', value: 'agent:builder' }],
    refs: [],
    is_read: false,
    mentions: [],
    priority: 'normal',
  },
  {
    message_id: 'msg-3',
    thread_id: 'thread-3',
    agent_id: 'agent:system',
    session_id: 'ses-3',
    body: { format: 'markdown', content: 'Agent registered' },
    created_at: twentyMinutesAgo,
    scopes: [{ type: 'to', value: 'agent:coordinator' }],
    refs: [],
    is_read: true,
    mentions: [],
    priority: 'normal',
  },
];

/**
 * Integration tests for Live Feed / FeedView functionality.
 * Tests feed display and interaction with mock data.
 */
describe('Live Feed Integration', () => {
  beforeEach(() => {
    selectLiveFeed();
    vi.mocked(sharedLogic.useCurrentUser).mockReturnValue(mockHookReturns.useCurrentUser());
    vi.mocked(sharedLogic.useMessageList).mockReturnValue({
      data: {
        messages: mockMessages,
        page: 1,
        page_size: 50,
        total_messages: mockMessages.length,
        total_pages: 1,
      },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useAgentList).mockReturnValue(mockHookReturns.useAgentList([]) as any);
    vi.mocked(sharedLogic.useGroupList).mockReturnValue({
      data: { groups: [] },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useSessionList).mockReturnValue({
      data: { sessions: [] },
      isLoading: false,
      error: null,
    } as any);
    vi.mocked(sharedLogic.useSendMessage).mockReturnValue(mockHookReturns.useMutation() as any);
    vi.mocked(sharedLogic.useMarkAsRead).mockReturnValue(mockHookReturns.useMutation() as any);
  });

  it('should display Activity Feed by default', () => {
    render(<DashboardPage />);

    expect(
      screen.getByText(/activity feed/i)
    ).toBeInTheDocument();
  });

  it('should display feed items from mock data', () => {
    const { container } = render(<DashboardPage />);

    // FeedView renders buttons for feed items
    const main = container.querySelector('main');
    const feedButtons = main?.querySelectorAll('button[class*="rounded-md"]');
    expect(feedButtons && feedButtons.length > 0).toBe(true);
  });

  it('should show message previews', () => {
    render(<DashboardPage />);

    const main = screen.getByRole('main');

    expect(within(main).getByText(/Build completed successfully/)).toBeInTheDocument();
    expect(within(main).getByText(/Can you check the logs\?/)).toBeInTheDocument();
    expect(within(main).getByText(/Agent registered/)).toBeInTheDocument();
  });

  it('should display feed item structure', () => {
    const { container } = render(<DashboardPage />);

    const main = container.querySelector('main');
    expect(main).toBeInTheDocument();

    const feedItems = main?.querySelectorAll('button[class*="rounded-md"]');
    expect(feedItems && feedItems.length > 0).toBe(true);
  });

  it('should show relative timestamps in feed', () => {
    const { container } = render(<DashboardPage />);

    const main = container.querySelector('main');
    expect(main?.textContent).toContain('ago');
  });

  it('should return to Activity Feed when clicking Live Feed in sidebar', async () => {
    const user = userEvent.setup();
    const { container } = render(<DashboardPage />);

    // Navigate away first
    const sidebar = container.querySelector('aside');
    const sidebarButtons = sidebar?.querySelectorAll('button');
    const inboxButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('My Inbox')
    );
    await user.click(inboxButton!);

    // Navigate back to Live Feed
    const feedButton = Array.from(sidebarButtons!).find((btn) =>
      btn.textContent?.includes('Live Feed')
    );
    await user.click(feedButton!);

    expect(
      screen.getByText(/activity feed/i)
    ).toBeInTheDocument();
  });

  it('should display feed in scrollable container', () => {
    const { container } = render(<DashboardPage />);

    // FeedView uses ScrollArea which provides scroll capability
    const main = container.querySelector('main');
    expect(main).toBeInTheDocument();
  });
});
