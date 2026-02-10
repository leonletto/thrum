import { useState } from 'react';
import type { ThreadListResponse } from '@thrum/shared-logic';
import { ThreadItem } from './ThreadItem';

interface ThreadListProps {
  threads: ThreadListResponse['threads'];
  sendingAs: string;
  isImpersonating: boolean;
}

export function ThreadList({ threads, sendingAs, isImpersonating }: ThreadListProps) {
  const [expandedThreadId, setExpandedThreadId] = useState<string | null>(null);

  if (threads.length === 0) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="empty-state">
          <div className="empty-icon">○</div>
          <div className="empty-text">NO THREADS</div>
          <div className="empty-subtext">Start a conversation</div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-2 p-4">
      {threads.map((thread) => (
        <ThreadItem
          key={thread.thread_id}
          thread={thread}
          expanded={expandedThreadId === thread.thread_id}
          onToggle={() => {
            setExpandedThreadId(
              expandedThreadId === thread.thread_id ? null : thread.thread_id
            );
          }}
          sendingAs={sendingAs}
          isImpersonating={isImpersonating}
        />
      ))}
    </div>
  );
}
