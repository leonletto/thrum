import { useHealth } from '@thrum/shared-logic';

export function SettingsView() {
  const { data: health, isLoading, error } = useHealth();

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
      </div>
    </div>
  );
}
