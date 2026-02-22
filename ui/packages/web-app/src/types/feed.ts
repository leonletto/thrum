export interface FeedItem {
  id: string;
  type: 'message' | 'agent_registered' | 'session_started' | 'session_ended' | 'sync';
  from?: string;
  to?: string;
  preview?: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
}

export type FeedItemType = 'message' | 'agent_registered' | 'session_started' | 'session_ended';

export interface UnifiedFeedItem {
  id: string;
  type: FeedItemType;
  timestamp: string;
  // For messages
  from?: string;
  to?: string;
  preview?: string;
  messageId?: string;
  // For sessions
  agentId?: string;
  sessionId?: string;
  // For agent registration
  agentName?: string;
  role?: string;
}
