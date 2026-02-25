import { useAgentContext } from '@thrum/shared-logic';
import { formatRelativeTime } from '../../lib/time';
import { useState } from 'react';
import { Settings, X, Trash2 } from 'lucide-react';
import { AgentDeleteDialog } from './AgentDeleteDialog';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';

interface AgentContextPanelProps {
  agentId: string;
}

/** Small coloured dot indicating online/offline */
function StatusDot({ online }: { online: boolean }) {
  return (
    <span
      className={`inline-block h-2 w-2 rounded-full shrink-0 ${
        online
          ? 'bg-[var(--status-online,#22c55e)]'
          : 'bg-[var(--status-offline,#6b7280)]'
      }`}
      aria-label={online ? 'online' : 'offline'}
    />
  );
}

export function AgentContextPanel({ agentId }: AgentContextPanelProps) {
  const { data, isLoading } = useAgentContext({ agentId });
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);

  // Derive display name from agentId as a safe fallback
  const fallbackDisplayName = agentId.split(':').slice(-1)[0] || agentId;

  // ── Loading skeleton ──────────────────────────────────────────────────────
  if (isLoading) {
    return (
      <div
        className="agent-context-panel flex items-center gap-2 px-4 h-10 border-b border-[var(--accent-border)] bg-[var(--panel-bg-start)] animate-pulse"
        data-testid="agent-context-loading"
      >
        <div className="h-2 w-2 rounded-full bg-muted shrink-0" />
        <div className="h-3 bg-muted rounded w-24" />
        <div className="h-4 bg-muted rounded w-16" />
        <div className="h-3 bg-muted rounded flex-1" />
      </div>
    );
  }

  // ── No active session ─────────────────────────────────────────────────────
  if (!data || data.length === 0) {
    return (
      <div className="agent-context-panel relative">
        {/* Thin header bar */}
        <div
          className="flex items-center gap-2 px-4 h-10 border-b border-[var(--accent-border)] bg-[var(--panel-bg-start)]"
          data-testid="agent-context-header"
        >
          <StatusDot online={false} />
          <span className="font-semibold text-sm text-[var(--text-secondary)] truncate">
            {fallbackDisplayName}
          </span>
          <span className="text-xs text-[var(--text-faint)] italic flex-1 truncate">
            No active session
          </span>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setDetailsOpen(true)}
            className="h-7 w-7 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)] shrink-0"
            aria-label="Agent settings"
            data-testid="agent-settings-button"
          >
            <Settings className="h-3.5 w-3.5" />
          </Button>
        </div>

        {/* Slide-out details panel */}
        {detailsOpen && (
          <div
            className="absolute inset-y-0 right-0 w-72 bg-[var(--panel-bg-start)] border-l border-[var(--accent-border)] flex flex-col z-20 shadow-xl"
            data-testid="agent-details-panel"
            role="dialog"
            aria-label="Agent details"
          >
            <div className="flex items-center justify-between px-4 py-3 border-b border-[var(--accent-border)] shrink-0">
              <div className="flex items-center gap-2">
                <Settings className="h-4 w-4 text-[var(--accent-color)]" />
                <span className="text-sm font-semibold text-[var(--text-secondary)]">
                  Agent Details
                </span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setDetailsOpen(false)}
                className="h-6 w-6 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
                aria-label="Close agent details panel"
              >
                <X className="h-3.5 w-3.5" />
              </Button>
            </div>

            <div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
              <DetailRow label="AGENT ID" value={agentId} />
              <p className="text-xs text-[var(--text-faint)] italic">No active session</p>
            </div>

            <div className="shrink-0 border-t border-[var(--accent-border)] px-4 py-3">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setDetailsOpen(false);
                  setDeleteDialogOpen(true);
                }}
                className="w-full h-7 text-xs text-[rgb(var(--destructive))] hover:text-[rgb(var(--destructive-dark))] hover:bg-[rgb(var(--destructive))]/10 justify-start"
                data-testid="open-delete-dialog"
              >
                <Trash2 className="h-3 w-3 mr-1.5" />
                Delete Agent
              </Button>
            </div>
          </div>
        )}

        <AgentDeleteDialog
          open={deleteDialogOpen}
          onOpenChange={setDeleteDialogOpen}
          agentName={fallbackDisplayName}
          agentId={agentId}
        />
      </div>
    );
  }

  // ── Active session ────────────────────────────────────────────────────────
  const context = data[0]!;
  const displayName = context.agent_id.split(':').slice(-1)[0] || context.agent_id;

  // Determine online status: seen within last 5 minutes
  const lastSeenMs = context.git_updated_at
    ? new Date(context.git_updated_at).getTime()
    : 0;
  const isOnline = Date.now() - lastSeenMs < 5 * 60 * 1000;

  // Role badge comes from agent_id pattern (e.g. "role_hash:role:module")
  const rolePart = context.agent_id.split(':')[1] ?? '';

  return (
    <div className="agent-context-panel relative">
      {/* ── Thin header bar (~40px) ── */}
      <div
        className="flex items-center gap-2 px-4 h-10 border-b border-[var(--accent-border)] bg-[var(--panel-bg-start)]"
        data-testid="agent-context-header"
      >
        <StatusDot online={isOnline} />

        <span className="font-semibold text-sm text-[var(--text-secondary)] truncate shrink-0">
          {displayName}
        </span>

        {rolePart && (
          <Badge
            variant="secondary"
            className="shrink-0 text-[10px] px-1.5 py-0 h-4 bg-[var(--accent-subtle-bg-hover)] border border-[var(--accent-border)] text-[var(--accent-color)]"
          >
            {rolePart}
          </Badge>
        )}

        {context.intent && (
          <span className="text-xs text-[var(--text-faint)] flex-1 truncate">
            {context.intent}
          </span>
        )}

        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setDetailsOpen(true)}
          className="h-7 w-7 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)] shrink-0 ml-auto"
          aria-label="Agent settings"
          data-testid="agent-settings-button"
        >
          <Settings className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* ── Slide-out details panel ── */}
      {detailsOpen && (
        <div
          className="absolute inset-y-0 right-0 w-72 bg-[var(--panel-bg-start)] border-l border-[var(--accent-border)] flex flex-col z-20 shadow-xl"
          data-testid="agent-details-panel"
          role="dialog"
          aria-label="Agent details"
        >
          {/* Panel header */}
          <div className="flex items-center justify-between px-4 py-3 border-b border-[var(--accent-border)] shrink-0">
            <div className="flex items-center gap-2">
              <Settings className="h-4 w-4 text-[var(--accent-color)]" />
              <span className="text-sm font-semibold text-[var(--text-secondary)]">
                Agent Details
              </span>
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setDetailsOpen(false)}
              className="h-6 w-6 p-0 text-[var(--text-muted)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
              aria-label="Close agent details panel"
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>

          {/* Scrollable details */}
          <div className="flex-1 overflow-y-auto px-4 py-3 space-y-3">
            <DetailRow label="AGENT ID" value={context.agent_id} />
            {rolePart && <DetailRow label="ROLE" value={rolePart} />}
            {context.intent && <DetailRow label="INTENT" value={context.intent} />}
            {context.current_task && <DetailRow label="TASK" value={context.current_task} />}
            <DetailRow label="BRANCH" value={context.branch} />

            {/* Uncommitted files */}
            <div>
              <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-1">
                UNCOMMITTED ({context.uncommitted_files?.length ?? 0} files)
              </div>
              {context.uncommitted_files && context.uncommitted_files.length > 0 ? (
                <ul className="space-y-0.5">
                  {context.uncommitted_files.map((f, i) => (
                    <li
                      key={i}
                      className="text-xs font-mono text-[var(--accent-color)] truncate"
                    >
                      {f}
                    </li>
                  ))}
                </ul>
              ) : (
                <div className="text-xs text-[var(--text-faint)] italic">none</div>
              )}
            </div>

            <DetailRow
              label="CHANGED"
              value={`${context.changed_files?.length ?? 0} files`}
            />
            <DetailRow
              label="HEARTBEAT"
              value={formatRelativeTime(context.git_updated_at)}
            />
          </div>

          {/* Delete button at bottom */}
          <div className="shrink-0 border-t border-[var(--accent-border)] px-4 py-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setDetailsOpen(false);
                setDeleteDialogOpen(true);
              }}
              className="w-full h-7 text-xs text-[rgb(var(--destructive))] hover:text-[rgb(var(--destructive-dark))] hover:bg-[rgb(var(--destructive))]/10 justify-start"
              data-testid="open-delete-dialog"
            >
              <Trash2 className="h-3 w-3 mr-1.5" />
              Delete Agent
            </Button>
          </div>
        </div>
      )}

      <AgentDeleteDialog
        open={deleteDialogOpen}
        onOpenChange={setDeleteDialogOpen}
        agentName={displayName}
        agentId={agentId}
      />
    </div>
  );
}

/** Small label+value row used inside the slide-out panel */
function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[10px] text-[var(--text-faint)] uppercase tracking-wider mb-0.5">
        {label}
      </div>
      <div className="text-xs text-[var(--text-secondary)] font-mono break-all">
        {value}
      </div>
    </div>
  );
}
