import { useHealth, useNotificationState, useTheme } from '@thrum/shared-logic';

/** Metadata for a single keyboard shortcut entry */
interface ShortcutEntry {
  keys: string[];
  description: string;
  note?: string;
}

const KEYBOARD_SHORTCUTS: ShortcutEntry[] = [
  { keys: ['1'], description: 'Live Feed' },
  { keys: ['2'], description: 'My Inbox' },
  { keys: ['3'], description: 'First Group', note: 'if available' },
  { keys: ['4'], description: 'First Agent', note: 'if available' },
  { keys: ['5'], description: 'Settings' },
  { keys: ['Cmd', 'K'], description: 'Focus search / main content' },
  { keys: ['Esc'], description: 'Dismiss / focus main content' },
];

export function SettingsView() {
  const { data: health, isLoading, error } = useHealth();
  const { permission, requestPermission } = useNotificationState();
  const { theme, setTheme } = useTheme();

  const notificationLabel =
    permission === 'granted'
      ? 'Enabled'
      : permission === 'denied'
        ? 'Blocked by browser'
        : 'Not requested';

  const notificationColor =
    permission === 'granted'
      ? 'text-green-400'
      : permission === 'denied'
        ? 'text-red-400'
        : 'text-yellow-400';

  return (
    <div className="h-full flex flex-col">
      <div className="p-4 border-b border-cyan-500/20">
        <div className="flex items-center gap-3">
          <span>⚙</span>
          <h1 className="font-semibold">Settings</h1>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {/* Daemon Status */}
        <section className="panel p-4">
          <h2 className="text-sm font-semibold text-cyan-400 uppercase tracking-wider mb-4">
            Daemon Status
          </h2>
          {isLoading ? (
            <div className="space-y-2">
              <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/3" />
              <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/2" />
            </div>
          ) : error ? (
            <p className="text-red-400 text-sm">
              Failed to connect: {error instanceof Error ? error.message : 'Unknown error'}
            </p>
          ) : health ? (
            <div className="space-y-3 font-mono text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Status</span>
                <span className={health.status === 'ok' ? 'text-green-400' : 'text-red-400'}>
                  {health.status}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Version</span>
                <span className="text-cyan-400">{health.version}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Uptime</span>
                <span className="text-cyan-400">
                  {Math.floor((health.uptime_ms || 0) / 60000)}m
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Repo ID</span>
                <span className="text-cyan-400 text-xs break-all">{health.repo_id}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Sync State</span>
                <span className="text-cyan-400">{health.sync_state}</span>
              </div>
            </div>
          ) : null}
        </section>

        {/* Notifications */}
        <section className="panel p-4">
          <h2 className="text-sm font-semibold text-cyan-400 uppercase tracking-wider mb-4">
            Notifications
          </h2>
          <div className="space-y-3 font-mono text-sm">
            <div className="flex justify-between items-center">
              <span className="text-muted-foreground">Browser permission</span>
              <span className={notificationColor}>{notificationLabel}</span>
            </div>
            {permission !== 'granted' && permission !== 'denied' && (
              <button
                onClick={() => requestPermission()}
                className="mt-2 w-full text-left px-3 py-2 text-xs border border-cyan-500/30 text-cyan-400 hover:border-cyan-500/60 hover:bg-cyan-500/10 transition-colors rounded"
                aria-label="Enable browser notifications"
              >
                Enable browser notifications
              </button>
            )}
            {permission === 'denied' && (
              <p className="text-xs text-muted-foreground mt-1">
                Notifications are blocked. Allow them in your browser site settings to enable this
                feature.
              </p>
            )}
          </div>
        </section>

        {/* Theme */}
        <section className="panel p-4">
          <h2 className="text-sm font-semibold text-cyan-400 uppercase tracking-wider mb-4">
            Theme
          </h2>
          <div className="flex gap-2" role="group" aria-label="Theme selection">
            {(['system', 'light', 'dark'] as const).map((option) => (
              <button
                key={option}
                onClick={() => setTheme(option)}
                aria-pressed={theme === option}
                className={[
                  'flex-1 px-3 py-2 text-xs font-mono border transition-colors rounded capitalize',
                  theme === option
                    ? 'border-cyan-500 text-cyan-400 bg-cyan-500/10'
                    : 'border-cyan-500/30 text-muted-foreground hover:border-cyan-500/60 hover:text-cyan-400 hover:bg-cyan-500/5',
                ].join(' ')}
              >
                {option}
              </button>
            ))}
          </div>
        </section>

        {/* Keyboard Shortcuts */}
        <section className="panel p-4">
          <h2 className="text-sm font-semibold text-cyan-400 uppercase tracking-wider mb-4">
            Keyboard Shortcuts
          </h2>
          <table className="w-full text-sm" aria-label="Keyboard shortcuts reference">
            <thead>
              <tr className="text-left border-b border-cyan-500/10">
                <th className="pb-2 font-normal text-muted-foreground text-xs uppercase tracking-wider w-1/3">
                  Keys
                </th>
                <th className="pb-2 font-normal text-muted-foreground text-xs uppercase tracking-wider">
                  Action
                </th>
              </tr>
            </thead>
            <tbody className="font-mono divide-y divide-cyan-500/5">
              {KEYBOARD_SHORTCUTS.map((shortcut) => (
                <tr key={shortcut.description} className="group">
                  <td className="py-2 pr-4">
                    <span className="inline-flex gap-1 flex-wrap">
                      {shortcut.keys.map((key, i) => (
                        <span key={i}>
                          <kbd className="inline-block px-1.5 py-0.5 text-xs border border-cyan-500/30 text-cyan-400 bg-cyan-500/5 rounded">
                            {key}
                          </kbd>
                          {i < shortcut.keys.length - 1 && (
                            <span className="text-muted-foreground mx-0.5">+</span>
                          )}
                        </span>
                      ))}
                    </span>
                  </td>
                  <td className="py-2 text-sm text-foreground">
                    {shortcut.description}
                    {shortcut.note && (
                      <span className="ml-1 text-xs text-muted-foreground">({shortcut.note})</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      </div>
    </div>
  );
}
