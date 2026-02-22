import { useMemo, useState } from 'react';
import { Settings } from 'lucide-react';
import { Sidebar } from './Sidebar';
import { ThemeToggle } from './ThemeToggle';
import { SkipLink } from './SkipLink';
import { HealthBar } from './status/HealthBar';
import { SubscriptionPanel } from './subscriptions/SubscriptionPanel';
import { useAuth } from './AuthProvider';
import { useAgentList, useGroupList } from '@thrum/shared-logic';
import { useKeyboardShortcuts } from '../hooks/useKeyboardShortcuts';

interface AppShellProps {
  children: React.ReactNode;
}

export function AppShell({ children }: AppShellProps) {
  const [subscriptionPanelOpen, setSubscriptionPanelOpen] = useState(false);
  const { user, isLoading } = useAuth();

  // Fetch groups and agents so keyboard shortcuts can navigate to first items
  const { data: groupData } = useGroupList();
  const { data: agentData } = useAgentList();

  const firstGroupName = useMemo(() => {
    const groups = groupData?.groups ?? [];
    if (groups.length === 0) return null;
    // Mirror Sidebar sort: 'everyone' first, then alphabetical
    const sorted = [...groups].sort((a, b) => {
      if (a.name === 'everyone') return -1;
      if (b.name === 'everyone') return 1;
      return a.name.localeCompare(b.name);
    });
    return sorted[0]?.name ?? null;
  }, [groupData]);

  const firstAgentId = useMemo(() => {
    const agents = agentData?.agents ?? [];
    if (agents.length === 0) return null;
    // Mirror AgentList sort: most recently seen first
    const sorted = [...agents].sort((a, b) => {
      const aTime = a.last_seen_at ? new Date(a.last_seen_at).getTime() : 0;
      const bTime = b.last_seen_at ? new Date(b.last_seen_at).getTime() : 0;
      return bTime - aTime;
    });
    return sorted[0]?.agent_id ?? null;
  }, [agentData]);

  // Register global keyboard shortcuts
  useKeyboardShortcuts({ firstGroupName, firstAgentId });

  const userDisplay = isLoading
    ? '...'
    : user
      ? `${user.display_name ?? user.username}`
      : 'not connected';

  return (
    <div className="flex h-screen overflow-hidden bg-[#0a0e1a]">
      <SkipLink />
      <header className="panel header fixed top-0 left-0 right-0 h-14 z-10 flex items-center justify-between px-6">
        <div className="header-title">Thrum</div>
        <div className="flex items-center gap-3">
          <span className="text-sm text-muted-foreground">{userDisplay} {user ? '\u{1F7E2}' : '\u{1F534}'}</span>
          <ThemeToggle />
          <button
            className="text-muted-foreground hover:text-foreground"
            aria-label="Settings"
            onClick={() => setSubscriptionPanelOpen(true)}
          >
            <Settings className="h-5 w-5" />
          </button>
        </div>
      </header>

      <div className="flex pt-14 w-full">
        <Sidebar />
        <main id="main-content" className="flex-1 overflow-auto pb-8" role="main">
          {children}
        </main>
      </div>

      <HealthBar />
      <SubscriptionPanel open={subscriptionPanelOpen} onOpenChange={setSubscriptionPanelOpen} />
    </div>
  );
}
