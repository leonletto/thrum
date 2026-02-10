import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SubscriptionPanel } from '../SubscriptionPanel';
import * as hooks from '@thrum/shared-logic';

vi.mock('@thrum/shared-logic', async () => {
  const actual = await vi.importActual('@thrum/shared-logic');
  return {
    ...actual,
    useSubscriptionList: vi.fn(),
    useSubscribe: vi.fn(),
    useUnsubscribe: vi.fn(),
    useAgentList: vi.fn(),
  };
});

describe('SubscriptionPanel', () => {
  let queryClient: QueryClient;
  const mockOnOpenChange = vi.fn();
  const mockSubscribe = vi.fn();
  const mockUnsubscribe = vi.fn();

  const mockSubscriptions = [
    {
      subscription_id: 'sub-1',
      session_id: 'sess-1',
      filter_type: 'scope' as const,
      scope: { type: 'project', value: 'thrum' },
      created_at: '2024-01-01T00:00:00Z',
    },
    {
      subscription_id: 'sub-2',
      session_id: 'sess-1',
      filter_type: 'mention' as const,
      mention: 'assistant',
      created_at: '2024-01-02T00:00:00Z',
    },
    {
      subscription_id: 'sub-3',
      session_id: 'sess-1',
      filter_type: 'all' as const,
      created_at: '2024-01-03T00:00:00Z',
    },
  ];

  const mockAgents = [
    {
      agent_id: 'agent:assistant:ABC',
      kind: 'agent' as const,
      role: 'assistant',
      module: 'core',
      display: 'Assistant Bot',
      registered_at: '2024-01-01T00:00:00Z',
    },
    {
      agent_id: 'agent:researcher:XYZ',
      kind: 'agent' as const,
      role: 'researcher',
      module: 'research',
      display: 'Research Agent',
      registered_at: '2024-01-01T00:00:00Z',
    },
  ];

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });

    mockSubscribe.mockClear();
    mockUnsubscribe.mockClear();
    mockOnOpenChange.mockClear();

    vi.mocked(hooks.useSubscriptionList).mockReturnValue({
      data: { subscriptions: mockSubscriptions },
      isLoading: false,
      error: null,
    } as any);

    vi.mocked(hooks.useSubscribe).mockReturnValue({
      mutate: mockSubscribe,
      isPending: false,
    } as any);

    vi.mocked(hooks.useUnsubscribe).mockReturnValue({
      mutate: mockUnsubscribe,
      isPending: false,
    } as any);

    vi.mocked(hooks.useAgentList).mockReturnValue({
      data: { agents: mockAgents },
      isLoading: false,
      error: null,
    } as any);
  });

  const renderWithProvider = (component: React.ReactElement) => {
    return render(
      <QueryClientProvider client={queryClient}>{component}</QueryClientProvider>
    );
  };

  describe('Basic Rendering', () => {
    it('should render dialog with title when open', () => {
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.getByText('Subscriptions')).toBeInTheDocument();
    });

    it('should not render content when closed', () => {
      renderWithProvider(
        <SubscriptionPanel open={false} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.queryByText('Subscriptions')).not.toBeInTheDocument();
    });

    it('should show Add Subscription button', () => {
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.getByRole('button', { name: /add subscription/i })).toBeInTheDocument();
    });
  });

  describe('Subscription List', () => {
    it('should display existing subscriptions', () => {
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.getByText('project:thrum')).toBeInTheDocument();
      expect(screen.getByText('@assistant')).toBeInTheDocument();
      expect(screen.getByText('All messages')).toBeInTheDocument();
    });

    it('should show filter type labels', () => {
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.getByText('scope')).toBeInTheDocument();
      expect(screen.getByText('mention')).toBeInTheDocument();
      expect(screen.getByText('all')).toBeInTheDocument();
    });

    it('should show empty state when no subscriptions', () => {
      vi.mocked(hooks.useSubscriptionList).mockReturnValue({
        data: { subscriptions: [] },
        isLoading: false,
        error: null,
      } as any);

      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      expect(screen.getByText(/no active subscriptions/i)).toBeInTheDocument();
    });

    it('should show delete button for each subscription', () => {
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      const deleteButtons = screen.getAllByRole('button', { name: /delete subscription/i });
      expect(deleteButtons).toHaveLength(3);
    });
  });

  describe('Delete Subscription', () => {
    it('should call unsubscribe when delete is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      const deleteButtons = screen.getAllByRole('button', { name: /delete subscription/i });
      await user.click(deleteButtons[0]);

      expect(mockUnsubscribe).toHaveBeenCalledWith({ subscription_id: 'sub-1' });
    });
  });

  describe('Add Subscription', () => {
    it('should show add form when Add Subscription is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      await user.click(screen.getByRole('button', { name: /add subscription/i }));

      expect(screen.getByText('Type')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /subscribe/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument();
    });

    it('should show scope fields by default in add form', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      await user.click(screen.getByRole('button', { name: /add subscription/i }));

      expect(screen.getByLabelText(/scope type/i)).toBeInTheDocument();
      expect(screen.getByLabelText(/scope value/i)).toBeInTheDocument();
    });

    it('should call subscribe with scope data', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      await user.click(screen.getByRole('button', { name: /add subscription/i }));

      await user.type(screen.getByLabelText(/scope type/i), 'project');
      await user.type(screen.getByLabelText(/scope value/i), 'my-project');
      await user.click(screen.getByRole('button', { name: /subscribe/i }));

      expect(mockSubscribe).toHaveBeenCalledWith(
        {
          filter_type: 'scope',
          scope: { type: 'project', value: 'my-project' },
        },
        expect.any(Object)
      );
    });

    it('should hide add form when cancel is clicked', async () => {
      const user = userEvent.setup();
      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      await user.click(screen.getByRole('button', { name: /add subscription/i }));
      expect(screen.getByRole('button', { name: /subscribe/i })).toBeInTheDocument();

      await user.click(screen.getByRole('button', { name: /cancel/i }));

      await waitFor(() => {
        expect(screen.queryByRole('button', { name: /subscribe/i })).not.toBeInTheDocument();
      });
    });
  });

  describe('Loading State', () => {
    it('should show loading skeletons when loading', () => {
      vi.mocked(hooks.useSubscriptionList).mockReturnValue({
        data: undefined,
        isLoading: true,
        error: null,
      } as any);

      renderWithProvider(
        <SubscriptionPanel open={true} onOpenChange={mockOnOpenChange} />
      );

      const skeletons = document.querySelectorAll('.animate-pulse');
      expect(skeletons.length).toBeGreaterThan(0);
    });
  });
});
