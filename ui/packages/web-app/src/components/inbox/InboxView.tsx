import { useMemo, useState } from 'react';
import { useCurrentUser, type MessageScope } from '@thrum/shared-logic';
import { ScrollArea } from '@/components/ui/scroll-area';
import { InboxHeader, type InboxFilter } from './InboxHeader';

interface InboxViewProps {
  /**
   * Optional identity ID to show inbox for specific agent.
   * If not provided, shows the current user's inbox.
   */
  identityId?: string;
}

export function InboxView({ identityId }: InboxViewProps) {
  const currentUser = useCurrentUser();
  const [, setScopeFilter] = useState<MessageScope | null>(null);
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
        activeScopeFilter={null}
      />

      <ScrollArea className="flex-1">
        <div className="flex items-center justify-center h-64">
          <div className="empty-state">
            <div className="empty-icon">○</div>
            <div className="empty-text">NO THREADS</div>
            <div className="empty-subtext">Start a conversation</div>
          </div>
        </div>
      </ScrollArea>
    </div>
  );
}
