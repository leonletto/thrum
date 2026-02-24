import { X } from 'lucide-react';
import type { MessageScope } from '@thrum/shared-logic';
import { cn } from '@/lib/utils';

interface ScopeBadgeProps {
  scope: MessageScope;
  onClick?: () => void;
  onRemove?: () => void;
  className?: string;
}

export function ScopeBadge({ scope, onClick, onRemove, className }: ScopeBadgeProps) {
  const isClickable = !!onClick;
  const isRemovable = !!onRemove;

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 font-mono text-[10px] uppercase',
        'border border-[var(--accent-border)] bg-transparent rounded-sm px-1.5 py-0.5',
        'transition-colors',
        isClickable && 'cursor-pointer hover:border-[var(--accent-border-hover)] hover:bg-[var(--accent-subtle-bg)]',
        className
      )}
      onClick={onClick}
      role={isClickable ? 'button' : undefined}
      tabIndex={isClickable ? 0 : undefined}
    >
      <span>
        {scope.type}:{scope.value}
      </span>
      {isRemovable && (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="hover:text-[var(--accent-color)] transition-colors"
          aria-label={`Remove ${scope.type}:${scope.value} filter`}
        >
          <X className="h-2.5 w-2.5" />
        </button>
      )}
    </span>
  );
}
