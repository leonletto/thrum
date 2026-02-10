import { Skeleton } from '../ui/skeleton';

/**
 * Skeleton loading state for message
 *
 * Shows a placeholder message while messages are loading.
 */
export function MessageSkeleton() {
  return (
    <div className="border-l-2 pl-4 py-2">
      <div className="flex items-center gap-2">
        <Skeleton className="h-5 w-24" />
        <Skeleton className="h-3 w-12" />
      </div>
      <Skeleton className="h-16 w-full mt-2" />
    </div>
  );
}

/**
 * Skeleton loading state for multiple messages
 *
 * Shows a list of placeholder messages.
 */
export function MessageListSkeleton({ count = 3 }: { count?: number }) {
  return (
    <div className="space-y-4">
      {[...Array(count)].map((_, i) => (
        <MessageSkeleton key={i} />
      ))}
    </div>
  );
}
