import { useState, type FormEvent } from 'react';
import { Loader2, Plus, Trash2 } from 'lucide-react';
import {
  useSubscriptionList,
  useSubscribe,
  useUnsubscribe,
  useAgentList,
  type MessageScope,
} from '@thrum/shared-logic';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { formatRelativeTime } from '@/lib/time';

interface SubscriptionPanelProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type FilterType = 'scope' | 'mention' | 'all';

export function SubscriptionPanel({ open, onOpenChange }: SubscriptionPanelProps) {
  const { data, isLoading } = useSubscriptionList();
  const { mutate: subscribe, isPending: isSubscribing } = useSubscribe();
  const { mutate: unsubscribe } = useUnsubscribe();
  const { data: agentListData } = useAgentList();

  const [filterType, setFilterType] = useState<FilterType>('scope');
  const [scopeType, setScopeType] = useState('');
  const [scopeValue, setScopeValue] = useState('');
  const [mentionRole, setMentionRole] = useState('');
  const [showAddForm, setShowAddForm] = useState(false);

  const subscriptions = data?.subscriptions || [];
  const agents = agentListData?.agents || [];

  const handleAddSubscription = (e: FormEvent) => {
    e.preventDefault();

    const request: { filter_type: FilterType; scope?: MessageScope; mention?: string } = {
      filter_type: filterType,
    };

    if (filterType === 'scope' && scopeType.trim() && scopeValue.trim()) {
      request.scope = { type: scopeType.trim(), value: scopeValue.trim() };
    } else if (filterType === 'mention' && mentionRole.trim()) {
      request.mention = mentionRole.trim();
    }

    subscribe(request, {
      onSuccess: () => {
        setScopeType('');
        setScopeValue('');
        setMentionRole('');
        setShowAddForm(false);
      },
    });
  };

  const handleDelete = (subscriptionId: string) => {
    unsubscribe({ subscription_id: subscriptionId });
  };

  const getFilterLabel = (sub: { filter_type: string; scope?: MessageScope; mention?: string }) => {
    if (sub.filter_type === 'scope' && sub.scope) {
      return `${sub.scope.type}:${sub.scope.value}`;
    }
    if (sub.filter_type === 'mention' && sub.mention) {
      return `@${sub.mention}`;
    }
    return 'All messages';
  };

  const canSubmit =
    filterType === 'all' ||
    (filterType === 'scope' && scopeType.trim() && scopeValue.trim()) ||
    (filterType === 'mention' && mentionRole.trim());

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>Subscriptions</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {isLoading && (
            <div className="space-y-3">
              {[1, 2].map((i) => (
                <div key={i} className="h-12 bg-slate-700/50 rounded animate-pulse" />
              ))}
            </div>
          )}

          {!isLoading && subscriptions.length === 0 && !showAddForm && (
            <div className="text-center py-8">
              <p className="text-muted-foreground text-sm">
                No active subscriptions. Add one to receive real-time notifications.
              </p>
            </div>
          )}

          {!isLoading && subscriptions.length > 0 && (
            <div className="space-y-2">
              {subscriptions.map((sub) => (
                <div
                  key={sub.subscription_id}
                  className="flex items-center justify-between px-3 py-2 rounded-md border border-[var(--accent-border)] hover:bg-[var(--accent-subtle-bg)] transition-colors"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-xs font-mono uppercase tracking-wider text-[var(--accent-color)]">
                        {sub.filter_type}
                      </span>
                      <span className="text-sm font-mono truncate">
                        {getFilterLabel(sub)}
                      </span>
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {formatRelativeTime(sub.created_at)}
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleDelete(sub.subscription_id)}
                    aria-label={`Delete subscription ${getFilterLabel(sub)}`}
                  >
                    <Trash2 className="h-4 w-4 text-red-400" />
                  </Button>
                </div>
              ))}
            </div>
          )}

          {!showAddForm && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowAddForm(true)}
              className="w-full"
            >
              <Plus className="h-4 w-4 mr-1" />
              Add Subscription
            </Button>
          )}

          {showAddForm && (
            <form onSubmit={handleAddSubscription} className="space-y-3 border-t pt-3">
              <div className="space-y-2">
                <Label>Type</Label>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="outline"
                      className="w-full justify-between capitalize"
                      type="button"
                    >
                      {filterType}
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent className="w-full">
                    <DropdownMenuRadioGroup
                      value={filterType}
                      onValueChange={(v) => setFilterType(v as FilterType)}
                    >
                      <DropdownMenuRadioItem value="scope">Scope</DropdownMenuRadioItem>
                      <DropdownMenuRadioItem value="mention">Mention</DropdownMenuRadioItem>
                      <DropdownMenuRadioItem value="all">All</DropdownMenuRadioItem>
                    </DropdownMenuRadioGroup>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>

              {filterType === 'scope' && (
                <div className="flex gap-2">
                  <div className="flex-1 space-y-1">
                    <Label htmlFor="scope-type" className="text-xs">Scope Type</Label>
                    <Input
                      id="scope-type"
                      placeholder="e.g., project"
                      value={scopeType}
                      onChange={(e) => setScopeType(e.target.value)}
                    />
                  </div>
                  <div className="flex-1 space-y-1">
                    <Label htmlFor="scope-value" className="text-xs">Scope Value</Label>
                    <Input
                      id="scope-value"
                      placeholder="e.g., thrum"
                      value={scopeValue}
                      onChange={(e) => setScopeValue(e.target.value)}
                    />
                  </div>
                </div>
              )}

              {filterType === 'mention' && (
                <div className="space-y-1">
                  <Label htmlFor="mention-role">Agent Role</Label>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        id="mention-role"
                        variant="outline"
                        className="w-full justify-between"
                        type="button"
                      >
                        {mentionRole || 'Select agent...'}
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent className="w-full">
                      <DropdownMenuRadioGroup
                        value={mentionRole}
                        onValueChange={setMentionRole}
                      >
                        {agents.map((agent) => (
                          <DropdownMenuRadioItem key={agent.agent_id} value={agent.role}>
                            @{agent.role} {agent.display ? `(${agent.display})` : ''}
                          </DropdownMenuRadioItem>
                        ))}
                      </DropdownMenuRadioGroup>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              )}

              <div className="flex gap-2">
                <Button type="submit" size="sm" disabled={isSubscribing || !canSubmit}>
                  {isSubscribing ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Subscribe'}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setShowAddForm(false);
                    setScopeType('');
                    setScopeValue('');
                    setMentionRole('');
                  }}
                >
                  Cancel
                </Button>
              </div>
            </form>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
