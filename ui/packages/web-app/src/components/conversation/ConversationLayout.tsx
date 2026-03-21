import { useState } from 'react';
import { ConversationList } from './ConversationList';
import { ConversationView } from './ConversationView';

interface ConversationLayoutProps {
  currentAgentId: string;
}

export function ConversationLayout({ currentAgentId }: ConversationLayoutProps) {
  const [selectedAgent, setSelectedAgent] = useState<string | undefined>();

  return (
    <div className="flex h-full">
      {/* Left panel: conversation list */}
      <div
        className={`w-72 flex-shrink-0 border-r border-[var(--border)] ${
          selectedAgent ? 'hidden md:block' : ''
        }`}
      >
        <ConversationList
          currentAgentId={currentAgentId}
          selectedAgentId={selectedAgent}
          onSelectAgent={setSelectedAgent}
        />
      </div>

      {/* Right panel: selected conversation or empty state */}
      <div className={`flex-1 min-w-0 ${!selectedAgent ? 'hidden md:flex' : 'flex'}`}>
        {selectedAgent ? (
          <div className="flex flex-col h-full w-full">
            {/* Mobile back button */}
            <div className="md:hidden flex-none px-3 py-2 border-b border-[var(--border)]">
              <button
                type="button"
                onClick={() => setSelectedAgent(undefined)}
                className="text-sm text-[var(--accent-color)] hover:underline"
              >
                ← Back
              </button>
            </div>
            <div className="flex-1 min-h-0">
              <ConversationView
                agentId={currentAgentId}
                withAgentId={selectedAgent}
              />
            </div>
          </div>
        ) : (
          <div className="flex items-center justify-center h-full w-full text-[var(--muted-foreground)]">
            <div className="text-center">
              <div className="text-lg mb-1">No conversation selected</div>
              <div className="text-sm">Choose a conversation from the list</div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
