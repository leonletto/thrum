import { useEffect } from 'react';
import { browserNotifications } from '../notifications/browser';
import { useWebSocketEvent } from './useWebSocket';
import type { Message } from '../types/api';

/**
 * Extract identity (agent or user) from a full ID like "agent:foo" or "user:bar"
 */
function extractIdentity(id: string | undefined): string | undefined {
  if (!id) return undefined;
  const parts = id.split(':');
  return parts.length > 1 ? parts[1] : id;
}

/**
 * Check if a message mentions a specific user or agent
 */
function isMentioned(message: Message, currentIdentity: string): boolean {
  const content = message.body.content || '';

  // Check for @mentions in content
  const mentionPattern = new RegExp(`@${currentIdentity}\\b`, 'i');
  return mentionPattern.test(content);
}

/**
 * Check if a message is a direct message to the current user
 */
function isDirectMessage(message: Message, currentUserId: string): boolean {
  // Check if message has a scope targeting this specific user
  return message.scopes.some(
    (scope) => scope.type === 'user' && scope.value === currentUserId
  );
}

/**
 * Hook to enable browser notifications for mentions and direct messages
 *
 * This hook:
 * - Requests notification permission
 * - Sets up idle tracking
 * - Listens for new messages
 * - Shows notifications for mentions and DMs
 * - Handles click to navigate to thread
 *
 * @param currentUserId - Full user ID (e.g., "user:alice")
 * @param projectName - Project name for notification title
 * @param onThreadClick - Callback when notification is clicked (receives thread_id)
 * @param enabled - Whether notifications are enabled (default: true)
 *
 * Example:
 * ```tsx
 * function App() {
 *   const { user } = useAuth();
 *   const navigate = useNavigate();
 *
 *   useBrowserNotifications(
 *     user?.userId,
 *     'MyProject',
 *     (threadId) => navigate(`/threads/${threadId}`)
 *   );
 *
 *   return <div>...</div>;
 * }
 * ```
 */
export function useBrowserNotifications(
  currentUserId: string | undefined,
  projectName: string,
  onThreadClick?: (threadId: string) => void,
  enabled = true
) {
  // Request permission and start idle tracking
  useEffect(() => {
    if (!enabled) return;

    browserNotifications.requestPermission().then((granted) => {
      if (granted) {
        browserNotifications.startIdleTracking();
      }
    });

    return () => {
      browserNotifications.cleanup();
    };
  }, [enabled]);

  // Listen for new messages
  useWebSocketEvent<{ message: Message }>('message.created', (data) => {
    if (!enabled || !currentUserId) return;

    const message = data.message;
    const currentIdentity = extractIdentity(currentUserId);

    if (!currentIdentity) return;

    // Check if this message is relevant to the current user
    const mentioned = isMentioned(message, currentIdentity);
    const direct = isDirectMessage(message, currentUserId);

    if (mentioned || direct) {
      // Get sender identity for notification
      const senderId = message.agent_id || message.session_id || 'Unknown';
      const senderIdentity = extractIdentity(senderId);

      // Truncate message content for preview
      const messageContent = message.body.content || '';
      const preview = messageContent.length > 100
        ? messageContent.slice(0, 100) + '...'
        : messageContent;

      // Show notification
      browserNotifications.notify(`Thrum · ${projectName}`, {
        body: `${senderIdentity}: "${preview}"`,
        tag: message.thread_id, // Group notifications by thread
        onClick: () => {
          if (message.thread_id && onThreadClick) {
            onThreadClick(message.thread_id);
          }
        },
      });
    }
  });
}

/**
 * Hook to get browser notification state
 *
 * Returns:
 * - permission: Current notification permission state
 * - isIdle: Whether user is currently idle
 * - requestPermission: Function to request notification permission
 *
 * Example:
 * ```tsx
 * function NotificationSettings() {
 *   const { permission, requestPermission } = useNotificationState();
 *
 *   if (permission === 'default') {
 *     return <button onClick={requestPermission}>Enable Notifications</button>;
 *   }
 *
 *   return <div>Notifications: {permission}</div>;
 * }
 * ```
 */
export function useNotificationState() {
  return {
    permission: browserNotifications.getPermission(),
    isIdle: browserNotifications.getIsIdle(),
    requestPermission: () => browserNotifications.requestPermission(),
  };
}
