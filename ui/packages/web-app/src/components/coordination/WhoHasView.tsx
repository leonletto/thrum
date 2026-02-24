import { useState } from 'react';
import { useAgentContext, useDebounce } from '@thrum/shared-logic';
import { Input } from '@/components/ui/input';
import { formatRelativeTime } from '@/lib/time';

export function WhoHasView() {
  const [search, setSearch] = useState('');
  const debouncedSearch = useDebounce(search, 500);

  const { data, isLoading } = useAgentContext();

  // Filter contexts by file path match
  const filteredContexts = debouncedSearch.trim()
    ? (data || []).filter((ctx) => {
        const searchLower = debouncedSearch.toLowerCase();
        return (
          ctx.changed_files.some((f) => f.toLowerCase().includes(searchLower)) ||
          ctx.uncommitted_files.some((f) => f.toLowerCase().includes(searchLower))
        );
      })
    : [];

  // Build result rows with matched files
  const results = filteredContexts.map((ctx) => {
    const searchLower = debouncedSearch.toLowerCase();
    const matchedFiles = [
      ...ctx.changed_files.filter((f) => f.toLowerCase().includes(searchLower)),
      ...ctx.uncommitted_files.filter((f) => f.toLowerCase().includes(searchLower)),
    ];
    // Deduplicate
    const uniqueFiles = [...new Set(matchedFiles)];
    const displayName = ctx.agent_id.split(':').slice(-1)[0] || ctx.agent_id;

    return {
      agentId: ctx.agent_id,
      displayName,
      branch: ctx.branch,
      intent: ctx.intent,
      lastSeen: ctx.git_updated_at,
      matchedFiles: uniqueFiles,
    };
  });

  return (
    <div className="who-has-view h-full flex flex-col p-6">
      <h1 className="text-xl font-bold font-mono uppercase tracking-wider mb-6">
        Who Has?
      </h1>

      <div className="who-has-search mb-6">
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by file path..."
          className="font-mono"
        />
      </div>

      <div className="who-has-results flex-1 overflow-auto">
        {!debouncedSearch.trim() && (
          <div className="text-center py-12">
            <p className="text-muted-foreground text-sm">
              Search for a file to see who is editing it
            </p>
          </div>
        )}

        {debouncedSearch.trim() && isLoading && (
          <div className="space-y-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="h-16 bg-slate-700/50 rounded animate-pulse" />
            ))}
          </div>
        )}

        {debouncedSearch.trim() && !isLoading && results.length === 0 && (
          <div className="text-center py-12">
            <p className="text-muted-foreground text-sm">
              No agents are currently editing this file
            </p>
          </div>
        )}

        {results.length > 0 && (
          <div className="space-y-1">
            <div className="grid grid-cols-4 gap-4 px-4 py-2 text-xs font-mono uppercase tracking-wider text-muted-foreground border-b border-[var(--accent-border)]">
              <span>Agent</span>
              <span>Branch</span>
              <span>Intent</span>
              <span>Last Seen</span>
            </div>
            {results.map((result) => (
              <div
                key={result.agentId}
                className="who-has-row grid grid-cols-4 gap-4 px-4 py-3 rounded-md hover:bg-[var(--accent-subtle-bg)] transition-colors"
              >
                <div>
                  <div className="font-mono text-sm text-[var(--accent-color)]">{result.displayName}</div>
                  <div className="text-xs text-muted-foreground truncate">{result.agentId}</div>
                </div>
                <div className="font-mono text-sm self-center">{result.branch}</div>
                <div className="text-sm self-center truncate">{result.intent || '—'}</div>
                <div className="text-sm text-muted-foreground self-center">
                  {formatRelativeTime(result.lastSeen)}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
