import { useStore } from '@tanstack/react-store';
import { AppShell } from '../components/AppShell';
import { LiveFeed } from '../components/feed/LiveFeed';
import { InboxView } from '../components/inbox/InboxView';
import { AgentContextPanel } from '../components/agents/AgentContextPanel';
import { WhoHasView } from '../components/coordination/WhoHasView';
import { uiStore } from '@thrum/shared-logic';

export function DashboardPage() {
  const { selectedView, selectedAgentId } = useStore(uiStore);

  return (
    <AppShell>
      {selectedView === 'live-feed' && <LiveFeed />}
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
    </AppShell>
  );
}
