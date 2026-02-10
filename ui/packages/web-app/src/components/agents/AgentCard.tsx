import { cn } from '../../lib/utils';
import { StatusIndicator } from './StatusIndicator';
import type { Agent } from '@thrum/shared-logic';

interface AgentCardProps {
  agent: Agent;
  active: boolean;
  onClick: () => void;
}

export function AgentCard({ agent, active, onClick }: AgentCardProps) {
  // Determine status based on last_seen_at
  const now = new Date().getTime();
  const lastSeen = agent.last_seen_at ? new Date(agent.last_seen_at).getTime() : 0;
  const minutesSinceLastSeen = (now - lastSeen) / 60000;
  const status = minutesSinceLastSeen < 5 ? 'online' : 'offline';

  const displayName = agent.display || agent.agent_id;

  return (
    <button
      onClick={onClick}
      className={cn('agent-item w-full text-left', active && 'ring-2 ring-cyan-500')}
    >
      <div className="agent-name">
        <StatusIndicator status={status} />
        <span className="truncate">{displayName}</span>
      </div>
    </button>
  );
}
