import { useState } from 'react';
import { Inbox, AlertTriangle, Filter } from 'lucide-react';
import type { MessageScope } from '@thrum/shared-logic';
import { ComposeModal } from './ComposeModal';
import { ScopeBadge } from '@/components/ui/ScopeBadge';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';

export type InboxFilter = 'all' | 'unread' | 'mentions';

interface InboxHeaderProps {
  identity: string;
  sendingAs: string;
  isImpersonating: boolean;
  onScopeFilterChange?: (scope: MessageScope | null) => void;
  activeScopeFilter?: MessageScope | null;
  unreadCount: number;
  filter: InboxFilter;
  onFilterChange: (filter: InboxFilter) => void;
}

export function InboxHeader({
  identity,
  sendingAs,
  isImpersonating,
  onScopeFilterChange,
  activeScopeFilter,
  unreadCount,
  filter,
  onFilterChange,
}: InboxHeaderProps) {
  const [composeOpen, setComposeOpen] = useState(false);
  const [filterOpen, setFilterOpen] = useState(false);
  const [scopeType, setScopeType] = useState('');
  const [scopeValue, setScopeValue] = useState('');

  const handleApplyFilter = () => {
    if (scopeType.trim() && scopeValue.trim()) {
      onScopeFilterChange?.({
        type: scopeType.trim(),
        value: scopeValue.trim(),
      });
      setFilterOpen(false);
      setScopeType('');
      setScopeValue('');
    }
  };

  const handleClearFilter = () => {
    onScopeFilterChange?.(null);
  };

  return (
    <>
      <div className="p-4 border-b space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Inbox className="w-5 h-5 text-muted-foreground" />
            <div>
              <h1 className="font-semibold">{identity}</h1>
              {isImpersonating && (
                <p className="text-xs text-yellow-600 dark:text-yellow-500 flex items-center gap-1">
                  <AlertTriangle className="w-3 h-3" />
                  Sending as {sendingAs}
                </p>
              )}
            </div>
          </div>

          <div className="flex items-center gap-2">
            <DropdownMenu open={filterOpen} onOpenChange={setFilterOpen}>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm">
                  <Filter className="h-4 w-4 mr-2" />
                  Scope Filter
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-64">
                <div className="p-3 space-y-3">
                  <div className="space-y-2">
                    <label className="text-xs text-muted-foreground">Scope Type</label>
                    <Input
                      placeholder="e.g., project, tag"
                      value={scopeType}
                      onChange={(e) => setScopeType(e.target.value)}
                      onKeyDown={(e) => e.key === 'Enter' && handleApplyFilter()}
                    />
                  </div>
                  <div className="space-y-2">
                    <label className="text-xs text-muted-foreground">Scope Value</label>
                    <Input
                      placeholder="e.g., thrum, urgent"
                      value={scopeValue}
                      onChange={(e) => setScopeValue(e.target.value)}
                      onKeyDown={(e) => e.key === 'Enter' && handleApplyFilter()}
                    />
                  </div>
                  <Button
                    size="sm"
                    className="w-full"
                    onClick={handleApplyFilter}
                    disabled={!scopeType.trim() || !scopeValue.trim()}
                  >
                    Apply Filter
                  </Button>
                </div>
              </DropdownMenuContent>
            </DropdownMenu>

            <button className="compose-btn" onClick={() => setComposeOpen(true)}>
              + COMPOSE
            </button>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant={filter === 'all' ? 'default' : 'outline'}
            size="sm"
            onClick={() => onFilterChange('all')}
          >
            All
          </Button>
          <Button
            variant={filter === 'unread' ? 'default' : 'outline'}
            size="sm"
            onClick={() => onFilterChange('unread')}
          >
            Unread
            {unreadCount > 0 && (
              <Badge variant="destructive" className="ml-2">
                {unreadCount}
              </Badge>
            )}
          </Button>
          <Button
            variant={filter === 'mentions' ? 'default' : 'outline'}
            size="sm"
            onClick={() => onFilterChange('mentions')}
          >
            Mentions
          </Button>
        </div>

        {activeScopeFilter && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">Filtered by:</span>
            <ScopeBadge scope={activeScopeFilter} onRemove={handleClearFilter} />
          </div>
        )}
      </div>

      <ComposeModal
        open={composeOpen}
        onOpenChange={setComposeOpen}
        sendingAs={sendingAs}
        isImpersonating={isImpersonating}
      />
    </>
  );
}
