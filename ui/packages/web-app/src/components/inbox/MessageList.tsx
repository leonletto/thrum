import { useEffect, useRef, useState } from 'react';
import { MessageSquare } from 'lucide-react';
import type { Message } from '@thrum/shared-logic';
import {
  groupByConversation,
  useMarkAsRead,
  useDebounce,
} from '@thrum/shared-logic';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { MessageBubble } from './MessageBubble';
import { MessageListSkeleton } from './MessageSkeleton';
import { EmptyState } from '@/components/ui/EmptyState';

interface MessageListProps {
  messages: Message[];
  isLoading: boolean;
  currentUserId?: string;
  onReply?: (messageId: string, senderName: string) => void;
  // Pagination props
  totalCount?: number;
  hasMore?: boolean;
  onLoadMore?: () => void;
  isLoadingMore?: boolean;
}

interface ConversationItemProps {
  conversation: ReturnType<typeof groupByConversation>[number];
  currentUserId?: string;
  onReply?: (messageId: string, senderName: string) => void;
}

function getSenderName(message: Message): string {
  return message.agent_id ?? 'Unknown';
}

function ConversationItem({ conversation, currentUserId, onReply }: ConversationItemProps) {
  const [expanded, setExpanded] = useState(false);
  const { rootMessage, replies } = conversation;
  const hasReplies = replies.length > 0;

  return (
    <div className="border rounded-lg p-3 space-y-2 bg-background">
      {/* Root message row */}
      <div className="flex items-start gap-2">
        <div className="flex-1 min-w-0">
          <MessageBubble
            message={rootMessage}
            isOwn={currentUserId !== undefined && rootMessage.agent_id === currentUserId}
          />
        </div>
        {onReply && (
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0 text-xs"
            onClick={() => onReply(rootMessage.message_id, getSenderName(rootMessage))}
            aria-label={`Reply to ${getSenderName(rootMessage)}`}
          >
            Reply
          </Button>
        )}
      </div>

      {/* Reply count toggle */}
      {hasReplies && (
        <button
          className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors pl-1"
          onClick={() => setExpanded(prev => !prev)}
          aria-expanded={expanded}
          aria-label={expanded ? 'Collapse replies' : `Show ${replies.length} ${replies.length === 1 ? 'reply' : 'replies'}`}
        >
          <span className="text-xs">{expanded ? '▲' : '▼'}</span>
          {!expanded && (
            <Badge variant="secondary" className="text-xs">
              {replies.length} {replies.length === 1 ? 'reply' : 'replies'}
            </Badge>
          )}
          {expanded && (
            <span className="text-xs text-muted-foreground">Collapse replies</span>
          )}
        </button>
      )}

      {/* Replies */}
      {expanded && hasReplies && (
        <div className="pl-4 border-l-2 border-muted space-y-2">
          {replies.map(reply => (
            <div key={reply.message_id} className="flex items-start gap-2">
              <div className="flex-1 min-w-0">
                <MessageBubble
                  message={reply}
                  isOwn={currentUserId !== undefined && reply.agent_id === currentUserId}
                />
              </div>
              {onReply && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="shrink-0 text-xs"
                  onClick={() => onReply(reply.message_id, getSenderName(reply))}
                  aria-label={`Reply to ${getSenderName(reply)}`}
                >
                  Reply
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function MessageList({
  messages,
  isLoading,
  currentUserId,
  onReply,
  totalCount,
  hasMore,
  onLoadMore,
  isLoadingMore,
}: MessageListProps) {
  const markAsRead = useMarkAsRead();

  // Collect IDs of unread messages, debounce to batch-mark as read
  const unreadIds = messages
    .filter(m => m.is_read === false)
    .map(m => m.message_id);

  const debouncedUnreadIds = useDebounce(unreadIds, 500);

  // Track which IDs we've already dispatched so we don't re-call on rerenders
  const markedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    if (debouncedUnreadIds.length === 0) return;
    const toMark = debouncedUnreadIds.filter(id => !markedRef.current.has(id));
    if (toMark.length === 0) return;
    toMark.forEach(id => markedRef.current.add(id));
    markAsRead.mutate(toMark);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedUnreadIds]);

  if (isLoading) {
    return (
      <ScrollArea className="flex-1 px-4 py-2">
        <MessageListSkeleton count={3} />
      </ScrollArea>
    );
  }

  if (messages.length === 0) {
    return (
      <ScrollArea className="flex-1">
        <EmptyState
          icon={<MessageSquare className="h-8 w-8" />}
          title="No messages"
          description="Messages will appear here when you receive them"
        />
      </ScrollArea>
    );
  }

  const conversations = groupByConversation(messages);
  const showingCount = messages.length;

  return (
    <ScrollArea className="flex-1 px-4 py-2">
      {/* Load More button at the top — older messages load above */}
      {hasMore && onLoadMore && (
        <div className="flex flex-col items-center gap-1.5 pb-3">
          {totalCount !== undefined && (
            <p className="text-xs text-muted-foreground">
              Showing {showingCount} of {totalCount} messages
            </p>
          )}
          <Button
            variant="outline"
            size="sm"
            onClick={onLoadMore}
            disabled={isLoadingMore}
            aria-label="Load more messages"
          >
            {isLoadingMore ? 'Loading...' : 'Load More'}
          </Button>
        </div>
      )}

      <div className="space-y-3">
        {conversations.map(convo => (
          <ConversationItem
            key={convo.rootMessage.message_id}
            conversation={convo}
            currentUserId={currentUserId}
            onReply={onReply}
          />
        ))}
      </div>
    </ScrollArea>
  );
}
