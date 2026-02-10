import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '../../test/test-utils';
import { AppShell } from '../AppShell';

// Mock useAgentList hook and AuthProvider
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

vi.mock('../AuthProvider', () => ({
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

describe('AppShell', () => {
  test('renders children content', () => {
    render(
      <AppShell>
        <div>Test Content</div>
      </AppShell>
    );

    expect(screen.getByText('Test Content')).toBeInTheDocument();
  });

  test('renders header with Thrum branding', () => {
    render(
      <AppShell>
        <div>Content</div>
      </AppShell>
    );

    expect(screen.getByRole('banner')).toBeInTheDocument();
    expect(screen.getByText('Thrum')).toBeInTheDocument();
  });

  test('renders user identity and settings button in header', () => {
    render(
      <AppShell>
        <div>Content</div>
      </AppShell>
    );

    expect(screen.getByText(/Test User/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /settings/i })).toBeInTheDocument();
  });

  test('renders Sidebar component', () => {
    render(
      <AppShell>
        <div>Content</div>
      </AppShell>
    );

    expect(screen.getByRole('complementary')).toBeInTheDocument();
  });

  test('renders main content area with proper role', () => {
    render(
      <AppShell>
        <div>Main Content</div>
      </AppShell>
    );

    const main = screen.getByRole('main');
    expect(main).toBeInTheDocument();
    expect(main).toHaveTextContent('Main Content');
  });
});
