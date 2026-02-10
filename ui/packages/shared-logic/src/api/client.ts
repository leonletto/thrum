import { ThrumWebSocket } from './websocket';

/**
 * Default WebSocket client instance
 *
 * This singleton is used by all hooks. You can reconfigure it before
 * connecting by setting the URL.
 *
 * Example:
 * ```ts
 * import { wsClient } from '@thrum/shared-logic';
 *
 * // Configure before first use
 * wsClient.disconnect(); // if already connected
 * const newClient = new ThrumWebSocket({ url: 'ws://localhost:9999/ws' });
 * ```
 */
export const wsClient = new ThrumWebSocket({
  url: typeof window !== 'undefined'
    ? `ws://${window.location.host}/ws`
    : 'ws://localhost:9999/ws',
  maxReconnectAttempts: 10,
  reconnectDelayMs: 1000,
  maxReconnectDelayMs: 30000,
  requestTimeoutMs: 30000,
});

/**
 * Ensure the WebSocket client is connected
 *
 * This is a helper function that ensures the client is connected before
 * making any API calls. It's used internally by the hooks.
 */
export async function ensureConnected(): Promise<void> {
  if (!wsClient.isConnected) {
    await wsClient.connect();
  }
}
