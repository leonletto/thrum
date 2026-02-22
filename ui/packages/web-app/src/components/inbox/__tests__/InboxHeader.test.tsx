import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@/test/test-utils';
import { InboxHeader } from '../InboxHeader';
import userEvent from '@testing-library/user-event';

describe('InboxHeader', () => {
  const defaultProps = {
    identity: 'test-user',
    sendingAs: 'test-user',
    isImpersonating: false,
    unreadCount: 0,
    filter: 'all' as const,
    onFilterChange: vi.fn(),
  };

  it('should render identity', () => {
    render(<InboxHeader {...defaultProps} />);
    expect(screen.getByText('test-user')).toBeInTheDocument();
  });

  it('should show impersonation warning when isImpersonating is true', () => {
    render(
      <InboxHeader
        {...defaultProps}
        sendingAs="agent:claude"
        isImpersonating={true}
      />
    );
    expect(screen.getByText(/Sending as agent:claude/)).toBeInTheDocument();
  });

  it('should render filter toggle buttons', () => {
    render(<InboxHeader {...defaultProps} />);
    expect(screen.getByText('All')).toBeInTheDocument();
    expect(screen.getByText('Unread')).toBeInTheDocument();
  });

  it('should highlight "All" button when filter is "all"', () => {
    render(<InboxHeader {...defaultProps} filter="all" />);
    const allButton = screen.getByText('All').closest('button');
    expect(allButton).toHaveClass('bg-gradient-to-br'); // default variant
  });

  it('should highlight "Unread" button when filter is "unread"', () => {
    render(<InboxHeader {...defaultProps} filter="unread" />);
    const unreadButton = screen.getByText('Unread').closest('button');
    expect(unreadButton).toHaveClass('bg-gradient-to-br'); // default variant
  });

  it('should show unread count badge when unreadCount > 0', () => {
    render(<InboxHeader {...defaultProps} unreadCount={5} />);
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('should not show unread count badge when unreadCount is 0', () => {
    const { container } = render(<InboxHeader {...defaultProps} unreadCount={0} />);
    // Badge component won't be rendered at all
    const badges = container.querySelectorAll('[class*="badge"]');
    expect(badges.length).toBe(0);
  });

  it('should call onFilterChange with "all" when All button is clicked', async () => {
    const user = userEvent.setup();
    const onFilterChange = vi.fn();
    render(
      <InboxHeader {...defaultProps} filter="unread" onFilterChange={onFilterChange} />
    );

    await user.click(screen.getByText('All'));
    expect(onFilterChange).toHaveBeenCalledWith('all');
  });

  it('should call onFilterChange with "unread" when Unread button is clicked', async () => {
    const user = userEvent.setup();
    const onFilterChange = vi.fn();
    render(
      <InboxHeader {...defaultProps} filter="all" onFilterChange={onFilterChange} />
    );

    await user.click(screen.getByText('Unread'));
    expect(onFilterChange).toHaveBeenCalledWith('unread');
  });

  it('should render scope filter button', () => {
    render(<InboxHeader {...defaultProps} />);
    expect(screen.getByText('Scope Filter')).toBeInTheDocument();
  });
});
