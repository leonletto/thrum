import { useStore } from '@tanstack/react-store';
import { SidebarItem } from './SidebarItem';
import { AgentList } from './AgentList';
import { uiStore, selectLiveFeed, selectMyInbox, selectWhoHas } from '@thrum/shared-logic';

export function Sidebar() {
  const { selectedView } = useStore(uiStore);

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

        <div className="my-2 border-t border-cyan-500/20" />

        {/* My Inbox */}
        <SidebarItem
          icon={<span>📥</span>}
          label="My Inbox"
          active={selectedView === 'my-inbox'}
          onClick={selectMyInbox}
        />

        <div className="my-2 border-t border-cyan-500/20" />

        {/* Who Has? */}
        <SidebarItem
          icon={<span>🔍</span>}
          label="Who Has?"
          active={selectedView === 'who-has'}
          onClick={selectWhoHas}
        />

        <div className="my-2 border-t border-cyan-500/20" />

        {/* Agent List */}
        <AgentList />
      </nav>
    </aside>
  );
}
