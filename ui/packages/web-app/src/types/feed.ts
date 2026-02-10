export interface FeedItem {
  id: string;
  type: 'message' | 'agent_registered' | 'session_started' | 'session_ended' | 'sync';
  from?: string;
  to?: string;
  preview?: string;
  timestamp: string;
  metadata?: Record<string, unknown>;
}
