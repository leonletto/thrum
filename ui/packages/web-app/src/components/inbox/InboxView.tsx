import { useMemo, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import {
  useCurrentUser,
  loadStoredUser,
  useMessageListPaged,
  type MessageScope,
} from '@thrum/shared-logic';
import { InboxHeader, type InboxFilter } from './InboxHeader';
import { MessageList } from './MessageList';
import { ComposeBar } from './ComposeBar';

interface InboxViewProps {
  /**
   * Optional identity ID to show inbox for specific agent.
   * If not provided, shows the current user's inbox.
   */
  identityId?: string;
  /** Deep-link: scroll to and highlight this message ID when set. */
  selectedMessageId?: string | null;
  /** Called after the highlight animation clears the selection. */
  onClearSelectedMessage?: () => void;
}

export function InboxView({ identityId, selectedMessageId, onClearSelectedMessage }: InboxViewProps) {
  const currentUser = useCurrentUser();
  const [filter, setFilter] = useState<InboxFilter>('all');
  const [scopeFilter, setScopeFilter] = useState<MessageScope | null>(null);
  const [replyTo, setReplyTo] = useState<{
    messageId: string;
    senderName: string;
  } | undefined>(undefined);

  // Determine the identity whose inbox we're viewing.
  // Falls back to localStorage-persisted user (available before React Query
  // cache is populated) and finally to a friendly default instead of "Unknown".
  const storedUser = loadStoredUser();
  const identity =
    identityId ||
    currentUser?.username ||
    storedUser?.username ||
    'Thrum User';

  // Determine sending identity based on whose inbox we're viewing
  const sendingAs = useMemo(() => {
    if (!currentUser) return identity;
    if (identityId && identityId !== currentUser.username) {
      // Viewing another agent's inbox, send as that agent (impersonation)
      return identityId;
    }
    // Viewing own inbox, send as self
    return currentUser.username;
  }, [identityId, currentUser, identity]);

  const isImpersonating = currentUser
    ? sendingAs !== currentUser.username
    : false;

  // Build message.list request params (without `page` — managed by the hook).
  const messageListParams = useMemo(() => {
    const params: {
      for_agent: string;
      page_size: number;
      sort_order: 'desc';
      unread_for_agent?: string;
      mention?: string;
      scope?: MessageScope;
    } = {
      for_agent: identity,
      page_size: 50,
      sort_order: 'desc',
    };

    // When "Unread" tab selected, add unread_for_agent filter
    if (filter === 'unread') {
      params.unread_for_agent = identity;
    }

    // When "Mentions" tab selected, add mention filter
    if (filter === 'mentions') {
      params.mention = identity;
    }

    // Scope filter from InboxHeader
    if (scopeFilter) {
      params.scope = scopeFilter;
    }

    return params;
  }, [identity, filter, scopeFilter]);

  const {
    messages,
    total,
    isLoading,
    hasMore,
    loadMore,
    isLoadingMore,
  } = useMessageListPaged(messageListParams);

  // Unread count for badge
  const unreadCount = messages.filter(m => m.is_read === false).length;

  const handleReply = (messageId: string, senderName: string) => {
    setReplyTo({ messageId, senderName });
  };

  const handleClearReply = () => {
    setReplyTo(undefined);
  };

  return (
    <div className="h-full flex flex-col">
      {/* Impersonation warning banner */}
      {isImpersonating && (
        <div
          className="flex items-center gap-2 px-4 py-2 bg-amber-50 dark:bg-amber-950 border-b border-amber-200 dark:border-amber-800 text-amber-800 dark:text-amber-200 text-sm"
          role="alert"
        >
          <AlertTriangle className="w-4 h-4 shrink-0" aria-hidden="true" />
          <span>
            Viewing as: <strong>{identity}</strong>
          </span>
        </div>
      )}

      <InboxHeader
        identity={identity}
        sendingAs={sendingAs}
        isImpersonating={isImpersonating}
        unreadCount={unreadCount}
        filter={filter}
        onFilterChange={setFilter}
        onScopeFilterChange={setScopeFilter}
        activeScopeFilter={scopeFilter}
      />

      <MessageList
        messages={messages}
        isLoading={isLoading}
        currentUserId={currentUser?.user_id}
        onReply={handleReply}
        totalCount={total}
        hasMore={hasMore}
        onLoadMore={loadMore}
        isLoadingMore={isLoadingMore}
        selectedMessageId={selectedMessageId}
        onClearSelectedMessage={onClearSelectedMessage}
      />

      <ComposeBar
        sendingAs={sendingAs}
        isImpersonating={isImpersonating}
        replyTo={replyTo}
        onClearReply={handleClearReply}
      />
    </div>
  );
}
