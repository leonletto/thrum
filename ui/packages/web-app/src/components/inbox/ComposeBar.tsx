import { useState, type FormEvent } from 'react';
import { Loader2, X } from 'lucide-react';
import {
  useSendMessage,
  useCurrentUser,
  useAgentList,
  useGroupList,
} from '@thrum/shared-logic';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { MentionAutocomplete } from './MentionAutocomplete';
import { StatusIndicator } from '../agents/StatusIndicator';

interface ComposeBarProps {
  sendingAs: string;
  isImpersonating: boolean;
  groupScope?: string;
  replyTo?: {
    messageId: string;
    senderName: string;
  };
  onClearReply?: () => void;
}

export function ComposeBar({
  sendingAs,
  isImpersonating,
  groupScope,
  replyTo,
  onClearReply,
}: ComposeBarProps) {
  const [content, setContent] = useState('');
  const [mentions, setMentions] = useState<string[]>([]);
  const [selectedRecipients, setSelectedRecipients] = useState<string[]>([]);
  const [showRecipientDropdown, setShowRecipientDropdown] = useState(false);
  const [warnings, setWarnings] = useState<string[]>([]);

  const currentUser = useCurrentUser();
  const { mutate: sendMessage, isPending } = useSendMessage();
  const { data: agentListData } = useAgentList();
  const { data: groupListData } = useGroupList();

  const allRecipients = Array.from(new Set([...selectedRecipients, ...mentions]));

  const handleContentChange = (newContent: string, newMentions: string[]) => {
    setContent(newContent);
    setMentions(newMentions);
  };

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!content.trim()) return;

    setWarnings([]);

    sendMessage(
      {
        content,
        ...(allRecipients.length > 0 && { mentions: allRecipients }),
        ...(groupScope && { scopes: [{ type: 'group', value: groupScope }] }),
        ...(replyTo?.messageId && { reply_to: replyTo.messageId }),
        ...(isImpersonating && { acting_as: sendingAs }),
        ...(currentUser?.user_id && { caller_agent_id: currentUser.user_id }),
      },
      {
        onSuccess: (data) => {
          setContent('');
          setMentions([]);
          setSelectedRecipients([]);
          if (data?.warnings && data.warnings.length > 0) {
            setWarnings(data.warnings);
          }
          onClearReply?.();
        },
        onError: (err) => {
          setWarnings([err instanceof Error ? err.message : 'Failed to send message']);
        },
      }
    );
  };

  const toggleRecipient = (id: string) => {
    setSelectedRecipients((prev) =>
      prev.includes(id) ? prev.filter((r) => r !== id) : [...prev, id]
    );
  };

  const removeChip = (chip: string) => {
    setSelectedRecipients((prev) => prev.filter((r) => r !== chip));
  };

  const getAgentStatus = (lastSeenAt?: string): 'online' | 'offline' => {
    if (!lastSeenAt) return 'offline';
    const now = new Date().getTime();
    const lastSeen = new Date(lastSeenAt).getTime();
    const minutesSinceLastSeen = (now - lastSeen) / 60000;
    return minutesSinceLastSeen < 5 ? 'online' : 'offline';
  };

  const agents = agentListData?.agents || [];
  const groups = groupListData?.groups || [];

  return (
    <div
      className="border-t border-[var(--accent-border)] bg-[var(--panel-bg-start)] font-mono"
      data-testid="compose-bar"
    >
      {warnings.length > 0 && (
        <div
          className="px-3 py-1 bg-yellow-900/30 border-b border-yellow-500/30 text-yellow-400 text-xs"
          role="alert"
        >
          {warnings.map((w, i) => (
            <span key={i} className="block">
              {w}
            </span>
          ))}
        </div>
      )}

      {replyTo && (
        <div className="flex items-center gap-2 px-3 py-1 bg-[var(--accent-subtle-bg)] border-b border-[var(--accent-border)]">
          <span className="text-xs text-[var(--accent-color)]">
            Replying to: @{replyTo.senderName}
          </span>
          <button
            type="button"
            aria-label="Clear reply"
            onClick={onClearReply}
            className="text-[var(--text-muted)] hover:text-[var(--accent-color)] transition-colors"
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      )}

      <form onSubmit={handleSubmit} className="flex flex-col p-3 gap-0">
        {/* Row 1: Addressing row — hidden when groupScope is set */}
        {!groupScope && (
          <div className="relative flex items-center gap-1 min-w-0 pb-2 border-b border-[var(--accent-border)]">
            <span className="text-xs text-[var(--text-muted)] shrink-0">To:</span>

            {/* Chips: union of selectedRecipients and @mentions */}
            <div className="flex flex-wrap gap-1 flex-1 min-w-0 text-xs text-[var(--text-secondary)]">
              {allRecipients.length > 0 ? (
                allRecipients.map((chip) => (
                  <span
                    key={chip}
                    className="flex items-center gap-0.5 bg-[var(--accent-subtle-bg-hover)] border border-[var(--accent-border)] rounded px-1"
                  >
                    @{chip}
                    {/* Only show X for explicitly selected recipients, not pure @mentions */}
                    {selectedRecipients.includes(chip) && (
                      <button
                        type="button"
                        aria-label={`Remove ${chip}`}
                        onClick={() => removeChip(chip)}
                        className="text-[var(--text-muted)] hover:text-[var(--accent-color)] transition-colors ml-0.5"
                      >
                        <X className="h-2.5 w-2.5" />
                      </button>
                    )}
                  </span>
                ))
              ) : (
                <span className="text-[var(--text-faint)] italic">none selected</span>
              )}
            </div>

            {/* + Add button to open recipient dropdown */}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label="Add recipients"
              onClick={() => setShowRecipientDropdown((v) => !v)}
              className="shrink-0 h-6 px-2 text-xs text-[var(--accent-color)] hover:text-[var(--accent-color)] hover:bg-[var(--accent-subtle-bg)]"
            >
              + Add
            </Button>

            {/* Recipient dropdown */}
            {showRecipientDropdown && (
              <div
                className="absolute bottom-full left-0 mb-1 w-64 bg-[var(--panel-bg-start)] border border-[var(--accent-border)] rounded shadow-lg z-10"
                data-testid="recipient-dropdown"
              >
                {agents.length > 0 && (
                  <div>
                    <div className="px-3 py-1 text-xs text-[var(--text-muted)] uppercase tracking-wider border-b border-[var(--accent-border)]">
                      Agents
                    </div>
                    {agents.map((agent) => {
                      const id = agent.display || agent.agent_id;
                      const status = getAgentStatus(agent.last_seen_at);
                      return (
                        <label
                          key={agent.agent_id}
                          className="flex items-center gap-2 px-3 py-1.5 hover:bg-[var(--accent-subtle-bg)] cursor-pointer"
                        >
                          <Checkbox
                            checked={selectedRecipients.includes(id)}
                            onCheckedChange={() => toggleRecipient(id)}
                            aria-label={`Select ${id}`}
                          />
                          <StatusIndicator status={status} />
                          <span className="text-xs text-[var(--text-secondary)] truncate">
                            {id}
                          </span>
                        </label>
                      );
                    })}
                  </div>
                )}

                {groups.length > 0 && (
                  <div>
                    <div className="px-3 py-1 text-xs text-[var(--text-muted)] uppercase tracking-wider border-b border-[var(--accent-border)]">
                      Groups
                    </div>
                    {groups.map((group) => (
                      <label
                        key={group.group_id}
                        className="flex items-center gap-2 px-3 py-1.5 hover:bg-[var(--accent-subtle-bg)] cursor-pointer"
                      >
                        <Checkbox
                          checked={selectedRecipients.includes(group.name)}
                          onCheckedChange={() => toggleRecipient(group.name)}
                          aria-label={`Select ${group.name}`}
                        />
                        <span className="text-xs text-[var(--text-secondary)] truncate">
                          @{group.name}
                        </span>
                      </label>
                    ))}
                  </div>
                )}

                <div className="border-t border-[var(--accent-border)] px-3 py-2 flex justify-end">
                  <Button
                    type="button"
                    size="sm"
                    onClick={() => setShowRecipientDropdown(false)}
                    className="h-6 px-3 text-xs"
                  >
                    Done
                  </Button>
                </div>
              </div>
            )}
          </div>
        )}

        {/* Row 2: Message row — textarea + Send button inline */}
        <div className="flex items-end gap-2 pt-2">
          <div className="flex-1 min-w-0">
            <MentionAutocomplete
              value={content}
              onChange={handleContentChange}
              placeholder="Write a message..."
              className="min-h-[36px] resize-none text-sm"
              disabled={isPending}
            />
          </div>

          <Button
            type="submit"
            size="sm"
            disabled={isPending || !content.trim()}
            className="shrink-0"
          >
            {isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Send'}
          </Button>
        </div>

        {isImpersonating && (
          <div className="pt-1 text-xs text-amber-400/70 px-1">
            Sending as: {sendingAs}
          </div>
        )}
      </form>
    </div>
  );
}
