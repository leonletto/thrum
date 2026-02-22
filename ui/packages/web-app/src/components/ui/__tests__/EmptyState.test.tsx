import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MessageSquare } from 'lucide-react';
import { EmptyState } from '../EmptyState';

describe('EmptyState', () => {
  it('renders the title', () => {
    render(<EmptyState title="No messages" />);
    expect(screen.getByText('No messages')).toBeInTheDocument();
  });

  it('renders the description when provided', () => {
    render(
      <EmptyState
        title="No messages"
        description="Messages will appear here when you receive them"
      />
    );
    expect(
      screen.getByText('Messages will appear here when you receive them')
    ).toBeInTheDocument();
  });

  it('does not render description when not provided', () => {
    render(<EmptyState title="No messages" />);
    // Only the title paragraph should be in the document
    const paragraphs = document.querySelectorAll('p');
    expect(paragraphs).toHaveLength(1);
  });

  it('renders the icon when provided', () => {
    const { container } = render(
      <EmptyState
        icon={<MessageSquare data-testid="empty-icon" className="h-8 w-8" />}
        title="No messages"
      />
    );
    expect(container.querySelector('[data-testid="empty-icon"]')).toBeInTheDocument();
  });

  it('does not render an icon wrapper when icon is not provided', () => {
    const { container } = render(<EmptyState title="No messages" />);
    // The icon wrapper uses aria-hidden="true"; without icon it should be absent
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeInTheDocument();
  });

  it('renders an action button when action prop is provided', () => {
    const onClick = vi.fn();
    render(
      <EmptyState
        title="No groups yet"
        action={{ label: 'Create a group', onClick }}
      />
    );
    expect(screen.getByRole('button', { name: 'Create a group' })).toBeInTheDocument();
  });

  it('calls action onClick when action button is clicked', async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(
      <EmptyState
        title="No groups yet"
        action={{ label: 'Create a group', onClick }}
      />
    );
    await user.click(screen.getByRole('button', { name: 'Create a group' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('does not render an action button when action prop is not provided', () => {
    render(<EmptyState title="No messages" />);
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
