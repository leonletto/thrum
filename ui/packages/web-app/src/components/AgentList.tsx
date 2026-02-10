import { useMemo } from 'react';
import { useStore } from '@tanstack/react-store';
import { AgentCard } from './agents/AgentCard';
import { AgentListSkeleton } from './agents/AgentListSkeleton';
import { useAgentList, uiStore, selectAgent } from '@thrum/shared-logic';

export function AgentList() {
  const { data, isLoading } = useAgentList();
  const { selectedView, selectedAgentId } = useStore(uiStore);

  const agents = data?.agents || [];

  // Sort by last_seen_at (most recent first)
  const sortedAgents = useMemo(() => {
    return [...agents].sort((a, b) => {
      const aTime = a.last_seen_at ? new Date(a.last_seen_at).getTime() : 0;
      const bTime = b.last_seen_at ? new Date(b.last_seen_at).getTime() : 0;
      return bTime - aTime;
    });
  }, [agents]);

  if (isLoading) {
    return <AgentListSkeleton />;
  }

  return (
    <div className="space-y-1">
      <h3 className="text-xs font-semibold text-muted-foreground uppercase px-3 py-2">
        Agents ({sortedAgents.length})
      </h3>
      {sortedAgents.map((agent) => (
        <AgentCard
          key={agent.agent_id}
          agent={agent}
          active={
            selectedView === 'agent-inbox' && selectedAgentId === agent.agent_id
          }
          onClick={() => selectAgent(agent.agent_id)}
        />
      ))}
    </div>
  );
}
