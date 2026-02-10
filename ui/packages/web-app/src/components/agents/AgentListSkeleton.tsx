import { Skeleton } from '../ui/skeleton';

/**
 * Skeleton loading state for agent list
 *
 * Shows 4 placeholder agent items while agents are loading.
 */
export function AgentListSkeleton() {
  return (
    <div className="space-y-1 p-2">
      <Skeleton className="h-3 w-16 mb-2" />
      {[...Array(4)].map((_, i) => (
        <div key={i} className="p-2">
          <Skeleton className="h-4 w-32" />
          <Skeleton className="h-3 w-24 mt-1" />
        </div>
      ))}
    </div>
  );
}
