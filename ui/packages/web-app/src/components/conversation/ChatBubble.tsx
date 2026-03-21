import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { ConversationMessage } from '@thrum/shared-logic';

interface ChatBubbleProps {
  message: ConversationMessage;
  /** Whether this message is the first in a consecutive group from the same sender. */
  showSender: boolean;
  /** Whether this is a sent (right-aligned) message. */
  isSent: boolean;
  /** Optional quoted message for reply-to display. */
  replyToMessage?: ConversationMessage;
}

function formatTime(timestamp: string): string {
  const date = new Date(timestamp);
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

export const ChatBubble = memo(function ChatBubble({
  message,
  showSender,
  isSent,
  replyToMessage,
}: ChatBubbleProps) {
  const source =
    message.structured && typeof message.structured.source === 'string'
      ? message.structured.source
      : null;

  const senderName = message.author.display || message.author.agentId;

  return (
    <div
      className={cn(
        'flex flex-col max-w-[75%]',
        isSent ? 'ml-auto items-end' : 'mr-auto items-start'
      )}
    >
      {/* Sender name — only shown at the top of a group */}
      {showSender && !isSent && (
        <span
          className="text-xs font-medium mb-1 px-1"
          style={{ color: 'var(--accent-color)' }}
        >
          {senderName}
        </span>
      )}

      <div
        className={cn(
          'relative rounded-2xl px-3 py-2 text-sm',
          isSent
            ? 'rounded-tr-sm'
            : 'rounded-tl-sm'
        )}
        style={
          isSent
            ? {
                backgroundColor: 'var(--accent-color)',
                color: 'var(--panel-bg-start)',
              }
            : {
                backgroundColor: 'var(--accent-subtle-bg)',
                border: '1px solid var(--border)',
                color: 'rgb(var(--foreground))',
              }
        }
      >
        {/* Reply-to quote block */}
        {replyToMessage && (
          <div
            className="mb-2 rounded px-2 py-1 text-xs opacity-80 border-l-2"
            style={{
              borderLeftColor: isSent ? 'var(--panel-bg-start)' : 'var(--accent-color)',
              backgroundColor: isSent
                ? 'rgba(0,0,0,0.15)'
                : 'var(--accent-subtle-bg)',
            }}
          >
            <span
              className="font-medium block"
              style={{ color: isSent ? 'var(--panel-bg-start)' : 'var(--accent-color)' }}
            >
              {replyToMessage.author.display || replyToMessage.author.agentId}
            </span>
            <span className="truncate block max-w-[200px]">
              {replyToMessage.content.slice(0, 80)}
              {replyToMessage.content.length > 80 ? '…' : ''}
            </span>
          </div>
        )}

        {/* Message content */}
        <div
          className={cn(
            'prose prose-sm max-w-none',
            isSent && 'prose-invert'
          )}
          style={isSent ? { color: 'var(--panel-bg-start)' } : undefined}
        >
          <ReactMarkdown
            components={{
              p: ({ children }) => (
                <p className="m-0 leading-relaxed">{children}</p>
              ),
              pre: ({ children }) => (
                <pre
                  className="rounded p-2 text-xs overflow-x-auto mt-1"
                  style={{
                    backgroundColor: isSent
                      ? 'rgba(0,0,0,0.2)'
                      : 'var(--accent-subtle-bg)',
                  }}
                >
                  {children}
                </pre>
              ),
            }}
          >
            {message.content}
          </ReactMarkdown>
        </div>

        {/* Footer row: time + source badge */}
        <div
          className={cn(
            'flex items-center gap-1.5 mt-1 text-[10px]',
            isSent ? 'justify-end' : 'justify-start'
          )}
          style={{ opacity: 0.65 }}
        >
          <span>{formatTime(message.timestamp)}</span>
          {source && (
            <Badge
              variant="outline"
              className="h-3.5 px-1 text-[9px] leading-none"
              style={{
                borderColor: isSent ? 'var(--panel-bg-start)' : 'var(--accent-color)',
                color: isSent ? 'var(--panel-bg-start)' : 'var(--accent-color)',
              }}
            >
              via {source}
            </Badge>
          )}
        </div>
      </div>
    </div>
  );
});
