import { FeedItem } from './FeedItem';
import { useFeed } from '../../hooks/useFeed';

export function LiveFeed() {
  const { data: feedItems, isLoading } = useFeed();

  if (isLoading) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="text-muted-foreground">Loading feed...</div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b">
        <h1 className="text-lg font-semibold">Live Feed</h1>
      </div>

      <div className="flex-1 overflow-auto p-4 space-y-2">
        {feedItems?.map((item) => (
          <FeedItem
            key={item.id}
            item={item}
            onClick={() => {
              console.log('Navigate to:', item.to);
            }}
          />
        ))}
      </div>
    </div>
  );
}
