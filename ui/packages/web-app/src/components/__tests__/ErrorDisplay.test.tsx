import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ErrorDisplay } from '../ErrorDisplay';
import userEvent from '@testing-library/user-event';

describe('ErrorDisplay', () => {
  it('renders default error message', () => {
    render(<ErrorDisplay />);

    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    expect(screen.getByText('An unexpected error occurred')).toBeInTheDocument();
  });

  it('renders error message from Error object', () => {
    const error = new Error('Custom error message');

    render(<ErrorDisplay error={error} />);

    expect(screen.getByText('Custom error message')).toBeInTheDocument();
  });

  it('renders custom title and message', () => {
    render(
      <ErrorDisplay
        title="Connection Failed"
        message="Unable to reach the server"
      />
    );

    expect(screen.getByText('Connection Failed')).toBeInTheDocument();
    expect(screen.getByText('Unable to reach the server')).toBeInTheDocument();
  });

  it('shows retry button when onRetry provided', () => {
    const onRetry = vi.fn();

    render(<ErrorDisplay onRetry={onRetry} />);

    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
  });

  it('does not show retry button when onRetry not provided', () => {
    render(<ErrorDisplay />);

    expect(screen.queryByRole('button', { name: /try again/i })).not.toBeInTheDocument();
  });

  it('calls onRetry when retry button clicked', async () => {
    const user = userEvent.setup();
    const onRetry = vi.fn();

    render(<ErrorDisplay onRetry={onRetry} />);

    const retryButton = screen.getByRole('button', { name: /try again/i });
    await user.click(retryButton);

    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('displays error icon', () => {
    const { container } = render(<ErrorDisplay />);

    const icon = container.querySelector('svg');
    expect(icon).toBeInTheDocument();
    expect(icon).toHaveClass('lucide');
  });

  it('prioritizes custom message over error message', () => {
    const error = new Error('Error message');

    render(<ErrorDisplay error={error} message="Custom message" />);

    expect(screen.getByText('Custom message')).toBeInTheDocument();
    expect(screen.queryByText('Error message')).not.toBeInTheDocument();
  });
});
