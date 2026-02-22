import { useMemo, useState } from 'react';
import { useStore } from '@tanstack/react-store';
import { useQuery } from '@tanstack/react-query';
import { Plus, Layers } from 'lucide-react';
import { SidebarItem } from './SidebarItem';
import { AgentList } from './AgentList';
import {
  uiStore,
  selectLiveFeed,
  selectMyInbox,
  selectGroup,
  selectWhoHas,
  selectSettings,
  useGroupList,
  useCurrentUser,
  ensureConnected,
  wsClient,
} from '@thrum/shared-logic';
import type { Group, MessageListResponse } from '@thrum/shared-logic';
import { CreateGroupDialog } from './groups/CreateGroupDialog';
import { EmptyState } from '@/components/ui/EmptyState';

interface GroupSidebarItemProps {
  group: Group;
  active: boolean;
}

function GroupSidebarItem({ group, active }: GroupSidebarItemProps) {
  const currentUser = useCurrentUser();
  const request = {
    scope: { type: 'group', value: group.name },
    unread_for_agent: currentUser?.user_id,
    page_size: 1,
  };
  const { data } = useQuery({
    queryKey: ['messages', 'list', request],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<MessageListResponse>('message.list', request);
    },
    enabled: !!currentUser,
    staleTime: 60000,
    refetchInterval: 60000,
  });
  const unreadCount = data?.total ?? 0;

  return (
    <SidebarItem
      icon={<span>#</span>}
      label={`# ${group.name}`}
      active={active}
      badge={unreadCount}
      onClick={() => selectGroup(group.name)}
    />
  );
}

export function Sidebar() {
  const { selectedView, selectedGroupName } = useStore(uiStore);
  const { data: groupData, isLoading: groupsLoading } = useGroupList();
  const [createGroupOpen, setCreateGroupOpen] = useState(false);

  const sortedGroups = useMemo(() => {
    const groups = groupData?.groups ?? [];
    return [...groups].sort((a, b) => {
      if (a.name === 'everyone') return -1;
      if (b.name === 'everyone') return 1;
      return a.name.localeCompare(b.name);
    });
  }, [groupData]);

  return (
    <aside className="panel sidebar w-64 flex-shrink-0 flex flex-col p-6">
      <div className="logo">THRUM</div>

      <nav className="flex-1 overflow-y-auto">
        {/* Live Feed */}
        <SidebarItem
          icon={<span>●</span>}
          label="Live Feed"
          active={selectedView === 'live-feed'}
          onClick={selectLiveFeed}
        />

        {/* YOUR INBOX section */}
        <div className="my-2 border-t border-cyan-500/20" />
        <div className="px-3 py-1 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
          Your Inbox
        </div>
        <SidebarItem
          icon={<span>📥</span>}
          label="My Inbox"
          active={selectedView === 'my-inbox'}
          onClick={selectMyInbox}
        />

        {/* GROUPS section */}
        <div className="my-2 border-t border-cyan-500/20" />
        <div className="flex items-center justify-between px-3 py-1">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">
            Groups
          </span>
          <button
            type="button"
            onClick={() => setCreateGroupOpen(true)}
            className="h-4 w-4 flex items-center justify-center text-muted-foreground hover:text-cyan-300 transition-colors rounded"
            aria-label="Create group"
            data-testid="create-group-button"
          >
            <Plus className="h-3 w-3" />
          </button>
        </div>
        {groupsLoading ? (
          <div className="px-3 py-2 text-xs text-muted-foreground">Loading...</div>
        ) : sortedGroups.length === 0 ? (
          <EmptyState
            icon={<Layers className="h-5 w-5" />}
            title="No groups yet"
            action={{
              label: 'Create a group',
              onClick: () => setCreateGroupOpen(true),
            }}
          />
        ) : (
          <div className="space-y-1">
            {sortedGroups.map((group) => (
              <GroupSidebarItem
                key={group.group_id}
                group={group}
                active={selectedView === 'group-channel' && selectedGroupName === group.name}
              />
            ))}
          </div>
        )}

        {/* AGENTS section */}
        <div className="my-2 border-t border-cyan-500/20" />
        <AgentList />

        {/* TOOLS section */}
        <div className="my-2 border-t border-cyan-500/20" />
        <div className="px-3 py-1 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
          Tools
        </div>
        <SidebarItem
          icon={<span>🔍</span>}
          label="Who Has?"
          active={selectedView === 'who-has'}
          onClick={selectWhoHas}
        />
        <SidebarItem
          icon={<span>⚙</span>}
          label="Settings"
          active={selectedView === 'settings'}
          onClick={selectSettings}
        />
      </nav>

      <CreateGroupDialog
        open={createGroupOpen}
        onOpenChange={setCreateGroupOpen}
      />
    </aside>
  );
}
