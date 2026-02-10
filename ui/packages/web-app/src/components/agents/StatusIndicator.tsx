import { cn } from '../../lib/utils';

interface StatusIndicatorProps {
  status: 'online' | 'offline';
}

export function StatusIndicator({ status }: StatusIndicatorProps) {
  const isActive = status === 'online';

  return (
    <div className="status-indicator">
      <div className={cn('status-dot', !isActive && 'inactive')}></div>
      <div className={cn('status-bar', !isActive && 'inactive')}></div>
    </div>
  );
}
