import { useState, useEffect } from 'react';
import { useTelegramStatus, useTelegramConfigure } from '@thrum/shared-logic';

export function TelegramSettings() {
  const { data: status, isLoading, error } = useTelegramStatus();
  const configure = useTelegramConfigure();

  const [token, setToken] = useState('');
  const [target, setTarget] = useState('');
  const [userId, setUserId] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [saveMessage, setSaveMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  // Populate form when status loads
  useEffect(() => {
    if (status) {
      // Don't pre-fill token — user must re-enter to change it
      setTarget(status.target ?? '');
      setUserId(status.user_id ?? '');
      setEnabled(status.enabled);
    }
  }, [status]);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaveMessage(null);

    const config: Record<string, unknown> = {
      target,
      user_id: userId,
      enabled,
    };
    // Only include token if user typed something
    if (token.trim()) {
      config.token = token.trim();
    }

    try {
      const result = await configure.mutateAsync(config);
      setSaveMessage({ type: 'success', text: result.message || 'Saved.' });
      setToken(''); // Clear sensitive field after save
    } catch (err) {
      setSaveMessage({
        type: 'error',
        text: err instanceof Error ? err.message : 'Save failed.',
      });
    }
  }

  const statusDot = status?.running
    ? { color: 'var(--status-online)', label: 'Running' }
    : status?.error
      ? { color: 'var(--destructive)', label: 'Error' }
      : { color: 'var(--status-offline)', label: 'Stopped' };

  return (
    <section className="panel p-4">
      <h2 className="text-sm font-semibold text-[var(--accent-color)] uppercase tracking-wider mb-4">
        Telegram Bridge
      </h2>

      {isLoading ? (
        <div className="space-y-2">
          <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/3" />
          <div className="h-4 bg-slate-700/50 rounded animate-pulse w-1/2" />
        </div>
      ) : error ? (
        <p className="text-sm" style={{ color: 'var(--destructive)' }}>
          Failed to load Telegram status:{' '}
          {error instanceof Error ? error.message : 'Unknown error'}
        </p>
      ) : (
        <>
          {/* Status row */}
          <div className="flex items-center gap-2 mb-4 font-mono text-sm">
            <span
              className="inline-block w-2 h-2 rounded-full flex-shrink-0"
              style={{ backgroundColor: statusDot.color }}
              aria-hidden="true"
            />
            <span style={{ color: 'var(--text-secondary)' }}>{statusDot.label}</span>
            {status?.configured && !status.running && !status.error && (
              <span className="text-xs" style={{ color: 'var(--muted-foreground)' }}>
                (configured but not running)
              </span>
            )}
          </div>

          {/* Error display */}
          {status?.error && (
            <div
              className="mb-4 px-3 py-2 rounded text-sm font-mono border"
              style={{
                color: 'var(--destructive)',
                borderColor: 'var(--destructive)',
                background: 'var(--accent-subtle-bg)',
              }}
              role="alert"
            >
              {status.error}
            </div>
          )}

          {/* Live stats */}
          {status?.running && (
            <div
              className="mb-4 px-3 py-2 rounded text-xs font-mono space-y-1 border"
              style={{
                borderColor: 'var(--border)',
                background: 'var(--accent-subtle-bg)',
              }}
            >
              {status.connected_at && (
                <div className="flex justify-between">
                  <span style={{ color: 'var(--muted-foreground)' }}>Connected since</span>
                  <span style={{ color: 'var(--foreground)' }}>
                    {new Date(status.connected_at).toLocaleString()}
                  </span>
                </div>
              )}
              <div className="flex justify-between">
                <span style={{ color: 'var(--muted-foreground)' }}>Messages received</span>
                <span style={{ color: 'var(--foreground)' }}>{status.inbound_count}</span>
              </div>
              {status.chat_id != null && (
                <div className="flex justify-between">
                  <span style={{ color: 'var(--muted-foreground)' }}>Chat ID</span>
                  <span style={{ color: 'var(--foreground)' }}>{status.chat_id}</span>
                </div>
              )}
            </div>
          )}

          {/* Configuration form */}
          <form onSubmit={handleSave} className="space-y-4">
            {/* Token */}
            <div>
              <label
                htmlFor="tg-token"
                className="block text-xs font-mono mb-1"
                style={{ color: 'var(--text-secondary)' }}
              >
                Bot Token
                {status?.token && (
                  <span
                    className="ml-2 text-xs"
                    style={{ color: 'var(--muted-foreground)' }}
                  >
                    (current: {status.token})
                  </span>
                )}
              </label>
              <input
                id="tg-token"
                type="password"
                autoComplete="new-password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={status?.configured ? 'Leave blank to keep existing token' : 'Enter bot token from @BotFather'}
                className="w-full px-3 py-2 text-sm font-mono rounded border bg-transparent transition-colors"
                style={{
                  borderColor: 'var(--border)',
                  color: 'var(--foreground)',
                }}
              />
            </div>

            {/* Target agent */}
            <div>
              <label
                htmlFor="tg-target"
                className="block text-xs font-mono mb-1"
                style={{ color: 'var(--text-secondary)' }}
              >
                Target Agent
              </label>
              <div className="relative">
                <span
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-sm font-mono select-none"
                  style={{ color: 'var(--muted-foreground)' }}
                  aria-hidden="true"
                >
                  @
                </span>
                <input
                  id="tg-target"
                  type="text"
                  value={target.replace(/^@/, '')}
                  onChange={(e) => setTarget(e.target.value.replace(/^@/, ''))}
                  placeholder="agent_name"
                  className="w-full pl-7 pr-3 py-2 text-sm font-mono rounded border bg-transparent transition-colors"
                  style={{
                    borderColor: 'var(--border)',
                    color: 'var(--foreground)',
                  }}
                />
              </div>
            </div>

            {/* User ID */}
            <div>
              <label
                htmlFor="tg-userid"
                className="block text-xs font-mono mb-1"
                style={{ color: 'var(--text-secondary)' }}
              >
                Allowed User ID
              </label>
              <input
                id="tg-userid"
                type="text"
                value={userId}
                onChange={(e) => setUserId(e.target.value)}
                placeholder="Your Telegram user ID"
                className="w-full px-3 py-2 text-sm font-mono rounded border bg-transparent transition-colors"
                style={{
                  borderColor: 'var(--border)',
                  color: 'var(--foreground)',
                }}
              />
            </div>

            {/* Enable toggle */}
            <div className="flex items-center justify-between">
              <label
                htmlFor="tg-enabled"
                className="text-xs font-mono cursor-pointer"
                style={{ color: 'var(--text-secondary)' }}
              >
                Enable bridge
              </label>
              <button
                id="tg-enabled"
                type="button"
                role="switch"
                aria-checked={enabled}
                onClick={() => setEnabled((v) => !v)}
                className="relative inline-flex h-5 w-9 flex-shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2"
                style={{
                  backgroundColor: enabled ? 'var(--accent-color)' : 'var(--border)',
                }}
              >
                <span
                  className="inline-block h-4 w-4 rounded-full bg-white shadow transform transition-transform"
                  style={{ transform: enabled ? 'translateX(1rem)' : 'translateX(0)' }}
                  aria-hidden="true"
                />
              </button>
            </div>

            {/* Save result message */}
            {saveMessage && (
              <p
                className="text-xs font-mono"
                role="status"
                style={{
                  color:
                    saveMessage.type === 'success'
                      ? 'var(--status-online)'
                      : 'var(--destructive)',
                }}
              >
                {saveMessage.text}
              </p>
            )}

            {/* Save button */}
            <button
              type="submit"
              disabled={configure.isPending}
              className="w-full px-3 py-2 text-xs font-mono border rounded transition-colors disabled:opacity-50"
              style={{
                borderColor: 'var(--accent-color)',
                color: 'var(--accent-color)',
                background: 'transparent',
              }}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLButtonElement).style.background =
                  'var(--accent-subtle-bg)';
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
              }}
            >
              {configure.isPending ? 'Saving...' : 'Save'}
            </button>
          </form>
        </>
      )}
    </section>
  );
}
