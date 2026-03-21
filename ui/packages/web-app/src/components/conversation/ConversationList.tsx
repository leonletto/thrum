import { useConversationList, type ConversationEntry } from '@thrum/shared-logic';
import { Skeleton } from '../ui/skeleton';
import { cn } from '../../lib/utils';

interface ConversationListProps {
  currentAgentId: string;
  selectedAgentId?: string;
  onSelectAgent: (agentId: string) => void;
}

function formatRelativeTime(timestamp: string): string {
  const diff = Date.now() - new Date(timestamp).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'now';
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

function ConversationListSkeleton() {
  return (
    <div className="space-y-1 p-2">
      <Skeleton className="h-3 w-24 mb-2" />
      {[...Array(4)].map((_, i) => (
        <div key={i} className="p-2">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-40 mt-1" />
        </div>
      ))}
    </div>
  );
}

interface ConversationItemProps {
  entry: ConversationEntry;
  isSelected: boolean;
  onClick: () => void;
}

function ConversationItem({ entry, isSelected, onClick }: ConversationItemProps) {
  const { agentId, lastMessage, unreadCount } = entry;

  return (
    <button
      onClick={onClick}
      className={cn(
        'w-full text-left px-3 py-2 rounded-md transition-colors',
        'flex flex-col gap-0.5',
        'hover:bg-[var(--accent-subtle-bg-hover)]',
        isSelected
          ? 'bg-[var(--accent-subtle-bg)] ring-1 ring-[var(--accent-color)]'
          : 'bg-transparent'
      )}
      aria-pressed={isSelected}
    >
      {/* Row 1: agent name + timestamp */}
      <div className="flex items-center justify-between gap-2">
        <span
          className={cn(
            'text-sm font-medium truncate',
            isSelected
              ? 'text-[var(--accent-color)]'
              : 'text-[var(--foreground)]'
          )}
          title={agentId}
        >
          {agentId}
        </span>
        <span className="text-xs shrink-0 text-[var(--muted-foreground)]">
          {formatRelativeTime(lastMessage.timestamp)}
        </span>
      </div>

      {/* Row 2: last message preview + unread badge */}
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-[var(--muted-foreground)] truncate flex-1">
          {lastMessage.direction === 'sent' && (
            <span className="text-[var(--text-secondary)] mr-1">You:</span>
          )}
          {lastMessage.content}
        </span>
        {unreadCount > 0 && (
          <span
            className="shrink-0 px-1.5 py-0.5 text-xs rounded-full font-mono font-semibold leading-none"
            style={{
              backgroundColor: 'var(--accent-color)',
              color: '#fff',
            }}
          >
            {unreadCount > 99 ? '99+' : unreadCount}
          </span>
        )}
      </div>
    </button>
  );
}

export function ConversationList({
  currentAgentId,
  selectedAgentId,
  onSelectAgent,
}: ConversationListProps) {
  const { data, isLoading } = useConversationList(currentAgentId);

  if (isLoading) {
    return <ConversationListSkeleton />;
  }

  const entries = data ?? [];

  if (entries.length === 0) {
    return (
      <div className="px-3 py-4 text-center text-xs text-[var(--muted-foreground)]">
        No conversations yet
      </div>
    );
  }

  return (
    <div className="flex flex-col overflow-y-auto">
      <div className="px-3 py-1 text-xs font-semibold uppercase tracking-wider text-[var(--muted-foreground)]">
        Conversations ({entries.length})
      </div>
      <div className="space-y-0.5 px-1 py-1">
        {entries.map((entry) => (
          <ConversationItem
            key={entry.agentId}
            entry={entry}
            isSelected={selectedAgentId === entry.agentId}
            onClick={() => onSelectAgent(entry.agentId)}
          />
        ))}
      </div>
    </div>
  );
}
