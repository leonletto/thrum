import { useState, type FormEvent } from 'react';
import { Loader2, X, Users } from 'lucide-react';
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

  const allMentions = Array.from(new Set([...mentions, ...selectedRecipients]));

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
        ...(allMentions.length > 0 && { mentions: allMentions }),
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
      className="border-t border-cyan-500/20 bg-[#0a0e1a] font-mono"
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
        <div className="flex items-center gap-2 px-3 py-1 bg-cyan-900/20 border-b border-cyan-500/20">
          <span className="text-xs text-cyan-400">
            Replying to: @{replyTo.senderName}
          </span>
          <button
            type="button"
            aria-label="Clear reply"
            onClick={onClearReply}
            className="text-cyan-600 hover:text-cyan-300 transition-colors"
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      )}

      <form onSubmit={handleSubmit} className="flex flex-col gap-2 p-3">
        <div className="flex items-center gap-2">
          {!groupScope && (
            <div className="relative flex items-center gap-1 min-w-0 flex-1">
              <span className="text-xs text-cyan-600 shrink-0">To:</span>
              <div className="flex flex-wrap gap-1 flex-1 min-w-0 text-xs text-cyan-300">
                {selectedRecipients.length > 0 ? (
                  selectedRecipients.map((r) => (
                    <span
                      key={r}
                      className="bg-cyan-900/40 border border-cyan-500/30 rounded px-1"
                    >
                      @{r}
                    </span>
                  ))
                ) : (
                  <span className="text-cyan-700 italic">none selected</span>
                )}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label="Select recipients"
                onClick={() => setShowRecipientDropdown((v) => !v)}
                className="shrink-0 h-6 px-2 text-xs text-cyan-500 hover:text-cyan-300 hover:bg-cyan-900/30"
              >
                <Users className="h-3 w-3 mr-1" />
                Select
              </Button>

              {showRecipientDropdown && (
                <div
                  className="absolute bottom-full left-0 mb-1 w-64 bg-[#0d1120] border border-cyan-500/30 rounded shadow-lg z-10"
                  data-testid="recipient-dropdown"
                >
                  {agents.length > 0 && (
                    <div>
                      <div className="px-3 py-1 text-xs text-cyan-600 uppercase tracking-wider border-b border-cyan-500/10">
                        Agents
                      </div>
                      {agents.map((agent) => {
                        const id = agent.display || agent.agent_id;
                        const status = getAgentStatus(agent.last_seen_at);
                        return (
                          <label
                            key={agent.agent_id}
                            className="flex items-center gap-2 px-3 py-1.5 hover:bg-cyan-900/20 cursor-pointer"
                          >
                            <Checkbox
                              checked={selectedRecipients.includes(id)}
                              onCheckedChange={() => toggleRecipient(id)}
                              aria-label={`Select ${id}`}
                            />
                            <StatusIndicator status={status} />
                            <span className="text-xs text-cyan-300 truncate">
                              {id}
                            </span>
                          </label>
                        );
                      })}
                    </div>
                  )}

                  {groups.length > 0 && (
                    <div>
                      <div className="px-3 py-1 text-xs text-cyan-600 uppercase tracking-wider border-b border-cyan-500/10">
                        Groups
                      </div>
                      {groups.map((group) => (
                        <label
                          key={group.group_id}
                          className="flex items-center gap-2 px-3 py-1.5 hover:bg-cyan-900/20 cursor-pointer"
                        >
                          <Checkbox
                            checked={selectedRecipients.includes(group.name)}
                            onCheckedChange={() =>
                              toggleRecipient(group.name)
                            }
                            aria-label={`Select ${group.name}`}
                          />
                          <span className="text-xs text-cyan-300 truncate">
                            @{group.name}
                          </span>
                        </label>
                      ))}
                    </div>
                  )}

                  <div className="border-t border-cyan-500/10 px-3 py-2 flex justify-end">
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

          <div className={groupScope ? 'flex-1' : 'flex-[2]'}>
            <MentionAutocomplete
              value={content}
              onChange={handleContentChange}
              placeholder="Write a message... (Use @ to mention agents)"
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
          <div className="text-xs text-amber-400/70 px-1">
            Sending as: {sendingAs}
          </div>
        )}
      </form>
    </div>
  );
}
