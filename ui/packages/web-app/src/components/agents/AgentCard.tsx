import { useQuery } from '@tanstack/react-query';
import { cn } from '../../lib/utils';
import { StatusIndicator } from './StatusIndicator';
import { useCurrentUser, ensureConnected, wsClient } from '@thrum/shared-logic';
import type { Agent, MessageListResponse } from '@thrum/shared-logic';

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

  const currentUser = useCurrentUser();
  const request = {
    for_agent: agent.agent_id,
    unread_for_agent: currentUser?.user_id,
    page_size: 1,
  };
  const { data } = useQuery({
    queryKey: ['messages', 'list', request],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<MessageListResponse>('message.list', request);
    },
    staleTime: 60000,
    refetchInterval: 60000,
  });
  const unreadCount = data?.total ?? 0;

  return (
    <button
      onClick={onClick}
      className={cn('agent-item w-full text-left', active && 'ring-2 ring-cyan-500')}
    >
      <div className="agent-name">
        <StatusIndicator status={status} />
        <span className="truncate" title={displayName}>{displayName}</span>
        {unreadCount > 0 && (
          <span className="px-2 py-0.5 text-xs rounded-full bg-red-500 text-white font-mono">
            {unreadCount}
          </span>
        )}
      </div>
    </button>
  );
}
