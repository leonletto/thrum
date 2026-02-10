import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '../../../test/test-utils';
import { ScopeBadge } from '../ScopeBadge';
import type { MessageScope } from '@thrum/shared-logic';
import userEvent from '@testing-library/user-event';

describe('ScopeBadge', () => {
  const mockScope: MessageScope = {
    type: 'project',
    value: 'thrum',
  };

  describe('Basic Rendering', () => {
    it('should render scope in type:value format', () => {
      render(<ScopeBadge scope={mockScope} />);
      expect(screen.getByText('project:thrum')).toBeInTheDocument();
    });

    it('should render with uppercase monospace styling', () => {
      const { container } = render(<ScopeBadge scope={mockScope} />);
      const badge = container.querySelector('.font-mono');
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveClass('uppercase');
      expect(badge).toHaveClass('text-[10px]');
    });

    it('should render with cyan border and transparent background', () => {
      const { container } = render(<ScopeBadge scope={mockScope} />);
      const badge = container.querySelector('.border-cyan-500\\/20');
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveClass('bg-transparent');
    });
  });

  describe('Click Behavior', () => {
    it('should be clickable when onClick is provided', async () => {
      const user = userEvent.setup();
      const handleClick = vi.fn();
      render(<ScopeBadge scope={mockScope} onClick={handleClick} />);

      const badge = screen.getByRole('button');
      expect(badge).toBeInTheDocument();

      await user.click(badge);
      expect(handleClick).toHaveBeenCalledTimes(1);
    });

    it('should not be clickable when onClick is not provided', () => {
      render(<ScopeBadge scope={mockScope} />);
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('should have hover styles when clickable', () => {
      const { container } = render(<ScopeBadge scope={mockScope} onClick={() => {}} />);
      const badge = container.querySelector('.cursor-pointer');
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveClass('hover:border-cyan-500/40');
    });
  });

  describe('Remove Button', () => {
    it('should show remove button when onRemove is provided', () => {
      render(<ScopeBadge scope={mockScope} onRemove={() => {}} />);
      const removeButton = screen.getByLabelText('Remove project:thrum filter');
      expect(removeButton).toBeInTheDocument();
    });

    it('should not show remove button when onRemove is not provided', () => {
      render(<ScopeBadge scope={mockScope} />);
      const removeButton = screen.queryByLabelText('Remove project:thrum filter');
      expect(removeButton).not.toBeInTheDocument();
    });

    it('should call onRemove when remove button is clicked', async () => {
      const user = userEvent.setup();
      const handleRemove = vi.fn();
      render(<ScopeBadge scope={mockScope} onRemove={handleRemove} />);

      const removeButton = screen.getByLabelText('Remove project:thrum filter');
      await user.click(removeButton);

      expect(handleRemove).toHaveBeenCalledTimes(1);
    });

    it('should stop propagation when remove button is clicked', async () => {
      const user = userEvent.setup();
      const handleClick = vi.fn();
      const handleRemove = vi.fn();
      render(
        <ScopeBadge scope={mockScope} onClick={handleClick} onRemove={handleRemove} />
      );

      const removeButton = screen.getByLabelText('Remove project:thrum filter');
      await user.click(removeButton);

      expect(handleRemove).toHaveBeenCalledTimes(1);
      expect(handleClick).not.toHaveBeenCalled();
    });
  });

  describe('Custom Styling', () => {
    it('should accept custom className', () => {
      const { container } = render(
        <ScopeBadge scope={mockScope} className="custom-class" />
      );
      const badge = container.querySelector('.custom-class');
      expect(badge).toBeInTheDocument();
    });

    it('should merge custom className with default classes', () => {
      const { container } = render(
        <ScopeBadge scope={mockScope} className="custom-class" />
      );
      const badge = container.querySelector('.custom-class');
      expect(badge).toHaveClass('font-mono');
      expect(badge).toHaveClass('custom-class');
    });
  });

  describe('Different Scope Types', () => {
    it('should render tag scope', () => {
      const scope: MessageScope = { type: 'tag', value: 'urgent' };
      render(<ScopeBadge scope={scope} />);
      expect(screen.getByText('tag:urgent')).toBeInTheDocument();
    });

    it('should render feature scope', () => {
      const scope: MessageScope = { type: 'feature', value: 'auth' };
      render(<ScopeBadge scope={scope} />);
      expect(screen.getByText('feature:auth')).toBeInTheDocument();
    });

    it('should render scope with spaces in value', () => {
      const scope: MessageScope = { type: 'project', value: 'my project' };
      render(<ScopeBadge scope={scope} />);
      expect(screen.getByText('project:my project')).toBeInTheDocument();
    });

    it('should render scope with special characters', () => {
      const scope: MessageScope = { type: 'tag', value: 'high-priority!' };
      render(<ScopeBadge scope={scope} />);
      expect(screen.getByText('tag:high-priority!')).toBeInTheDocument();
    });
  });

  describe('Accessibility', () => {
    it('should have proper tabIndex when clickable', () => {
      render(<ScopeBadge scope={mockScope} onClick={() => {}} />);
      const badge = screen.getByRole('button');
      expect(badge).toHaveAttribute('tabIndex', '0');
    });

    it('should have descriptive aria-label for remove button', () => {
      render(<ScopeBadge scope={mockScope} onRemove={() => {}} />);
      const removeButton = screen.getByLabelText('Remove project:thrum filter');
      expect(removeButton).toBeInTheDocument();
    });
  });
});
