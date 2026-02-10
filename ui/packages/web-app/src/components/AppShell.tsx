import { useState } from 'react';
import { Settings } from 'lucide-react';
import { Sidebar } from './Sidebar';
import { ThemeToggle } from './ThemeToggle';
import { SkipLink } from './SkipLink';
import { HealthBar } from './status/HealthBar';
import { SubscriptionPanel } from './subscriptions/SubscriptionPanel';
import { useAuth } from './AuthProvider';

interface AppShellProps {
  children: React.ReactNode;
}

export function AppShell({ children }: AppShellProps) {
  const [subscriptionPanelOpen, setSubscriptionPanelOpen] = useState(false);
  const { user, isLoading } = useAuth();

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
