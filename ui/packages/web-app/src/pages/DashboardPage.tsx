import { useStore } from '@tanstack/react-store';
import { AppShell } from '../components/AppShell';
import { FeedView } from '../components/feed/FeedView';
import { InboxView } from '../components/inbox/InboxView';
import { ConversationLayout } from '../components/conversation/ConversationLayout';
import { AgentContextPanel } from '../components/agents/AgentContextPanel';
import { WhoHasView } from '../components/coordination/WhoHasView';
import { SettingsView } from '../components/settings/SettingsView';
import { GroupChannelView } from '../components/groups/GroupChannelView';
import { uiStore, useRealtimeMessages, setSelectedMessageId, useCurrentUser } from '@thrum/shared-logic';

export function DashboardPage() {
  const { selectedView, selectedAgentId, selectedGroupName, selectedMessageId } = useStore(uiStore);
  const currentUser = useCurrentUser();

  // Subscribe to real-time WebSocket events for cache invalidation
  useRealtimeMessages();

  const currentAgentId = currentUser?.username || currentUser?.user_id || '';

  return (
    <AppShell>
      {selectedView === 'live-feed' && <FeedView />}
      {selectedView === 'conversations' && currentAgentId && (
        <ConversationLayout currentAgentId={currentAgentId} />
      )}
      {selectedView === 'my-inbox' && (
        <InboxView
          selectedMessageId={selectedMessageId}
          onClearSelectedMessage={() => setSelectedMessageId(null)}
        />
      )}
      {selectedView === 'agent-inbox' && selectedAgentId && (
        <div className="h-full flex flex-col">
          <div className="flex-none px-4 pt-4">
            <AgentContextPanel agentId={selectedAgentId} />
          </div>
          <div className="flex-1 min-h-0">
            <InboxView
              identityId={selectedAgentId}
              selectedMessageId={selectedMessageId}
              onClearSelectedMessage={() => setSelectedMessageId(null)}
            />
          </div>
        </div>
      )}
      {selectedView === 'who-has' && <WhoHasView />}
      {selectedView === 'settings' && <SettingsView />}
      {selectedView === 'group-channel' && selectedGroupName && (
        <GroupChannelView
          groupName={selectedGroupName}
          selectedMessageId={selectedMessageId}
          onClearSelectedMessage={() => setSelectedMessageId(null)}
        />
      )}
    </AppShell>
  );
}
