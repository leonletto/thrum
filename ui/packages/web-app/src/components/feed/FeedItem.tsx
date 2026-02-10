import { formatRelativeTime } from '../../lib/time';
import { useAgentList } from '@thrum/shared-logic';
import type { FeedItem as FeedItemType } from '../../types/feed';

interface FeedItemProps {
  item: FeedItemType;
  onClick: () => void;
}

export function FeedItem({ item, onClick }: FeedItemProps) {
  const { data: agentListData } = useAgentList();

  // Get display name for agent_id
  const getDisplayName = (agentId: string | undefined) => {
    if (!agentId) return undefined;
    const agent = agentListData?.agents.find(a => a.agent_id === agentId);
    return agent?.display || agentId;
  };

  const fromDisplay = getDisplayName(item.from) || item.from;
  const toDisplay = getDisplayName(item.to) || item.to;

  return (
    <button
      onClick={onClick}
      className="w-full p-3 rounded-md hover:bg-accent/50 text-left"
    >
      <div className="flex items-center gap-2 text-sm">
        <span className="font-medium">{fromDisplay}</span>
        <span className="text-muted-foreground">→</span>
        <span className="font-medium">{toDisplay}</span>
        <span className="text-muted-foreground ml-auto text-xs">
          {formatRelativeTime(item.timestamp)}
        </span>
      </div>
      {item.preview && (
        <p className="text-sm text-muted-foreground mt-1 truncate">
          {item.preview}
        </p>
      )}
    </button>
  );
}
