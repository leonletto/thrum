import { Skeleton } from '../ui/skeleton';
import { Card, CardHeader } from '../ui/card';

/**
 * Skeleton loading state for thread list
 *
 * Shows 5 placeholder thread items while threads are loading.
 */
export function ThreadListSkeleton() {
  return (
    <div className="space-y-2">
      {[...Array(5)].map((_, i) => (
        <Card key={i}>
          <CardHeader>
            <Skeleton className="h-4 w-3/4" />
            <Skeleton className="h-3 w-1/2 mt-2" />
          </CardHeader>
        </Card>
      ))}
    </div>
  );
}
