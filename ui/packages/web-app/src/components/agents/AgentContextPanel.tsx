import { useAgentContext } from '@thrum/shared-logic';
import { formatRelativeTime } from '../../lib/time';
import { useState } from 'react';
import { AgentDeleteDialog } from './AgentDeleteDialog';

interface AgentContextPanelProps {
  agentId: string;
}

export function AgentContextPanel({ agentId }: AgentContextPanelProps) {
  const { data, isLoading } = useAgentContext({ agentId });

  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);

  if (isLoading) {
    return (
      <div className="agent-context-panel panel p-4 mb-4">
        <div className="space-y-2">
          <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/3" />
          <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/2" />
          <div className="h-4 bg-slate-700/50 rounded animate-pulse w-2/3" />
        </div>
      </div>
    );
  }

  // Show empty state if no contexts exist for this agent
  if (!data || data.length === 0) {
    const displayName = agentId.split(':').slice(-1)[0] || agentId;
    return (
      <div className="agent-context-panel panel p-4 mb-4">
        <div className="text-center py-6">
          <p className="text-muted-foreground text-sm">No active session</p>
        </div>
        <div className="mt-4 pt-4 border-t border-slate-700">
          <button
            onClick={() => setDeleteDialogOpen(true)}
            className="w-full px-4 py-2 bg-red-600/10 hover:bg-red-600/20 text-red-400 rounded border border-red-600/30 transition-colors"
            data-testid="open-delete-dialog"
          >
            Delete Agent
          </button>
        </div>
        <AgentDeleteDialog
          open={deleteDialogOpen}
          onOpenChange={setDeleteDialogOpen}
          agentName={displayName}
          agentId={agentId}
        />
      </div>
    );
  }

  // Display the first (most recent) context
  const context = data[0]!;

  const displayName = context.agent_id.split(':').slice(-1)[0] || context.agent_id;

  return (
    <div className="agent-context-panel panel p-4 mb-4">
      <div className="context-grid">
        <div className="context-row">
          <span className="context-label">AGENT</span>
          <span className="context-value">{displayName}</span>
        </div>
        <div className="context-row">
          <span className="context-label">AGENT ID</span>
          <span className="context-value">{context.agent_id}</span>
        </div>
        {context.intent && (
          <div className="context-row">
            <span className="context-label">INTENT</span>
            <span className="context-value">{context.intent}</span>
          </div>
        )}
        {context.current_task && (
          <div className="context-row">
            <span className="context-label">TASK</span>
            <span className="context-value">{context.current_task}</span>
          </div>
        )}
        <div className="context-row">
          <span className="context-label">BRANCH</span>
          <span className="context-value">{context.branch}</span>
        </div>
        <div className="context-row">
          <span className="context-label">UNCOMMITTED</span>
          <span className="context-value">{context.uncommitted_files?.length ?? 0} files</span>
        </div>
        <div className="context-row">
          <span className="context-label">CHANGED</span>
          <span className="context-value">{context.changed_files?.length ?? 0} files</span>
        </div>
        <div className="context-row">
          <span className="context-label">HEARTBEAT</span>
          <span className="context-value">{formatRelativeTime(context.git_updated_at)}</span>
        </div>
      </div>

      {/* Delete button */}
      <div className="mt-4 pt-4 border-t border-slate-700">
        <button
          onClick={() => setDeleteDialogOpen(true)}
          className="w-full px-4 py-2 bg-red-600/10 hover:bg-red-600/20 text-red-400 rounded border border-red-600/30 transition-colors"
          data-testid="open-delete-dialog"
        >
          Delete Agent
        </button>
      </div>

      <AgentDeleteDialog
        open={deleteDialogOpen}
        onOpenChange={setDeleteDialogOpen}
        agentName={displayName}
        agentId={agentId}
      />
    </div>
  );
}
