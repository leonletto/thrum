import { useHealth } from '@thrum/shared-logic';

/**
 * Format uptime from milliseconds to human-readable string
 */
function formatUptime(uptimeMs: number): string {
  const seconds = Math.floor(uptimeMs / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (days > 0) return `${days}d ${hours % 24}h`;
  if (hours > 0) return `${hours}h ${minutes % 60}m`;
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
  return `${seconds}s`;
}

/**
 * Truncate repo ID to first 8 characters
 */
function truncateRepoId(repoId: string): string {
  return repoId.slice(0, 8);
}

export function HealthBar() {
  const { data: health, error } = useHealth();

  const isHealthy = !error && health?.status === 'ok';
  const statusColor = isHealthy ? 'bg-green-500' : 'bg-red-500';
  const statusLabel = isHealthy ? 'CONNECTED' : 'DISCONNECTED';

  return (
    <footer
      className="fixed bottom-0 left-0 right-0 h-8 bg-[var(--panel-bg-start)] border-t border-[var(--accent-border)] flex items-center px-4 gap-4 text-[11px] font-mono z-20"
      aria-label="System health status"
    >
      <div className="flex items-center gap-2">
        <div
          className={`w-2 h-2 rounded-full ${statusColor}`}
          aria-hidden="true"
        />
        <span className="text-[var(--accent-color)]">{statusLabel}</span>
      </div>

      {health && isHealthy && (
        <>
          <div className="text-gray-400">
            <span className="text-gray-500">v</span>
            {health.version}
          </div>

          <div className="text-gray-400">
            <span className="text-gray-500">up:</span>
            {formatUptime(health.uptime_ms)}
          </div>

          <div className="text-gray-400">
            <span className="text-gray-500">repo:</span>
            {truncateRepoId(health.repo_id)}
          </div>
        </>
      )}
    </footer>
  );
}
