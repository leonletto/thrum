import { describe, test, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { userEvent } from '@testing-library/user-event';
import { SidebarItem } from '../SidebarItem';

describe('SidebarItem', () => {
  test('renders label and nav-icon div', () => {
    const { container } = render(
      <SidebarItem
        icon={<span>📊</span>}
        label="Test Item"
        onClick={() => {}}
      />
    );

    expect(screen.getByText('Test Item')).toBeInTheDocument();
    // Icon prop is reserved for future use - currently using CSS nav-icon
    const navIcon = container.querySelector('.nav-icon');
    expect(navIcon).toBeInTheDocument();
  });

  test('calls onClick when clicked', async () => {
    const user = userEvent.setup();
    const handleClick = vi.fn();

    render(
      <SidebarItem
        icon={<span>📊</span>}
        label="Test Item"
        onClick={handleClick}
      />
    );

    await user.click(screen.getByRole('button', { name: /test item/i }));
    expect(handleClick).toHaveBeenCalledTimes(1);
  });

  test('shows badge when badge prop is provided', () => {
    render(
      <SidebarItem
        icon={<span>📊</span>}
        label="Test Item"
        badge={5}
        onClick={() => {}}
      />
    );

    expect(screen.getByText('5')).toBeInTheDocument();
  });

  test('does not show badge when badge is 0', () => {
    render(
      <SidebarItem
        icon={<span>📊</span>}
        label="Test Item"
        badge={0}
        onClick={() => {}}
      />
    );

    expect(screen.queryByText('0')).not.toBeInTheDocument();
  });

  test('applies active styling when active prop is true', () => {
    render(
      <SidebarItem
        icon={<span>📊</span>}
        label="Active Item"
        active={true}
        onClick={() => {}}
      />
    );

    const button = screen.getByRole('button', { name: /active item/i });
    expect(button).toHaveClass('active');
  });
});
