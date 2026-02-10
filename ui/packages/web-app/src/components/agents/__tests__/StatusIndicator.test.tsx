import { describe, test, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StatusIndicator } from '../StatusIndicator';

describe('StatusIndicator', () => {
  test('renders online status with active styling', () => {
    const { container } = render(<StatusIndicator status="online" />);
    const indicator = container.firstChild as HTMLElement;

    expect(indicator).toHaveClass('status-indicator');
    // Online status should NOT have inactive class
    const dot = indicator.querySelector('.status-dot');
    expect(dot).not.toHaveClass('inactive');
  });

  test('renders offline status with inactive styling', () => {
    const { container } = render(<StatusIndicator status="offline" />);
    const indicator = container.firstChild as HTMLElement;

    expect(indicator).toHaveClass('status-indicator');
    // Offline status should have inactive class
    const dot = indicator.querySelector('.status-dot');
    expect(dot).toHaveClass('inactive');
  });

  test('contains status-dot and status-bar elements', () => {
    const { container } = render(<StatusIndicator status="online" />);

    expect(container.querySelector('.status-dot')).toBeInTheDocument();
    expect(container.querySelector('.status-bar')).toBeInTheDocument();
  });
});
