import { Button } from '@/components/ui/button';

interface EmptyStateProps {
  icon?: React.ReactNode;
  title: string;
  description?: string;
  action?: {
    label: string;
    onClick: () => void;
  };
}

export function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12 text-muted-foreground">
      {icon && (
        <div className="opacity-40" aria-hidden="true">
          {icon}
        </div>
      )}
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description && (
        <p className="text-xs text-center max-w-[240px]">{description}</p>
      )}
      {action && (
        <Button
          variant="outline"
          size="sm"
          className="mt-1"
          onClick={action.onClick}
        >
          {action.label}
        </Button>
      )}
    </div>
  );
}
