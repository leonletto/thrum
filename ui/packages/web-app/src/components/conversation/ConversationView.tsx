import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Loader2, Send } from 'lucide-react';
import {
  useConversation,
  useSendMessage,
  type ConversationMessage,
} from '@thrum/shared-logic';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { ChatBubble } from './ChatBubble';
import { cn } from '@/lib/utils';

interface ConversationViewProps {
  /** Current user / agent viewing the conversation. */
  agentId: string;
  /** The conversation partner. */
  withAgentId: string;
}

/** Returns true when two ISO timestamps are more than 5 minutes apart. */
function isMoreThanFiveMinutes(a: string, b: string): boolean {
  return Math.abs(new Date(b).getTime() - new Date(a).getTime()) > 5 * 60 * 1000;
}

function formatDividerTime(timestamp: string): string {
  const date = new Date(timestamp);
  const now = new Date();
  const diffDays = Math.floor(
    (now.getTime() - date.getTime()) / (1000 * 60 * 60 * 24)
  );
  if (diffDays === 0) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  if (diffDays === 1) {
    return `Yesterday ${date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
  }
  return date.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

/** Build a lookup map from message id → ConversationMessage for reply-to references. */
function buildMessageMap(
  messages: ConversationMessage[]
): Map<string, ConversationMessage> {
  const map = new Map<string, ConversationMessage>();
  for (const msg of messages) {
    map.set(msg.id, msg);
  }
  return map;
}

export function ConversationView({ agentId, withAgentId }: ConversationViewProps) {
  const { data: messages = [], isLoading, error } = useConversation({
    agentId,
    withAgentId,
  });

  const sendMessage = useSendMessage();
  const [text, setText] = useState('');
  const bottomRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Auto-scroll to bottom whenever the message list changes.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages.length]);

  const messageMap = buildMessageMap(messages);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = text.trim();
    if (!trimmed || sendMessage.isPending) return;

    try {
      await sendMessage.mutateAsync({
        content: trimmed,
        mentions: [`@${withAgentId}`],
        caller_agent_id: agentId,
      });
      setText('');
      textareaRef.current?.focus();
    } catch (err) {
      console.error('Failed to send message:', err);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Submit on Enter (without Shift)
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e as unknown as FormEvent);
    }
  };

  return (
    <div className="h-full flex flex-col" style={{ fontFamily: 'inherit' }}>
      {/* ── Message timeline ── */}
      <div className="flex-1 overflow-y-auto px-4 py-4 space-y-1" role="log" aria-live="polite">
        {isLoading && (
          <div className="flex items-center justify-center h-full">
            <Loader2
              className="h-6 w-6 animate-spin"
              style={{ color: 'var(--muted-foreground)' }}
            />
          </div>
        )}

        {error && (
          <div
            className="text-sm text-center py-8"
            style={{ color: 'var(--muted-foreground)' }}
          >
            Failed to load conversation.
          </div>
        )}

        {!isLoading && !error && messages.length === 0 && (
          <div
            className="text-sm text-center py-8"
            style={{ color: 'var(--muted-foreground)' }}
          >
            No messages yet. Say hello!
          </div>
        )}

        {messages.map((msg, index) => {
          const prev = index > 0 ? messages[index - 1] : null;
          const isSent = msg.direction === 'sent';

          // Group consecutive messages from the same sender.
          const sameAuthorAsPrev =
            prev != null && prev.author.agentId === msg.author.agentId;

          // Insert a time divider when there's a >5-min gap.
          const showTimeDivider =
            prev == null || isMoreThanFiveMinutes(prev.timestamp, msg.timestamp);

          // Show sender name on the first bubble of a new group.
          const showSender = !sameAuthorAsPrev || showTimeDivider;

          // Add extra top margin for new groups.
          const extraMarginTop = showSender && index > 0 && !showTimeDivider;

          const replyToMessage = msg.replyTo ? messageMap.get(msg.replyTo) : undefined;

          return (
            <div key={msg.id}>
              {/* Time divider */}
              {showTimeDivider && (
                <div className="flex items-center gap-3 my-4" aria-hidden="true">
                  <div
                    className="flex-1 h-px"
                    style={{ backgroundColor: 'var(--border)' }}
                  />
                  <span
                    className="text-[10px] shrink-0 px-1"
                    style={{ color: 'var(--muted-foreground)' }}
                  >
                    {formatDividerTime(msg.timestamp)}
                  </span>
                  <div
                    className="flex-1 h-px"
                    style={{ backgroundColor: 'var(--border)' }}
                  />
                </div>
              )}

              {/* Chat bubble with optional group spacing */}
              <div className={cn(extraMarginTop && 'mt-3')}>
                <ChatBubble
                  message={msg}
                  showSender={showSender}
                  isSent={isSent}
                  replyToMessage={replyToMessage}
                />
              </div>
            </div>
          );
        })}

        {/* Scroll anchor */}
        <div ref={bottomRef} />
      </div>

      {/* ── Compose bar ── */}
      <div
        className="border-t px-4 py-3"
        style={{
          borderColor: 'var(--border)',
          backgroundColor: 'var(--panel-bg-start)',
        }}
      >
        <form onSubmit={handleSubmit} className="flex items-end gap-2">
          <Textarea
            ref={textareaRef}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={`Message @${withAgentId}…`}
            disabled={sendMessage.isPending}
            rows={1}
            className={cn(
              'flex-1 resize-none min-h-[36px] max-h-[120px] text-sm',
              'border focus-visible:ring-1'
            )}
            style={{
              borderColor: 'var(--border)',
              color: 'rgb(var(--foreground))',
              backgroundColor: 'var(--panel-bg-end)',
            }}
          />
          <Button
            type="submit"
            size="sm"
            disabled={sendMessage.isPending || !text.trim()}
            className="shrink-0 h-9 w-9 p-0"
            aria-label="Send message"
          >
            {sendMessage.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Send className="h-4 w-4" />
            )}
          </Button>
        </form>
        <p
          className="text-[10px] mt-1.5 pl-0.5"
          style={{ color: 'var(--muted-foreground)' }}
        >
          Enter to send, Shift+Enter for newline
        </p>
      </div>
    </div>
  );
}
