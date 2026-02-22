import { useState, useMemo } from 'react';
import { ChevronDown, Activity } from 'lucide-react';
import { useMessageList, useSessionList, useAgentList, useCurrentUser, selectAgent, selectGroup, selectMyInbox } from '@thrum/shared-logic';
import type { Message, MessageScope, Session, Agent } from '@thrum/shared-logic';
import { ScrollArea } from '@/components/ui/scroll-area';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { EmptyState } from '@/components/ui/EmptyState';
import { formatRelativeTime } from '../../lib/time';

// ─── Unified feed item types ────────────────────────────────────────────────

type FeedItemType = 'message' | 'agent_registered' | 'session_started' | 'session_ended';

type FilterOption = 'all' | 'messages' | 'agent_events';

interface UnifiedFeedItem {
  id: string;
  type: FeedItemType;
  timestamp: string;
  // For messages
  from?: string;
  to?: string;
  preview?: string;
  messageId?: string;
  scope?: { type: string; value: string };
  // For sessions
  agentId?: string;
  sessionId?: string;
  // For agent registration
  agentName?: string;
  role?: string;
}

// ─── Transform helpers ───────────────────────────────────────────────────────

function transformMessage(message: Message): UnifiedFeedItem {
  const toScope = message.scopes?.find((s: MessageScope) => s.type === 'to');
  const groupScope = message.scopes?.find((s: MessageScope) => s.type === 'group');
  return {
    id: `msg-${message.message_id}`,
    type: 'message',
    timestamp: message.created_at,
    from: message.agent_id,
    to: toScope?.value,
    preview: message.body.content || message.body.structured || '',
    messageId: message.message_id,
    scope: groupScope,
  };
}

function transformSessionStarted(session: Session): UnifiedFeedItem {
  return {
    id: `ses-start-${session.session_id}`,
    type: 'session_started',
    timestamp: session.started_at,
    agentId: session.agent_id,
    sessionId: session.session_id,
  };
}

function transformSessionEnded(session: Session): UnifiedFeedItem | null {
  if (!session.ended_at) return null;
  return {
    id: `ses-end-${session.session_id}`,
    type: 'session_ended',
    timestamp: session.ended_at,
    agentId: session.agent_id,
    sessionId: session.session_id,
  };
}

function transformAgentRegistered(agent: Agent): UnifiedFeedItem {
  return {
    id: `agent-reg-${agent.agent_id}`,
    type: 'agent_registered',
    timestamp: agent.registered_at,
    agentId: agent.agent_id,
    agentName: agent.display || agent.agent_id,
    role: agent.role,
  };
}

// ─── Individual row renderers ────────────────────────────────────────────────

interface FeedRowProps {
  item: UnifiedFeedItem;
  onClick: (item: UnifiedFeedItem) => void;
  getDisplayName: (agentId: string | undefined) => string | undefined;
}

function FeedRow({ item, onClick, getDisplayName }: FeedRowProps) {
  const baseClass =
    'w-full px-3 py-2 rounded-md hover:bg-accent/50 text-left flex items-start gap-2 text-sm';

  const handleClick = () => onClick(item);

  if (item.type === 'message') {
    const fromDisplay = getDisplayName(item.from) || item.from;
    const toDisplay = getDisplayName(item.to) || item.to;

    return (
      <button onClick={handleClick} className={baseClass}>
        <span className="flex-1 min-w-0">
          <span className="font-medium">{fromDisplay}</span>
          <span className="text-muted-foreground mx-1">→</span>
          <span className="font-medium">{toDisplay}</span>
          {item.preview && (
            <span className="text-muted-foreground">: {item.preview}</span>
          )}
        </span>
        <span className="text-xs text-muted-foreground shrink-0 mt-0.5">
          {formatRelativeTime(item.timestamp)}
        </span>
      </button>
    );
  }

  if (item.type === 'session_started') {
    const agentDisplay = getDisplayName(item.agentId) || item.agentId;
    return (
      <button onClick={handleClick} className={baseClass}>
        <span className="shrink-0">▶</span>
        <span className="flex-1 min-w-0 text-muted-foreground">
          <span className="font-medium text-foreground">{agentDisplay}</span>
          {' '}started session
        </span>
        <span className="text-xs text-muted-foreground shrink-0 mt-0.5">
          {formatRelativeTime(item.timestamp)}
        </span>
      </button>
    );
  }

  if (item.type === 'session_ended') {
    const agentDisplay = getDisplayName(item.agentId) || item.agentId;
    return (
      <button onClick={handleClick} className={baseClass}>
        <span className="shrink-0">⏹</span>
        <span className="flex-1 min-w-0 text-muted-foreground">
          <span className="font-medium text-foreground">{agentDisplay}</span>
          {' '}ended session
        </span>
        <span className="text-xs text-muted-foreground shrink-0 mt-0.5">
          {formatRelativeTime(item.timestamp)}
        </span>
      </button>
    );
  }

  if (item.type === 'agent_registered') {
    return (
      <button onClick={handleClick} className={baseClass}>
        <span className="shrink-0">🔵</span>
        <span className="flex-1 min-w-0 text-muted-foreground">
          <span className="font-medium text-foreground">{item.agentName}</span>
          {' '}registered
          {item.role && <span> ({item.role})</span>}
        </span>
        <span className="text-xs text-muted-foreground shrink-0 mt-0.5">
          {formatRelativeTime(item.timestamp)}
        </span>
      </button>
    );
  }

  return null;
}

// ─── Filter dropdown ─────────────────────────────────────────────────────────

const FILTER_LABELS: Record<FilterOption, string> = {
  all: 'All',
  messages: 'Messages only',
  agent_events: 'Agent events only',
};

interface FilterDropdownProps {
  value: FilterOption;
  onChange: (value: FilterOption) => void;
}

function FilterDropdown({ value, onChange }: FilterDropdownProps) {
  const [open, setOpen] = useState(false);

  return (
    <div className="relative">
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen((o) => !o)}
        className="text-xs gap-1"
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        Filter: {FILTER_LABELS[value]}
        <ChevronDown className="h-3 w-3" />
      </Button>
      {open && (
        <div
          role="listbox"
          className="absolute right-0 top-full mt-1 z-10 bg-popover border border-border rounded-md shadow-md min-w-[160px] py-1"
        >
          {(Object.entries(FILTER_LABELS) as [FilterOption, string][]).map(
            ([key, label]) => (
              <button
                key={key}
                role="option"
                aria-selected={value === key}
                className="w-full px-3 py-1.5 text-sm text-left hover:bg-accent/50 data-[selected=true]:font-medium"
                data-selected={value === key}
                onClick={() => {
                  onChange(key);
                  setOpen(false);
                }}
              >
                {label}
              </button>
            )
          )}
        </div>
      )}
    </div>
  );
}

// ─── Main FeedView component ─────────────────────────────────────────────────

export function FeedView() {
  const [filter, setFilter] = useState<FilterOption>('all');
  const currentUser = useCurrentUser();

  const {
    data: messageData,
    isLoading: messagesLoading,
  } = useMessageList({ page_size: 50, sort_order: 'desc' });

  const {
    data: sessionData,
    isLoading: sessionsLoading,
  } = useSessionList();

  const {
    data: agentData,
    isLoading: agentsLoading,
  } = useAgentList();

  const isLoading = messagesLoading || sessionsLoading || agentsLoading;

  // Build a lookup for display names
  const agentLookup = useMemo(() => {
    const map = new Map<string, string>();
    for (const agent of agentData?.agents ?? []) {
      if (agent.display) map.set(agent.agent_id, agent.display);
    }
    return map;
  }, [agentData]);

  const getDisplayName = (agentId: string | undefined) => {
    if (!agentId) return undefined;
    return agentLookup.get(agentId) || agentId;
  };

  // Merge all sources into a single sorted feed
  const allItems = useMemo<UnifiedFeedItem[]>(() => {
    const items: UnifiedFeedItem[] = [];

    // Messages
    for (const msg of messageData?.messages ?? []) {
      items.push(transformMessage(msg));
    }

    // Session events
    for (const session of sessionData?.sessions ?? []) {
      items.push(transformSessionStarted(session));
      const ended = transformSessionEnded(session);
      if (ended) items.push(ended);
    }

    // Agent registrations
    for (const agent of agentData?.agents ?? []) {
      items.push(transformAgentRegistered(agent));
    }

    // Sort descending by timestamp
    items.sort(
      (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime()
    );

    return items;
  }, [messageData, sessionData, agentData]);

  // Apply filter
  const filteredItems = useMemo<UnifiedFeedItem[]>(() => {
    if (filter === 'messages') {
      return allItems.filter((item) => item.type === 'message');
    }
    if (filter === 'agent_events') {
      return allItems.filter(
        (item) =>
          item.type === 'agent_registered' ||
          item.type === 'session_started' ||
          item.type === 'session_ended'
      );
    }
    return allItems;
  }, [allItems, filter]);

  const handleItemClick = (item: UnifiedFeedItem) => {
    if (item.type === 'message') {
      if (item.scope?.type === 'group') {
        selectGroup(item.scope.value);
      } else if (item.from && item.from === currentUser?.user_id) {
        selectMyInbox();
      } else if (item.from) {
        selectAgent(item.from);
      }
    } else if (item.type === 'session_started' || item.type === 'session_ended') {
      if (item.agentId) {
        selectAgent(item.agentId);
      }
    } else if (item.type === 'agent_registered') {
      if (item.agentName) {
        selectAgent(item.agentName);
      }
    }
  };

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="p-4 border-b flex items-center justify-between shrink-0">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
          Activity Feed
        </h2>
        <FilterDropdown value={filter} onChange={setFilter} />
      </div>

      {/* Body */}
      {isLoading ? (
        <div className="flex-1 p-2 space-y-1" role="region" aria-label="Loading feed" aria-busy="true">
          {[...Array(6)].map((_, i) => (
            <div key={i} className="flex items-start gap-2 px-3 py-2">
              <Skeleton className="h-4 w-24 shrink-0" />
              <Skeleton className="h-4 w-4 shrink-0" />
              <Skeleton className="h-4 w-20 shrink-0" />
              <Skeleton className="h-4 flex-1" />
              <Skeleton className="h-3 w-10 shrink-0" />
            </div>
          ))}
        </div>
      ) : filteredItems.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <EmptyState
            icon={<Activity className="h-8 w-8" />}
            title="No activity yet"
            description="Agent messages, session events, and registrations will appear here"
          />
        </div>
      ) : (
        <ScrollArea className="flex-1">
          <div className="p-2 space-y-0.5">
            {filteredItems.map((item) => (
              <FeedRow
                key={item.id}
                item={item}
                onClick={handleItemClick}
                getDisplayName={getDisplayName}
              />
            ))}
          </div>
        </ScrollArea>
      )}
    </div>
  );
}
