import { useEffect } from 'react';
import { ChevronDown, ChevronRight, MessageSquare, AlertTriangle } from 'lucide-react';
import {
  useThread,
  useMarkAsRead,
  type ThreadListResponse,
  type MessageScope,
} from '@thrum/shared-logic';
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { ScopeBadge } from '@/components/ui/ScopeBadge';
import { cn } from '@/lib/utils';
import { MessageBubble } from './MessageBubble';
import { InlineReply } from './InlineReply';

interface ThreadItemProps {
  thread: ThreadListResponse['threads'][number];
  expanded: boolean;
  onToggle: () => void;
  sendingAs: string;
  isImpersonating: boolean;
}

export function ThreadItem({
  thread,
  expanded,
  onToggle,
  sendingAs,
  isImpersonating,
}: ThreadItemProps) {
  // Lazy load thread detail only when expanded
  const { data: threadDetail, isLoading } = useThread(thread.thread_id, {
    enabled: expanded,
  });

  const markAsRead = useMarkAsRead();

  // Auto-mark messages as read when thread is expanded
  useEffect(() => {
    if (!expanded || !threadDetail?.messages) return;

    const unreadIds = threadDetail.messages
      .filter((m) => !m.is_read)
      .map((m) => m.message_id);

    if (unreadIds.length === 0) return;

    // Debounce to avoid excessive API calls
    const timer = setTimeout(() => {
      markAsRead.mutate(unreadIds);
    }, 500);

    return () => clearTimeout(timer);
  }, [expanded, threadDetail?.messages, markAsRead]);

  const formatTimestamp = (timestamp: string) => {
    const date = new Date(timestamp);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    if (diffHours < 24) return `${diffHours}h ago`;
    if (diffDays < 7) return `${diffDays}d ago`;
    return date.toLocaleDateString();
  };

  const messageCount = thread.message_count || 0;
  const unreadCount = thread.unread_count || 0;

  // Extract unique scopes from all messages in thread
  const threadScopes: MessageScope[] = threadDetail?.messages
    ? Array.from(
        new Map(
          threadDetail.messages
            .flatMap((msg) => msg.scopes || [])
            .map((scope) => [`${scope.type}:${scope.value}`, scope])
        ).values()
      )
    : [];

  // Check if any message in thread is high priority
  const hasHighPriority = threadDetail?.messages?.some((msg) => msg.priority === 'high') ?? false;

  return (
    <Card className={cn('thread-item', expanded && 'ring-2 ring-primary')}>
      <CardHeader
        className="cursor-pointer transition-colors p-0"
        onClick={onToggle}
        role="button"
        tabIndex={0}
        aria-label={`Thread: ${thread.title || 'Untitled'}, ${messageCount} messages${unreadCount > 0 ? `, ${unreadCount} unread` : ''}`}
        aria-expanded={expanded}
      >
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" className="h-6 w-6 shrink-0">
            {expanded ? (
              <ChevronDown className="h-4 w-4" />
            ) : (
              <ChevronRight className="h-4 w-4" />
            )}
          </Button>

          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              {hasHighPriority && (
                <AlertTriangle className="h-4 w-4" style={{ color: '#ef4444' }} />
              )}
              <h3 className="thread-participants truncate">{thread.title}</h3>
              {thread.unread_count && thread.unread_count > 0 && (
                <Badge variant="default" className="text-xs">
                  {thread.unread_count} new
                </Badge>
              )}
            </div>
            {thread.preview && !expanded && (
              <p className="thread-preview mt-1 truncate">
                {thread.preview}
              </p>
            )}
            <div className="flex items-center gap-2 thread-time mt-1">
              <MessageSquare className="h-3 w-3" />
              <span>{thread.message_count} messages</span>
              <span>•</span>
              <span>{formatTimestamp(thread.last_activity ?? new Date().toISOString())}</span>
            </div>
            {expanded && threadScopes.length > 0 && (
              <div className="flex flex-wrap gap-1 mt-2">
                {threadScopes.map((scope, index) => (
                  <ScopeBadge key={`${scope.type}-${scope.value}-${index}`} scope={scope} />
                ))}
              </div>
            )}
          </div>
        </div>
      </CardHeader>

      {expanded && (
        <CardContent className="pt-0">
          {isLoading ? (
            <div className="py-8 text-center text-muted-foreground">
              Loading messages...
            </div>
          ) : threadDetail?.messages ? (
            <>
              <div className="space-y-3 mb-4">
                {threadDetail.messages.map((message) => (
                  <MessageBubble
                    key={message.message_id}
                    message={message}
                    isOwn={message.agent_id === sendingAs}
                  />
                ))}
              </div>

              <InlineReply
                threadId={thread.thread_id}
                sendingAs={sendingAs}
                isImpersonating={isImpersonating}
              />
            </>
          ) : (
            <div className="py-4 text-center text-muted-foreground">
              No messages
            </div>
          )}
        </CardContent>
      )}
    </Card>
  );
}
