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
 * Waits for the WebSocket to reach OPEN state before returning.
 * If the client is already connecting (readyState === CONNECTING), this
 * attaches to the in-progress handshake rather than starting a new one,
 * so multiple callers racing during startup all wait correctly.
 *
 * If the connection is not established within the timeout, an error is thrown
 * so TanStack Query's retry logic can handle it.
 */
export async function ensureConnected(timeoutMs = 5000): Promise<void> {
  if (wsClient.isConnected) {
    return;
  }

  // connect() handles the CONNECTING case by returning a promise that resolves
  // on the 'open' event, so we can always await it safely.
  const connectPromise = wsClient.connect();

  // Race the connect promise against a timeout so we don't hang forever.
  // TanStack Query will retry on rejection.
  const timeoutPromise = new Promise<never>((_, reject) =>
    setTimeout(
      () => reject(new Error(`WebSocket connection timed out after ${timeoutMs}ms`)),
      timeoutMs
    )
  );

  await Promise.race([connectPromise, timeoutPromise]);
}
