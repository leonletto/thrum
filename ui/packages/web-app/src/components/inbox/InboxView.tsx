import { useMemo, useState } from 'react';
import { useThreadList, useCurrentUser, type MessageScope } from '@thrum/shared-logic';
import { ScrollArea } from '@/components/ui/scroll-area';
import { InboxHeader, type InboxFilter } from './InboxHeader';
import { ThreadList } from './ThreadList';
import { ThreadListSkeleton } from './ThreadListSkeleton';

interface InboxViewProps {
  /**
   * Optional identity ID to show inbox for specific agent.
   * If not provided, shows the current user's inbox.
   */
  identityId?: string;
}

export function InboxView({ identityId }: InboxViewProps) {
  const currentUser = useCurrentUser();
  const [scopeFilter, setScopeFilter] = useState<MessageScope | null>(null);
  const { data, isLoading } = useThreadList(
    scopeFilter ? { scope: scopeFilter } : undefined
  );
  const [filter, setFilter] = useState<InboxFilter>('all');

  // Determine the identity whose inbox we're viewing
  const identity = identityId || currentUser?.username || 'Unknown';

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

  // Filter threads based on filter selection
  const filteredThreads = useMemo(() => {
    if (!data?.threads) return [];
    if (filter === 'unread') {
      return data.threads.filter((thread) => (thread.unread_count || 0) > 0);
    }
    if (filter === 'mentions') {
      // TODO: Implement mentions filter when backend supports it
      return [];
    }
    return data.threads;
  }, [data?.threads, filter]);

  // Calculate total unread count across all threads
  const totalUnreadCount = useMemo(() => {
    if (!data?.threads) return 0;
    return data.threads.reduce((sum, thread) => sum + (thread.unread_count || 0), 0);
  }, [data?.threads]);

  if (isLoading) {
    return (
      <div className="h-full flex flex-col">
        <InboxHeader
          identity={identity}
          sendingAs={sendingAs}
          isImpersonating={isImpersonating}
          unreadCount={0}
          filter={filter}
          onFilterChange={setFilter}
          onScopeFilterChange={setScopeFilter}
          activeScopeFilter={scopeFilter}
        />
        <ScrollArea className="flex-1">
          <div className="p-4">
            <ThreadListSkeleton />
          </div>
        </ScrollArea>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      <InboxHeader
        identity={identity}
        sendingAs={sendingAs}
        isImpersonating={isImpersonating}
        unreadCount={totalUnreadCount}
        filter={filter}
        onFilterChange={setFilter}
        onScopeFilterChange={setScopeFilter}
        activeScopeFilter={scopeFilter}
      />

      <ScrollArea className="flex-1">
        <ThreadList
          threads={filteredThreads}
          sendingAs={sendingAs}
          isImpersonating={isImpersonating}
        />
      </ScrollArea>
    </div>
  );
}
