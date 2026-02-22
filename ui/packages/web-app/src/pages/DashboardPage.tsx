import { useStore } from '@tanstack/react-store';
import { AppShell } from '../components/AppShell';
import { FeedView } from '../components/feed/FeedView';
import { InboxView } from '../components/inbox/InboxView';
import { AgentContextPanel } from '../components/agents/AgentContextPanel';
import { WhoHasView } from '../components/coordination/WhoHasView';
import { GroupChannelView } from '../components/groups/GroupChannelView';
import { uiStore, useRealtimeMessages, useBrowserNotifications, useCurrentUser } from '@thrum/shared-logic';

export function DashboardPage() {
  const { selectedView, selectedAgentId, selectedGroupName } = useStore(uiStore);
  const currentUser = useCurrentUser();

  // Subscribe to real-time WebSocket events for cache invalidation
  useRealtimeMessages();

  // Enable browser notifications for mentions and DMs
  useBrowserNotifications(currentUser?.user_id, 'Thrum');

  return (
    <AppShell>
      {selectedView === 'live-feed' && <FeedView />}
      {selectedView === 'my-inbox' && <InboxView />}
      {selectedView === 'agent-inbox' && selectedAgentId && (
        <div className="h-full flex flex-col">
          <div className="flex-none px-4 pt-4">
            <AgentContextPanel agentId={selectedAgentId} />
          </div>
          <div className="flex-1 min-h-0">
            <InboxView identityId={selectedAgentId} />
          </div>
        </div>
      )}
      {selectedView === 'who-has' && <WhoHasView />}
      {selectedView === 'group-channel' && selectedGroupName && (
        <GroupChannelView groupName={selectedGroupName} />
      )}
    </AppShell>
  );
}
